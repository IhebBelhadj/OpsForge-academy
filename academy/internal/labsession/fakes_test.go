package labsession_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// memRepo is the Repo in memory.
type memRepo struct {
	mu       sync.Mutex
	attempts []labsession.Attempt
	steps    map[int64]map[string]bool
	hints    map[int64]map[string]int
}

func newRepo() *memRepo {
	return &memRepo{steps: map[int64]map[string]bool{}, hints: map[int64]map[string]int{}}
}

func (r *memRepo) Create(_ context.Context, a labsession.Attempt) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.attempts {
		if b.UserID == a.UserID && b.Status.Active() {
			return 0, labsession.ErrAnotherLabRunning
		}
	}
	a.ID = int64(len(r.attempts) + 1)
	r.attempts = append(r.attempts, a)
	return a.ID, nil
}

func (r *memRepo) SetPlaces(_ context.Context, id int64, ns, ws string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[id-1].Namespace, r.attempts[id-1].Workspace = ns, ws
	return nil
}

func (r *memRepo) Attempt(_ context.Context, id int64) (labsession.Attempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id < 1 || int(id) > len(r.attempts) {
		return labsession.Attempt{}, labsession.ErrAttemptNotFound
	}
	return r.attempts[id-1], nil
}

func (r *memRepo) Active(_ context.Context, user int64) (labsession.Attempt, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.attempts {
		if a.UserID == user && a.Status.Active() {
			return a, true, nil
		}
	}
	return labsession.Attempt{}, false, nil
}

func (r *memRepo) Finish(_ context.Context, a labsession.Attempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := &r.attempts[a.ID-1]
	cur.Status, cur.FinishedAt, cur.Seconds, cur.XP, cur.Error = a.Status, a.FinishedAt, a.Seconds, a.XP, a.Error
	return nil
}

func (r *memRepo) PassedSteps(_ context.Context, id int64) (map[string]bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]bool{}
	for k, v := range r.steps[id] {
		out[k] = v
	}
	return out, nil
}

func (r *memRepo) RecordStep(_ context.Context, id int64, step string, passed bool, _ json.RawMessage, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.steps[id] == nil {
		r.steps[id] = map[string]bool{}
	}
	if passed {
		r.steps[id][step] = true
	}
	return nil
}

func (r *memRepo) ClearSteps(_ context.Context, id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.steps, id)
	return nil
}

func (r *memRepo) Hints(_ context.Context, id int64) (map[string]int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]int{}
	for k, v := range r.hints[id] {
		out[k] = v
	}
	return out, nil
}

func (r *memRepo) ShowHint(_ context.Context, id int64, step string, idx int, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hints[id] == nil {
		r.hints[id] = map[string]int{}
	}
	if r.hints[id][step] <= idx {
		r.hints[id][step] = idx + 1
		r.attempts[id-1].HintsUsed++
	}
	return nil
}

