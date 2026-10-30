package challenge_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

var t0 = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return t0 }

type memRepo struct {
	mu   sync.Mutex
	subs []challenge.Submission
}

func (r *memRepo) Create(_ context.Context, s challenge.Submission) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.ID = int64(len(r.subs) + 1)
	r.subs = append(r.subs, s)
	return s.ID, nil
}

func (r *memRepo) Submissions(_ context.Context, user int64, course, item string) ([]challenge.Submission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []challenge.Submission
	for i := len(r.subs) - 1; i >= 0; i-- {
		if s := r.subs[i]; s.UserID == user && s.Course == course && s.Item == item {
			out = append(out, s)
		}
	}
	return out, nil
}

func (r *memRepo) Accepted(_ context.Context, user int64) ([]challenge.Submission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []challenge.Submission
	for _, s := range r.subs {
		if s.UserID == user && s.Verdict == challenge.Accepted {
			out = append(out, s)
		}
	}
	return out, nil
}

// content holds the course "dsa": two-sum (a coding challenge) and pods (a manifest challenge).
type content struct{}

func (content) Content(_ context.Context, course, id string) (catalog.ItemRef, catalog.Entry, error) {
	if course != "dsa" {
		return catalog.ItemRef{}, catalog.Entry{}, catalog.ErrNotFound
	}
	switch id {
	case "two-sum":
		d := twoSum()
		d.Solution = map[string]string{"solution.py": "py", "solution.js": "js", "solution.go": "go"}
		doc, _ := json.Marshal(d)
		return catalog.ItemRef{ID: id, Type: catalog.ItemChallenge, Ref: id, Hash: "H1"}, catalog.Entry{Doc: doc}, nil
	case "pods":
		return catalog.ItemRef{ID: id, Type: catalog.ItemChallenge, Ref: id},
			catalog.Entry{Doc: []byte(`{"files":["pod.yaml"]}`)}, nil
	case "intro":
		return catalog.ItemRef{ID: id, Type: catalog.ItemLecture}, catalog.Entry{}, nil
	}
	return catalog.ItemRef{}, catalog.Entry{}, catalog.ErrNotFound
}

// sandboxes is OpsForge: Judge answers the action with the judge's output (from the files it got).
type sandboxes struct {
	mu      sync.Mutex
	refuse  error
	created []lab.SandboxRequest
	acted   [][]json.RawMessage
	deleted []string
	judge   func(files map[string]string) lab.RunResult
}

func (s *sandboxes) Create(_ context.Context, req lab.SandboxRequest) (lab.Pending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse != nil {
		return lab.Pending{}, s.refuse
	}
	s.created = append(s.created, req)
	return lab.Pending{Sandbox: "sb1", Job: "j1"}, nil
}

func (s *sandboxes) Await(context.Context, lab.Pending) (lab.Sandbox, error) {
	return lab.Sandbox{ID: "sb1", Status: lab.SandboxReady}, nil
}

func (s *sandboxes) Act(_ context.Context, _ string, steps []json.RawMessage) (lab.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acted = append(s.acted, steps)
	return s.judge(s.created[len(s.created)-1].Files), nil
}

func (s *sandboxes) Delete(_ context.Context, id string) (lab.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return lab.Sandbox{ID: id, Status: lab.SandboxDeleted}, nil
}

// syncOps runs an operation's work at once and keeps its result.
type syncOps struct{ results []json.RawMessage }

func (o *syncOps) Start(ctx context.Context, user int64, kind, subject string, work operations.Work) (
	operations.Operation, error) {
	res, err := work(ctx)
	if err != nil {
		return operations.Operation{}, err
	}
	data, _ := json.Marshal(res)
	o.results = append(o.results, data)
	return operations.Operation{ID: "op", Kind: kind, Subject: subject, Status: operations.Done, Result: data}, nil
}

type events struct{ got []platform.Event }

func (e *events) Publish(_ context.Context, ev platform.Event) (platform.Event, error) {
	e.got = append(e.got, ev)
	return ev, nil
}

type world struct {
	svc    *challenge.Service
	repo   *memRepo
	boxes  *sandboxes
	ops    *syncOps
	events *events
}

// answers is a judge returning want for every case, unless the code says "wrong".
func answers(files map[string]string) lab.RunResult {
	var tests struct{ Cases []json.RawMessage }
	_ = json.Unmarshal([]byte(files["tests.json"]), &tests)
	all := twoSum().Cases
	var out strings.Builder
	for i := range tests.Cases {
		got := string(all[i].Want)
		if strings.Contains(files["solution.py"], "wrong") {
			got = "[9,9]"
		}
		out.WriteString(line(`{"i":` + string(rune('0'+i)) + `,"got":` + got + `}`))
	}
	return lab.RunResult{Metrics: map[string]float64{"judge.exit": 0, "judge.seconds": 0.4},
		Outputs: map[string]string{"judge": out.String()}}
}

func newWorld() *world {
	w := &world{repo: &memRepo{}, boxes: &sandboxes{judge: answers}, ops: &syncOps{}, events: &events{}}
	w.svc = challenge.NewService(w.repo, content{}, w.boxes, w.ops, w.events, fixedClock{})
	return w
}

