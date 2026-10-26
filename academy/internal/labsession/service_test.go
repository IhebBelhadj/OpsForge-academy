package labsession_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession"
)

func ptr[T any](v T) *T { return &v }

func TestStartMakesTheAttemptsSandbox(t *testing.T) {
	f := newFixture()
	a, out := f.start(t, "tour")
	if a.Status != labsession.Setup || a.Workspace != "00000001" || a.Namespace != "sb-00000001" ||
		a.Hash != "H-tour" || a.Course != "k8s" || a.Item != "item" {
		t.Fatalf("started %+v", a)
	}
	if out.Status != labsession.Running || out.Error != nil || out.Attempt != 1 {
		t.Fatalf("outcome %+v", out)
	}
	if f.lab.called() != "create 00000001, await 00000001" {
		t.Fatalf("calls: %s", f.lab.called())
	}
	req := f.lab.requests["00000001"]
	if req.Kind != "lab" || req.Label != "lab tour (user 1)" || !req.Namespace || *req.Requires.Layout != "stretched" ||
		len(req.Requires.Needs) != 1 || !req.Requires.Faults || req.Requires.ClusterChanges ||
		req.Files["web.yaml"] != "kind: Pod\n" || len(req.Setup) != 1 || len(req.Teardown) != 1 {
		t.Fatalf("request %+v", req)
	}
	if f.spaces.claimed["00000001"] != 1 {
		t.Fatalf("workspace %+v", f.spaces.claimed)
	}
	if stored, _ := f.repo.Attempt(context.Background(), 1); stored.Status != labsession.Running ||
		stored.Workspace != "00000001" {
		t.Fatalf("stored %+v", stored)
	}
	if f.bus.kinds() != "lab.started" {
		t.Fatalf("events %s", f.bus.kinds())
	}
}

func TestALabWithoutStepsGetsASandboxToo(t *testing.T) {
	f := newFixture()
	a, out := f.start(t, "plain")
	if out.Status != labsession.Running || a.Namespace != "" || f.lab.called() != "create 00000001, await 00000001" {
		t.Fatalf("%+v, calls %s", out, f.lab.called())
	}
}

// The learner's terminal can break links in any lab: every lab's sandbox requires faults (OpsForge heals when
// deleting it); a lab that changes the cluster says so too.
func TestALabsSandboxMayBreakLinks(t *testing.T) {
	f := newFixture()
	f.start(t, "storm")
	if req := f.lab.requests["00000001"]; !req.Requires.Faults || !req.Requires.ClusterChanges || req.Namespace {
		t.Fatalf("request %+v", req)
	}
}

func TestStartRefusals(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		lab   string
		setup func(*fixture)
		want  error
	}{
		"unknown lab":  {"nope", nil, labsession.ErrLabNotFound},
		"locked":       {"next", nil, labsession.ErrLocked},
		"unmet":        {"far", nil, labsession.ErrUnavailable},
		"lab down":     {"tour", func(f *fixture) { f.lab.refuse = fmt.Errorf("%w: up it", lab.ErrLabDown) }, labsession.ErrLabDown},
		"lab busy":     {"tour", func(f *fixture) { f.lab.refuse = fmt.Errorf("%w: a job runs", lab.ErrBusy) }, labsession.ErrBusy},
		"no opsforge":  {"tour", func(f *fixture) { f.lab.refuse = errors.New("connection refused") }, labsession.ErrToolchain},
		"another lab":  {"tour", func(f *fixture) { f.start(t, "plain") }, labsession.ErrAnotherLabRunning},
		"no workspace": {"tour", func(f *fixture) { f.spaces.err = errors.New("disk full") }, errAny},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture()
			if tc.setup != nil {
				tc.setup(f)
			}
			_, _, err := f.svc.Start(ctx, 1, tc.lab, "", "")
			if errors.Is(tc.want, errAny) {
				if err == nil {
					t.Fatal("started")
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// Refused or failed, it leaves no active attempt behind and the lab free
			if _, active, _ := f.repo.Active(ctx, 1); active && name != "another lab" {
				t.Fatal("an attempt is still active")
			}
			if name != "another lab" {
				f.lab.refuse, f.spaces.err = nil, nil
				if _, _, err := f.svc.Start(ctx, 1, "plain", "", ""); err != nil {
					t.Fatalf("the lab stayed reserved: %v", err)
				}
			}
		})
	}
	var locked *labsession.LockedError
	_, _, err := newFixture().svc.Start(ctx, 1, "next", "", "")
	if !errors.As(err, &locked) || len(locked.Missing) != 1 || locked.Missing[0] != "tour" {
		t.Fatalf("%v", err)
	}
	var unmet *lab.UnmetError
	if _, _, err := newFixture().svc.Start(ctx, 1, "far", "", ""); !errors.As(err, &unmet) ||
		unmet.Unmet[0] != "layout multi-cluster" {
		t.Fatalf("unmet: %v", err)
	}
}