func (r *memRepo) Passed(_ context.Context, user int64) ([]labsession.Attempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []labsession.Attempt
	for _, a := range r.attempts {
		if a.UserID == user && a.Status == labsession.Passed {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *memRepo) Recover(context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for i := range r.attempts {
		if r.attempts[i].Status == labsession.Setup {
			r.attempts[i].Status = labsession.Running
			n++
		}
	}
	return n, nil
}

// tour is a lab with one check of each kind, its own namespace, setup and teardown, and two hints on its probe.
const tour = `{"id":"tour","xp":100,"layout":"stretched","needs":["k3s"],"requires":[],"files":{"web.yaml":"kind: Pod\n"},
"body":{"namespace":"auto","setup":[{"run":{"cmd":"setup"}}],"teardown":[{"run":{"cmd":"teardown"}}],
"steps":[
 {"id":"look","hints":["h1","h2"],"check":{"probe":{"steps":[{"run":{"cmd":"p"},"name":"p"}],"expect":{"p.exit":"== 0"}}}},
 {"id":"why","check":{"quiz":{"question":"?","choices":["a","b"],"answer":1,"explain":"because"}}},
 {"id":"done","check":{"confirm":"Done"}}]}}`

// catalogue serves tour, "next" (requires tour), "far" (multi-cluster), "snap" (resets from a snapshot), "plain"
// (no steps, no namespace, one confirm) and "storm" (it changes the cluster), in a pack with a hint penalty of 0.25.
type catalogue struct{}

func (catalogue) Entry(_ context.Context, kind catalog.Kind, key string) (catalog.Entry, error) {
	docs := map[string]string{
		"lab/tour":  tour,
		"lab/next":  `{"xp":50,"requires":["tour"],"body":{"steps":[{"id":"a","check":{"confirm":"ok"}}]}}`,
		"lab/far":   `{"xp":50,"layout":"multi-cluster","body":{"steps":[{"id":"a","check":{"confirm":"ok"}}]}}`,
		"lab/snap":  `{"xp":50,"body":{"reset":"snapshot","steps":[{"id":"a","check":{"confirm":"ok"}}]}}`,
		"lab/plain": `{"xp":10,"body":{"steps":[{"id":"a","check":{"confirm":"ok"}}]}}`,
		"lab/storm": `{"xp":10,"body":{"cluster_changes":["labels a node"],"steps":[{"id":"a","check":{"confirm":"ok"}}]}}`,
		"pack/p":    `{"id":"p","hint_penalty":0.25}`,
	}
	doc, ok := docs[string(kind)+"/"+key]
	if !ok {
		return catalog.Entry{}, catalog.ErrNotFound
	}
	return catalog.Entry{Kind: kind, Key: key, PackID: "p", Hash: "H-" + key, Doc: json.RawMessage(doc)}, nil
}

// fakeSandboxes is the Sandboxes port: a stretched lab whose sandboxes are numbered 00000001, ..., with a call log.
type fakeSandboxes struct {
	mu        sync.Mutex
	layout    string
	refuse    error             // Create's refusal
	awaitErr  error             // Await's error (a lost job)
	failSetup string            // the setup's error: the sandbox fails (and is cleaned up)
	teardown  string            // the teardown's error, when deleted
	probes    []lab.ProbeResult // returned in order; passed when empty
	probeErr  error
	made      int
	requests  map[string]lab.SandboxRequest
	gone      map[string]bool
	calls     []string
}

func newSandboxes() *fakeSandboxes {
	return &fakeSandboxes{layout: "stretched", requests: map[string]lab.SandboxRequest{}, gone: map[string]bool{}}
}

func (l *fakeSandboxes) log(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *fakeSandboxes) called() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.calls, ", ")
}

func (l *fakeSandboxes) Create(_ context.Context, req lab.SandboxRequest) (lab.Pending, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refuse != nil {
		return lab.Pending{}, l.refuse
	}
	if req.Requires.Layout != nil && *req.Requires.Layout != l.layout {
		return lab.Pending{}, &lab.UnmetError{Unmet: []string{"layout " + *req.Requires.Layout}}
	}
	l.made++
	id := fmt.Sprintf("%08x", l.made)
	l.requests[id] = req
	l.calls = append(l.calls, fmt.Sprintf("create %s", id))
	return lab.Pending{Sandbox: id, Job: "job-" + id}, nil
}

func (l *fakeSandboxes) sandbox(id string) lab.Sandbox {
	sb := lab.Sandbox{ID: id, Status: lab.SandboxReady, Setup: &lab.RunResult{}}
	if l.requests[id].Namespace {
		ns := "sb-" + id
		sb.Namespace = &ns
	}
	if l.failSetup != "" {
		sb.Status, sb.Setup.Error, sb.Teardown = lab.SandboxFailed, l.failSetup, &lab.RunResult{}
	}
	return sb
}

func (l *fakeSandboxes) Await(_ context.Context, p lab.Pending) (lab.Sandbox, error) {
	l.log("await " + p.Sandbox)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.awaitErr != nil {
		return lab.Sandbox{}, l.awaitErr
	}
	return l.sandbox(p.Sandbox), nil
}