func outcome(t *testing.T, op operations.Operation) challenge.Outcome {
	t.Helper()
	var o challenge.Outcome
	if err := json.Unmarshal(op.Result, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestARunTriesTheSamplesInAThrowawaySandbox(t *testing.T) {
	w := newWorld()
	op, err := w.svc.Run(context.Background(), 1, "dsa", "two-sum", "python", "def two_sum(n, t): ...")
	if err != nil {
		t.Fatal(err)
	}
	o := outcome(t, op)
	if op.Kind != challenge.OpRun || o.Verdict != challenge.Accepted || o.Total != 2 || len(o.Cases) != 2 ||
		o.Submission != nil || o.Language != "python" {
		t.Fatalf("%+v", o)
	}
	req := w.boxes.created[0]
	if req.Kind != "challenge" || !req.Namespace || req.Requires.Runtimes[0] != "python" ||
		req.Requires.Needs[0] != "k3s" || req.ExpiresAfter != challenge.SandboxExpiry ||
		req.Files["solution.py"] != "def two_sum(n, t): ..." || strings.Contains(req.Files["tests.json"], "want") {
		t.Fatalf("%+v", req)
	}
	if len(w.boxes.deleted) != 1 || len(w.repo.subs) != 0 || len(w.events.got) != 0 {
		t.Fatalf("deleted %v, recorded %v", w.boxes.deleted, w.repo.subs)
	}
}

func TestSubmissionsAreRecordedAndTheFirstAcceptedEarnsTheXP(t *testing.T) {
	w := newWorld()
	ctx := context.Background()
	op, _ := w.svc.Submit(ctx, 1, "dsa", "two-sum", "python", "wrong")
	if o := outcome(t, op); o.Verdict != challenge.WrongAnswer || o.Total != 3 || len(o.Cases) != 1 || o.XP != 0 ||
		*o.Submission != 1 {
		t.Fatalf("%+v", o)
	}
	if _, err := w.svc.Solution(ctx, 1, "dsa", "two-sum"); !errors.Is(err, challenge.ErrSolutionLocked) {
		t.Fatal(err)
	}
	op, _ = w.svc.Submit(ctx, 1, "dsa", "two-sum", "python", "right")
	if o := outcome(t, op); o.Verdict != challenge.Accepted || o.XP != 50 {
		t.Fatalf("%+v", o)
	}
	op, _ = w.svc.Submit(ctx, 1, "dsa", "two-sum", "python", "right again")
	if o := outcome(t, op); o.XP != 0 {
		t.Fatalf("XP once: %+v", o)
	}
	kinds := []string{}
	for _, e := range w.events.got {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "challenge.submitted,challenge.accepted,challenge.accepted" ||
		w.events.got[1].Subject != "dsa/two-sum" {
		t.Fatalf("%v", kinds)
	}
	subs, _ := w.svc.Submissions(ctx, 1, "dsa", "two-sum")
	if len(subs) != 3 || subs[0].Code != "right again" || subs[2].Hash != "H1" {
		t.Fatalf("%+v", subs)
	}
	sol, err := w.svc.Solution(ctx, 1, "dsa", "two-sum")
	if err != nil || sol["go"] != "go" || sol["python"] != "py" {
		t.Fatalf("%v %v", sol, err)
	}
	if accepted, _ := w.svc.Accepted(ctx, 1); len(accepted) != 2 {
		t.Fatalf("%+v", accepted)
	}
}

func TestWhatOpsForgeCannotJudgeIsNotRecorded(t *testing.T) {
	w := newWorld()
	w.boxes.judge = func(map[string]string) lab.RunResult {
		return lab.RunResult{Error: "program: the pod did not start in 120s"}
	}
	op, _ := w.svc.Submit(context.Background(), 1, "dsa", "two-sum", "go", "package main")
	if o := outcome(t, op); o.Verdict != challenge.Error || !strings.Contains(o.Error, "did not start") ||
		o.Submission != nil {
		t.Fatalf("%+v", o)
	}
	if len(w.repo.subs) != 0 || len(w.boxes.deleted) != 1 {
		t.Fatalf("%+v", w.repo.subs)
	}
}

func TestRefusals(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name         string
		course, item string
		lang, code   string
		refuse       error
		want         error
	}{
		{"unknown item", "dsa", "nope", "python", "", nil, catalog.ErrNotFound},
		{"a lecture", "dsa", "intro", "python", "", nil, challenge.ErrNotACodeChallenge},
		{"a manifest challenge", "dsa", "pods", "python", "", nil, challenge.ErrNotACodeChallenge},
		{"another language", "dsa", "two-sum", "ruby", "", nil, challenge.ErrLanguage},
		{"too much code", "dsa", "two-sum", "go", strings.Repeat("x", challenge.MaxCode+1), nil,
			challenge.ErrCodeTooLarge},
		{"lab busy", "dsa", "two-sum", "go", "", lab.ErrBusy, challenge.ErrBusy},
		{"lab down", "dsa", "two-sum", "go", "", lab.ErrLabDown, challenge.ErrLabDown},
		{"offline", "dsa", "two-sum", "go", "", lab.ErrOffline, challenge.ErrOffline},
		{"no runtime", "dsa", "two-sum", "go", "", &lab.UnmetError{Unmet: []string{"runtime go"}},
			challenge.ErrUnavailable},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			w.boxes.refuse = c.refuse
			if _, err := w.svc.Run(ctx, 1, c.course, c.item, c.lang, c.code); !errors.Is(err, c.want) {
				t.Fatalf("%v", err)
			}
			if len(w.ops.results) != 0 {
				t.Fatal("an operation started")
			}
		})
	}
}