func TestASandboxMadeForNothingIsDeleted(t *testing.T) {
	f := newFixture()
	f.spaces.err = errors.New("disk full")
	if _, _, err := f.svc.Start(context.Background(), 1, "plain", "", ""); err == nil {
		t.Fatal("started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(f.lab.called(), "delete 00000001") {
		if time.Now().After(deadline) {
			t.Fatalf("calls: %s", f.lab.called())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// errAny marks a case that only needs to fail (no sentinel to match).
var errAny = errors.New("any error")

func TestAFailedSetupFailsTheAttempt(t *testing.T) {
	f := newFixture()
	f.lab.failSetup = "step 1 failed: exit 1"
	_, out := f.start(t, "tour")
	if out.Status != labsession.Failed || out.Error == nil || *out.Error != "step 1 failed: exit 1" {
		t.Fatalf("%+v", out)
	}
	if f.lab.called() != "create 00000001, await 00000001" { // OpsForge cleaned the sandbox up
		t.Fatalf("calls: %s", f.lab.called())
	}
	if f.bus.kinds() != "lab.started lab.failed" {
		t.Fatalf("events %s", f.bus.kinds())
	}
	a, _ := f.repo.Attempt(context.Background(), 1)
	if a.Status != labsession.Failed || a.FinishedAt == nil || a.Error == "" {
		t.Fatalf("%+v", a)
	}
	f.lab.failSetup = ""
	if _, out := f.start(t, "tour"); out.Status != labsession.Running {
		t.Fatalf("starting again: %+v", out)
	}
	g := newFixture()
	g.lab.awaitErr = errors.New("opsforge create job x: opsforge lost the job")
	if _, out := g.start(t, "tour"); out.Status != labsession.Failed || !strings.Contains(*out.Error, "lost the job") {
		t.Fatalf("lost setup: %+v", out)
	}
}

func TestStepsAreCheckedInOrderUntilThePass(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	a, _ := f.start(t, "tour")

	if _, err := f.svc.Check(ctx, 1, a.ID, "why", ptr(1)); !errors.Is(err, labsession.ErrStepOrder) {
		t.Fatalf("out of order: %v", err)
	}
	if _, err := f.svc.Check(ctx, 1, a.ID, "nope", nil); !errors.Is(err, labsession.ErrNoSuchStep) {
		t.Fatalf("unknown step: %v", err)
	}
	f.lab.probes = []lab.ProbeResult{{Passed: false, Error: "nodes: exit 1",
		Results: []lab.Expectation{{Metric: "p.exit", Want: "== 0", Got: ptr(1.0), Checked: true}}}}
	out := f.check(t, a.ID, "look", nil)
	if *out.Passed || out.Error == nil || len(out.Results) != 1 || out.Status != labsession.Running {
		t.Fatalf("failing probe: %+v", out)
	}
	if out := f.check(t, a.ID, "look", nil); !*out.Passed {
		t.Fatalf("passing probe: %+v", out)
	}
	if _, err := f.svc.Check(ctx, 1, a.ID, "why", nil); !errors.Is(err, labsession.ErrAnswerRequired) {
		t.Fatalf("quiz without answer: %v", err)
	}
	if out := f.check(t, a.ID, "why", ptr(0)); *out.Passed || out.Explain != nil {
		t.Fatalf("wrong answer: %+v", out)
	}
	if out := f.check(t, a.ID, "why", ptr(1)); !*out.Passed || *out.Explain != "because" {
		t.Fatalf("right answer: %+v", out)
	}
	f.clock.add(90 * time.Second)
	out = f.check(t, a.ID, "done", nil)
	if out.Status != labsession.Passed || *out.XP != 100 || *out.Seconds != 90 || out.Error != nil {
		t.Fatalf("pass: %+v", out)
	}
	if f.lab.called() != "create 00000001, await 00000001, probe 00000001, probe 00000001, delete 00000001" {
		t.Fatalf("calls: %s", f.lab.called())
	}
	if f.bus.kinds() != "lab.started step.checked step.checked step.checked step.checked step.checked lab.passed" {
		t.Fatalf("events %s", f.bus.kinds())
	}
	if _, err := f.svc.Check(ctx, 1, a.ID, "done", nil); !errors.Is(err, labsession.ErrNotRunning) {
		t.Fatalf("after the pass: %v", err)
	}
	passed, _ := f.svc.Passed(ctx, 1)
	if len(passed) != 1 || passed[0].XP != 100 {
		t.Fatalf("%+v", passed)
	}
	// The pass unlocks the lab that requires it
	if _, out := f.start(t, "next"); out.Status != labsession.Running {
		t.Fatalf("%+v", out)
	}
}

func TestAProbeOpsForgeCannotRunFailsTheCheck(t *testing.T) {
	f := newFixture()
	a, _ := f.start(t, "tour")
	f.lab.probeErr = errors.New("opsforge probe: contract 2")
	if out := f.check(t, a.ID, "look", nil); *out.Passed || *out.Error != "opsforge probe: contract 2" {
		t.Fatalf("%+v", out)
	}
	f.lab.gone["00000001"] = true // expired
	if out := f.check(t, a.ID, "look", nil); *out.Passed || !strings.Contains(*out.Error, "sandbox is gone") {
		t.Fatalf("%+v", out)
	}
}

func TestHintsCostXPOnce(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	a, _ := f.start(t, "tour")
	h, err := f.svc.Hint(ctx, 1, a.ID, "look")
	if err != nil || h.Index != 0 || h.Text != "h1" || h.HintsUsed != 1 {
		t.Fatalf("%+v %v", h, err)
	}
	if h, _ := f.svc.Hint(ctx, 1, a.ID, "look"); h.Index != 1 || h.Text != "h2" || h.HintsUsed != 2 {
		t.Fatalf("%+v", h)
	}
	if _, err := f.svc.Hint(ctx, 1, a.ID, "look"); !errors.Is(err, labsession.ErrNoMoreHints) {
		t.Fatalf("third: %v", err)
	}
	if _, err := f.svc.Hint(ctx, 1, a.ID, "why"); !errors.Is(err, labsession.ErrNoMoreHints) {
		t.Fatalf("a step without hints: %v", err)
	}
	if _, err := f.svc.Hint(ctx, 1, a.ID, "nope"); !errors.Is(err, labsession.ErrNoSuchStep) {
		t.Fatalf("unknown step: %v", err)
	}
	v, _ := f.svc.View(ctx, 1, a.ID)
	if len(v.Steps[0].Hints) != 2 || v.Current != "look" || v.HintsUsed != 2 {
		t.Fatalf("view %+v", v)
	}
	f.check(t, a.ID, "look", nil)
	f.check(t, a.ID, "why", ptr(1))
	if out := f.check(t, a.ID, "done", nil); *out.XP != 50 { // 100 × (1 − 2 × 0.25)
		t.Fatalf("%+v", out)
	}
}

func TestResetStartsTheStepsOver(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	a, _ := f.start(t, "tour")
	f.check(t, a.ID, "look", nil)
	op, err := f.svc.Reset(ctx, 1, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out := f.outcome(t, op); out.Status != labsession.Running || out.Error != nil {
		t.Fatalf("%+v", out)
	}
	if f.lab.called() != "create 00000001, await 00000001, probe 00000001, reset 00000001" {
		t.Fatalf("calls: %s", f.lab.called())
	}
	if v, _ := f.svc.View(ctx, 1, a.ID); v.Current != "look" || v.Steps[0].Passed {
		t.Fatalf("%+v", v)
	}
	f.lab.failSetup = "setup failed again"
	op, _ = f.svc.Reset(ctx, 1, a.ID)
	if out := f.outcome(t, op); out.Status != labsession.Failed || *out.Error != "setup failed again" {
		t.Fatalf("failed reset: %+v", out)
	}

	g := newFixture()
	s, _ := g.start(t, "snap")
	if _, err := g.svc.Reset(ctx, 1, s.ID); !errors.Is(err, labsession.ErrResetUnsupported) {
		t.Fatalf("snapshot lab: %v", err)
	}
}

func TestAbandonDeletesTheSandboxAndEndsTheAttempt(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	a, _ := f.start(t, "tour")
	f.lab.teardown = "heal: wan-core unreachable"
	op, err := f.svc.Abandon(ctx, 1, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := f.outcome(t, op)
	if out.Status != labsession.Abandoned || out.Error == nil || *out.Error != "heal: wan-core unreachable" {
		t.Fatalf("%+v", out)
	}
	if f.lab.called() != "create 00000001, await 00000001, delete 00000001" {
		t.Fatalf("calls: %s", f.lab.called())
	}
	if _, err := f.svc.Abandon(ctx, 1, a.ID); !errors.Is(err, labsession.ErrNotRunning) {
		t.Fatalf("twice: %v", err)
	}
	if _, ok, _ := f.svc.Current(ctx, 1); ok {
		t.Fatal("still current")
	}
	if f.bus.kinds() != "lab.started lab.abandoned" {
		t.Fatalf("events %s", f.bus.kinds())
	}
}

func TestAttemptsAreTheirUsers(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	a, _ := f.start(t, "tour")
	for name, err := range map[string]error{
		"check":   func() error { _, err := f.svc.Check(ctx, 2, a.ID, "look", nil); return err }(),
		"hint":    func() error { _, err := f.svc.Hint(ctx, 2, a.ID, "look"); return err }(),
		"reset":   func() error { _, err := f.svc.Reset(ctx, 2, a.ID); return err }(),
		"abandon": func() error { _, err := f.svc.Abandon(ctx, 2, a.ID); return err }(),
		"view":    func() error { _, err := f.svc.View(ctx, 2, a.ID); return err }(),
		"unknown": func() error { _, err := f.svc.View(ctx, 1, 99); return err }(),
	} {
		if !errors.Is(err, labsession.ErrAttemptNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if v, ok, err := f.svc.Current(ctx, 1); err != nil || !ok || v.ID != a.ID || v.Current != "look" || len(v.Steps) != 3 {
		t.Fatalf("current %+v %v %v", v, ok, err)
	}
	if _, ok, _ := f.svc.Current(ctx, 2); ok {
		t.Fatal("user 2 has user 1's attempt")
	}
}

// slowLab blocks probes until released, to hold the lab busy.
type slowLab struct {
	*fakeSandboxes
	release chan struct{}
}

func (l slowLab) Probe(ctx context.Context, id string, req lab.ProbeRequest) (lab.ProbeResult, error) {
	<-l.release
	return l.fakeSandboxes.Probe(ctx, id, req)
}

func TestOneLabOperationAtATime(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	slow := slowLab{fakeSandboxes: f.lab, release: make(chan struct{})}
	f.svc = labsession.NewService(f.repo, catalogue{}, slow, f.spaces, f.ops, f.bus, f.clock)
	a, _ := f.start(t, "tour")
	op, err := f.svc.Check(ctx, 1, a.ID, "look", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"check":   func() error { _, err := f.svc.Check(ctx, 1, a.ID, "look", nil); return err }(),
		"reset":   func() error { _, err := f.svc.Reset(ctx, 1, a.ID); return err }(),
		"abandon": func() error { _, err := f.svc.Abandon(ctx, 1, a.ID); return err }(),
		"start":   func() error { _, _, err := f.svc.Start(ctx, 2, "plain", "", ""); return err }(),
	} {
		if !errors.Is(err, labsession.ErrBusy) {
			t.Errorf("%s while a probe runs: %v", name, err)
		}
	}
	if _, err := f.svc.Hint(ctx, 1, a.ID, "look"); err != nil { // a hint touches no lab
		t.Fatalf("hint while a probe runs: %v", err)
	}
	close(slow.release)
	if out := f.outcome(t, op); !*out.Passed {
		t.Fatalf("%+v", out)
	}
	if _, err := f.svc.Abandon(ctx, 1, a.ID); err != nil {
		t.Fatalf("after the probe: %v", err)
	}
}

func TestRecoverLetsAnInterruptedSetupBeAbandoned(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	id, _ := f.repo.Create(ctx, labsession.Attempt{UserID: 1, Lab: "tour", Status: labsession.Setup, StartedAt: t0})
	if _, err := f.svc.Abandon(ctx, 1, id); !errors.Is(err, labsession.ErrNotRunning) {
		t.Fatalf("in setup: %v", err)
	}
	if n, err := f.svc.Recover(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	op, err := f.svc.Abandon(ctx, 1, id)
	if err != nil {
		t.Fatal(err)
	}
	if out := f.outcome(t, op); out.Status != labsession.Abandoned || out.Error != nil {
		t.Fatalf("%+v", out) // no sandbox yet: nothing to delete
	}
}

func TestASandboxAlreadyGoneIsNoTeardownError(t *testing.T) {
	f := newFixture()
	a, _ := f.start(t, "tour")
	f.lab.gone["00000001"] = true
	op, err := f.svc.Abandon(context.Background(), 1, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out := f.outcome(t, op); out.Status != labsession.Abandoned || out.Error != nil {
		t.Fatalf("%+v", out)
	}
}