func (l *fakeSandboxes) Probe(_ context.Context, id string, _ lab.ProbeRequest) (lab.ProbeResult, error) {
	l.log("probe " + id)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gone[id] {
		return lab.ProbeResult{}, fmt.Errorf("%w: no sandbox %s", lab.ErrGone, id)
	}
	if l.probeErr != nil {
		return lab.ProbeResult{}, l.probeErr
	}
	if len(l.probes) == 0 {
		return lab.ProbeResult{Passed: true, Results: []lab.Expectation{{Metric: "p.exit", Want: "== 0", Passed: true,
			Checked: true}}}, nil
	}
	r := l.probes[0]
	l.probes = l.probes[1:]
	return r, nil
}

func (l *fakeSandboxes) Reset(_ context.Context, id string) (lab.Sandbox, error) {
	l.log("reset " + id)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sandbox(id), nil
}

func (l *fakeSandboxes) Delete(_ context.Context, id string) (lab.Sandbox, error) {
	l.log("delete " + id)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gone[id] {
		return lab.Sandbox{}, fmt.Errorf("%w: no sandbox %s", lab.ErrGone, id)
	}
	l.gone[id] = true
	return lab.Sandbox{ID: id, Status: lab.SandboxDeleted, Teardown: &lab.RunResult{Error: l.teardown}}, nil
}

// spaces records the claimed workspaces (and fails claims with err).
type spaces struct {
	mu      sync.Mutex
	claimed map[string]int64
	err     error
}

func (s *spaces) Claim(_ context.Context, user int64, ws string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.claimed[ws] = user
	return nil
}

// bus records the history.
type bus struct {
	mu     sync.Mutex
	events []platform.Event
}

func (b *bus) Publish(_ context.Context, e platform.Event) (platform.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, e)
	return e, nil
}

func (b *bus) kinds() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ks []string
	for _, e := range b.events {
		ks = append(ks, e.Kind)
	}
	return strings.Join(ks, " ")
}

type opsRepo struct {
	mu  sync.Mutex
	ops map[string]operations.Operation
}

func (r *opsRepo) Create(_ context.Context, op operations.Operation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops[op.ID] = op
	return nil
}

func (r *opsRepo) Finish(ctx context.Context, op operations.Operation) error {
	return r.Create(ctx, op)
}

func (r *opsRepo) Get(_ context.Context, id string) (operations.Operation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ops[id], nil
}

func (r *opsRepo) FailRunning(context.Context, string, time.Time) (int64, error) { return 0, nil }

type nobody struct{}

func (nobody) Send(int64, platform.Message) {}

type fixture struct {
	svc    *labsession.Service
	repo   *memRepo
	lab    *fakeSandboxes
	spaces *spaces
	ops    *operations.Service
	bus    *bus
	clock  *clock
}

func newFixture() *fixture {
	f := &fixture{repo: newRepo(), lab: newSandboxes(), spaces: &spaces{claimed: map[string]int64{}}, bus: &bus{},
		clock: &clock{now: t0}}
	f.ops = operations.NewService(&opsRepo{ops: map[string]operations.Operation{}}, nobody{}, f.clock,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.svc = labsession.NewService(f.repo, catalogue{}, f.lab, f.spaces, f.ops, f.bus, f.clock)
	return f
}

// outcome waits for an operation and returns its outcome.
func (f *fixture) outcome(t *testing.T, op operations.Operation) labsession.Outcome {
	t.Helper()
	if err := f.ops.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	done, err := f.ops.Get(context.Background(), op.UserID, op.ID)
	if err != nil || done.Status != operations.Done {
		t.Fatalf("operation %+v: %v (error %v)", done, err, done.Error)
	}
	var out labsession.Outcome
	if err := json.Unmarshal(done.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// start starts a lab for user 1 and waits for its setup.
func (f *fixture) start(t *testing.T, labID string) (labsession.Attempt, labsession.Outcome) {
	t.Helper()
	a, op, err := f.svc.Start(context.Background(), 1, labID, "k8s", "item")
	if err != nil {
		t.Fatalf("start %s: %v", labID, err)
	}
	return a, f.outcome(t, op)
}

// check checks a step of attempt id and waits for it.
func (f *fixture) check(t *testing.T, id int64, step string, answer *int) labsession.Outcome {
	t.Helper()
	op, err := f.svc.Check(context.Background(), 1, id, step, answer)
	if err != nil {
		t.Fatalf("check %s: %v", step, err)
	}
	return f.outcome(t, op)
}
