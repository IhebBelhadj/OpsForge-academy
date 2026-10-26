package labsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// Service runs lab attempts, each in its sandbox. busy is the one lab operation at a time: taken before an
// operation starts (a second one is refused, never queued) and released when its background work ends.
type Service struct {
	repo    Repo
	catalog Catalog
	lab     Sandboxes
	spaces  Workspaces
	ops     Operations
	events  Publisher
	clock   platform.Clock
	busy    sync.Mutex
}

// NewService runs labs in sandboxes.
func NewService(repo Repo, cat Catalog, sandboxes Sandboxes, spaces Workspaces, ops Operations,
	events Publisher, clock platform.Clock) *Service {
	return &Service{repo: repo, catalog: cat, lab: sandboxes, spaces: spaces, ops: ops, events: events, clock: clock}
}

// definition is a live lab with its pack's hint penalty.
func (s *Service) definition(ctx context.Context, labID string) (Definition, error) {
	e, err := s.catalog.Entry(ctx, catalog.KindLab, labID)
	if errors.Is(err, catalog.ErrNotFound) {
		return Definition{}, ErrLabNotFound
	}
	if err != nil {
		return Definition{}, err
	}
	pack, err := s.catalog.Entry(ctx, catalog.KindPack, e.PackID)
	if err != nil {
		return Definition{}, fmt.Errorf("lab %s: its pack: %w", labID, err)
	}
	return Decode(e, pack)
}

// take reserves the lab for one operation; the returned release is safe to call more than once.
func (s *Service) take() (func(), error) {
	if !s.busy.TryLock() {
		return nil, ErrBusy
	}
	return sync.OnceFunc(s.busy.Unlock), nil
}

func (s *Service) publish(ctx context.Context, a Attempt, kind string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["attempt"] = a.ID
	doc, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.events.Publish(ctx, platform.Event{UserID: a.UserID, Kind: kind, Subject: a.Lab, Data: doc,
		At: s.clock.Now()})
	return err
}

// Start begins an attempt at a lab (from course/item, or "" for none): its sandbox is made, and its setup awaited
// in the background. It is refused while another attempt is active, the lab is busy or down, the running lab
// cannot host it (ErrUnavailable) or its required labs are not passed.
func (s *Service) Start(ctx context.Context, user int64, labID, course, item string) (Attempt, operations.Operation, error) {
	def, err := s.definition(ctx, labID)
	if err != nil {
		return Attempt{}, operations.Operation{}, err
	}
	_, active, err := s.repo.Active(ctx, user)
	if err != nil {
		return Attempt{}, operations.Operation{}, err
	}
	if active {
		return Attempt{}, operations.Operation{}, ErrAnotherLabRunning
	}
	if err := s.unlocked(ctx, user, def); err != nil {
		return Attempt{}, operations.Operation{}, err
	}
	release, err := s.take()
	if err != nil {
		return Attempt{}, operations.Operation{}, err
	}
	handedOver := false // to the setup, which releases the lab when it ends
	defer func() {
		if !handedOver {
			release()
		}
	}()
	pending, err := s.lab.Create(ctx, s.request(user, def))
	if err != nil {
		return Attempt{}, operations.Operation{}, refusal(err)
	}

	a := Attempt{UserID: user, Lab: labID, Course: course, Item: item, Hash: def.Hash, Status: Setup,
		StartedAt: s.clock.Now(), Workspace: pending.Sandbox}
	if def.Namespace {
		a.Namespace = "sb-" + pending.Sandbox // the contract's name for a sandbox's namespace
	}
	if a.ID, err = s.repo.Create(ctx, a); err != nil {
		go s.discard(context.WithoutCancel(ctx), pending) // made for nothing: OpsForge would expire it, but sooner is better
		return Attempt{}, operations.Operation{}, err
	}
	op, err := s.begin(ctx, a, pending, release)
	if err != nil {
		s.fail(ctx, &a, err.Error())
		go s.discard(context.WithoutCancel(ctx), pending)
		return Attempt{}, operations.Operation{}, err
	}
	handedOver = true
	return a, op, nil
}

// begin records a new attempt's places and owner, then awaits its sandbox's setup in the background (which
// releases the lab when it ends).
func (s *Service) begin(ctx context.Context, a Attempt, pending lab.Pending, release func()) (operations.Operation, error) {
	if err := s.repo.SetPlaces(ctx, a.ID, a.Namespace, a.Workspace); err != nil {
		return operations.Operation{}, err
	}
	if err := s.spaces.Claim(ctx, a.UserID, a.Workspace); err != nil {
		return operations.Operation{}, fmt.Errorf("workspace: %w", err)
	}
	if err := s.publish(ctx, a, KindStarted, map[string]any{"content_hash": a.Hash}); err != nil {
		return operations.Operation{}, err
	}
	return s.ops.Start(ctx, a.UserID, OpStart, a.Lab, func(ctx context.Context) (any, error) {
		defer release()
		sb, err := s.lab.Await(ctx, pending)
		return s.settle(ctx, a, sb, err)
	})
}

// request is def's sandbox for user. It always requires faults: besides its setup, the learner's terminal can break
// links (`opsforge partition`, `degrade`: several labs ask for it), so OpsForge heals the lab when it deletes the
// sandbox, as Phase 10 healed after every lab. Such a sandbox runs alone, as labs do here (one at a time).
func (s *Service) request(user int64, def Definition) lab.SandboxRequest {
	return lab.SandboxRequest{Kind: "lab", Label: fmt.Sprintf("lab %s (user %d)", def.ID, user),
		Requires:  lab.Requires{Layout: def.Layout, Needs: def.Needs, Faults: true, ClusterChanges: def.ClusterChanges},
		Namespace: def.Namespace, Files: def.Files, Setup: def.Setup, Teardown: def.Teardown}
}

// refusal is why OpsForge would not make a sandbox, as this module's errors.
func refusal(err error) error {
	switch {
	case errors.Is(err, lab.ErrLabDown):
		return ErrLabDown
	case errors.Is(err, lab.ErrBusy):
		return fmt.Errorf("%w: %w", ErrBusy, err)
	case errors.Is(err, lab.ErrUnmet):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return fmt.Errorf("%w: %w", ErrToolchain, err)
}

// discard deletes a sandbox nobody will use, once its setup ended (best effort; ctx outlives the request).
func (s *Service) discard(ctx context.Context, p lab.Pending) {
	if _, err := s.lab.Await(ctx, p); err == nil {
		_, _ = s.lab.Delete(ctx, p.Sandbox)
	}
}

// unlocked checks that the user passed every lab def requires.
func (s *Service) unlocked(ctx context.Context, user int64, def Definition) error {
	if len(def.Requires) == 0 {
		return nil
	}
	passed, err := s.repo.Passed(ctx, user)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, p := range passed {
		done[p.Lab] = true
	}
	var missing []string
	for _, r := range def.Requires {
		if !done[r] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return &LockedError{Missing: missing}
	}
	return nil
}

// settle is the end of a sandbox's setup (sb, or err when it could not be awaited): the attempt runs, or it failed
// (OpsForge already cleaned the sandbox up after a failed setup).
func (s *Service) settle(ctx context.Context, a Attempt, sb lab.Sandbox, err error) (Outcome, error) {
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	case sb.Status != lab.SandboxReady:
		msg = setupError(sb)
	}
	if msg != "" {
		s.fail(ctx, &a, msg)
		return Outcome{Attempt: a.ID, Status: a.Status, Error: &msg, Results: []lab.Expectation{}}, nil
	}
	a.Status = Running
	if err := s.repo.Finish(ctx, a); err != nil {
		return Outcome{}, err
	}
	return Outcome{Attempt: a.ID, Status: a.Status, Results: []lab.Expectation{}}, nil
}

// setupError is why a failed sandbox's setup failed, with its cleanup's error if that failed too.
func setupError(sb lab.Sandbox) string {
	msg := "setup failed"
	if sb.Setup != nil && sb.Setup.Error != "" {
		msg = sb.Setup.Error
	}
	if sb.Teardown != nil && sb.Teardown.Error != "" {
		msg += "\nteardown: " + sb.Teardown.Error
	}
	return msg
}

// tearDown deletes the attempt's sandbox (its teardown, its namespace, a heal when it broke links); "" when that
// went well (or there was nothing left to delete).
func (s *Service) tearDown(ctx context.Context, a Attempt) string {
	if a.Workspace == "" { // never got a sandbox
		return ""
	}
	sb, err := s.lab.Delete(ctx, a.Workspace)
	switch {
	case errors.Is(err, lab.ErrGone):
		return ""
	case err != nil:
		return err.Error()
	case sb.Teardown != nil && sb.Teardown.Error != "":
		return sb.Teardown.Error
	}
	return ""
}

// fail records a failed setup (best effort: the attempt must not stay active).
func (s *Service) fail(ctx context.Context, a *Attempt, msg string) {
	now := s.clock.Now()
	a.Status, a.FinishedAt, a.Error = Failed, &now, msg
	if s.repo.Finish(ctx, *a) == nil {
		_ = s.publish(ctx, *a, KindFailed, map[string]any{"error": msg})
	}
}

// running is user's attempt id, running, with its lab.
func (s *Service) running(ctx context.Context, user, id int64) (Attempt, Definition, error) {
	a, err := s.repo.Attempt(ctx, id)
	if err != nil {
		return Attempt{}, Definition{}, err
	}
	if a.UserID != user {
		return Attempt{}, Definition{}, ErrAttemptNotFound
	}
	if a.Status != Running {
		return Attempt{}, Definition{}, ErrNotRunning
	}
	def, err := s.definition(ctx, a.Lab)
	return a, def, err
}

// Check checks a step of a running attempt: the first step not passed yet. A probe runs on the lab; a quiz takes
// answer (a choice index). Passing the last step passes the attempt: the teardown runs and the XP is earned.
func (s *Service) Check(ctx context.Context, user, id int64, stepID string, answer *int) (operations.Operation, error) {
	a, def, err := s.running(ctx, user, id)
	if err != nil {
		return operations.Operation{}, err
	}
	if _, ok := def.Step(stepID); !ok {
		return operations.Operation{}, ErrNoSuchStep
	}
	passed, err := s.repo.PassedSteps(ctx, a.ID)
	if err != nil {
		return operations.Operation{}, err
	}
	step, last, ok := def.Current(passed)
	if !ok || step.ID != stepID {
		return operations.Operation{}, ErrStepOrder
	}
	if step.Check.Type == CheckQuiz && answer == nil {
		return operations.Operation{}, ErrAnswerRequired
	}
	release, err := s.take()
	if err != nil {
		return operations.Operation{}, err
	}
	op, err := s.ops.Start(ctx, user, OpCheck, a.Lab, func(ctx context.Context) (any, error) {
		defer release()
		return s.check(ctx, a, def, step, last, answer)
	})
	if err != nil {
		release()
	}
	return op, err
}

func (s *Service) check(ctx context.Context, a Attempt, def Definition, step Step, last bool, answer *int) (Outcome, error) {
	out := Outcome{Attempt: a.ID, Status: Running, Step: &step.ID, Results: []lab.Expectation{}}
	ok := false
	detail := map[string]any{}
	switch step.Check.Type {
	case CheckConfirm:
		ok = true
	case CheckQuiz:
		ok = *answer == step.Check.Answer
		detail["answer"] = *answer
		if ok && step.Check.Explain != "" {
			out.Explain = &step.Check.Explain
		}
	case CheckProbe:
		res, err := s.lab.Probe(ctx, a.Workspace, *step.Check.Probe)
		if err != nil {
			msg := err.Error()
			if errors.Is(err, lab.ErrGone) {
				msg = "this attempt's sandbox is gone (OpsForge deletes one after a day without use): abandon the " +
					"attempt and start the lab again"
			}
			out.Error = &msg
		} else {
			ok = res.Passed
			if res.Results != nil {
				out.Results = res.Results
			}
			if res.Error != "" {
				out.Error = &res.Error
			}
		}
		detail["results"], detail["error"] = out.Results, out.Error
	}
	out.Passed = &ok
	detail["passed"] = ok
	doc, err := json.Marshal(detail)
	if err != nil {
		return Outcome{}, err
	}
	if err := s.repo.RecordStep(ctx, a.ID, step.ID, ok, doc, s.clock.Now()); err != nil {
		return Outcome{}, err
	}
	if err := s.publish(ctx, a, KindStepChecked, map[string]any{"step": step.ID, "passed": ok}); err != nil {
		return Outcome{}, err
	}
	if !ok || !last {
		return out, nil
	}
	return s.pass(ctx, a.ID, def, out)
}

// pass finishes an attempt whose steps all passed: its sandbox deleted, then its time and XP (hints read now).
func (s *Service) pass(ctx context.Context, id int64, def Definition, out Outcome) (Outcome, error) {
	a, err := s.repo.Attempt(ctx, id)
	if err != nil {
		return Outcome{}, err
	}
	if msg := s.tearDown(ctx, a); msg != "" {
		out.Error = &msg
	}
	now := s.clock.Now()
	a.Status, a.FinishedAt = Passed, &now
	a.Seconds = int(now.Sub(a.StartedAt).Seconds())
	a.XP = PassXP(def.XP, a.HintsUsed, def.HintPenalty)
	if err := s.repo.Finish(ctx, a); err != nil {
		return Outcome{}, err
	}
	if err := s.publish(ctx, a, KindPassed, map[string]any{"xp": a.XP, "hints": a.HintsUsed,
		"seconds": a.Seconds, "content_hash": a.Hash}); err != nil {
		return Outcome{}, err
	}
	out.Status, out.XP, out.Seconds = Passed, &a.XP, &a.Seconds
	return out, nil
}

// Hint opens the next hint of a step of a running attempt. Each hint counts against the pass once.
func (s *Service) Hint(ctx context.Context, user, id int64, stepID string) (HintShown, error) {
	a, def, err := s.running(ctx, user, id)
	if err != nil {
		return HintShown{}, err
	}
	step, ok := def.Step(stepID)
	if !ok {
		return HintShown{}, ErrNoSuchStep
	}
	shown, err := s.repo.Hints(ctx, a.ID)
	if err != nil {
		return HintShown{}, err
	}
	idx := shown[stepID]
	if idx >= len(step.Hints) {
		return HintShown{}, ErrNoMoreHints
	}
	if err := s.repo.ShowHint(ctx, a.ID, stepID, idx, s.clock.Now()); err != nil {
		return HintShown{}, err
	}
	if err := s.publish(ctx, a, KindHintShown, map[string]any{"step": stepID, "index": idx}); err != nil {
		return HintShown{}, err
	}
	if a, err = s.repo.Attempt(ctx, a.ID); err != nil {
		return HintShown{}, err
	}
	return HintShown{Namespace: a.Namespace, Step: stepID, Index: idx, Text: step.Hints[idx], HintsUsed: a.HintsUsed}, nil
}

// Reset sets a running attempt's sandbox up again (its teardown, then its setup; the files stay); its steps start
// over (hints stay counted).
func (s *Service) Reset(ctx context.Context, user, id int64) (operations.Operation, error) {
	a, def, err := s.running(ctx, user, id)
	if err != nil {
		return operations.Operation{}, err
	}
	if def.Reset != "setup" {
		return operations.Operation{}, ErrResetUnsupported
	}
	release, err := s.take()
	if err != nil {
		return operations.Operation{}, err
	}
	op, err := s.ops.Start(ctx, user, OpReset, a.Lab, func(ctx context.Context) (any, error) {
		defer release()
		if err := s.repo.ClearSteps(ctx, a.ID); err != nil {
			return nil, err
		}
		if err := s.publish(ctx, a, KindReset, nil); err != nil {
			return nil, err
		}
		sb, err := s.lab.Reset(ctx, a.Workspace)
		return s.settle(ctx, a, sb, err)
	})
	if err != nil {
		release()
	}
	return op, err
}

// Abandon ends a running attempt: its sandbox deleted, nothing earned.
func (s *Service) Abandon(ctx context.Context, user, id int64) (operations.Operation, error) {
	a, _, err := s.running(ctx, user, id)
	if err != nil {
		return operations.Operation{}, err
	}
	release, err := s.take()
	if err != nil {
		return operations.Operation{}, err
	}
	op, err := s.ops.Start(ctx, user, OpAbandon, a.Lab, func(ctx context.Context) (any, error) {
		defer release()
		out := Outcome{Attempt: a.ID, Status: Abandoned, Results: []lab.Expectation{}}
		if msg := s.tearDown(ctx, a); msg != "" {
			out.Error = &msg
		}
		now := s.clock.Now()
		a.Status, a.FinishedAt = Abandoned, &now
		if err := s.repo.Finish(ctx, a); err != nil {
			return nil, err
		}
		return out, s.publish(ctx, a, KindAbandoned, nil)
	})
	if err != nil {
		release()
	}
	return op, err
}

// View is one of user's attempts with its steps (hint texts as shown so far).
func (s *Service) View(ctx context.Context, user, id int64) (View, error) {
	a, err := s.repo.Attempt(ctx, id)
	if err != nil {
		return View{}, err
	}
	if a.UserID != user {
		return View{}, ErrAttemptNotFound
	}
	return s.view(ctx, a)
}

// Current is user's active attempt, if any.
func (s *Service) Current(ctx context.Context, user int64) (View, bool, error) {
	a, ok, err := s.repo.Active(ctx, user)
	if err != nil || !ok {
		return View{}, false, err
	}
	v, err := s.view(ctx, a)
	return v, err == nil, err
}

func (s *Service) view(ctx context.Context, a Attempt) (View, error) {
	v := View{Attempt: a, Steps: []StepState{}}
	def, err := s.definition(ctx, a.Lab)
	if errors.Is(err, ErrLabNotFound) { // retired since: the attempt is still the learner's
		return v, nil
	}
	if err != nil {
		return View{}, err
	}
	passed, err := s.repo.PassedSteps(ctx, a.ID)
	if err != nil {
		return View{}, err
	}
	shown, err := s.repo.Hints(ctx, a.ID)
	if err != nil {
		return View{}, err
	}
	for _, step := range def.Steps {
		st := StepState{ID: step.ID, Passed: passed[step.ID], Hints: []string{}}
		for i := 0; i < shown[step.ID] && i < len(step.Hints); i++ {
			st.Hints = append(st.Hints, step.Hints[i])
		}
		v.Steps = append(v.Steps, st)
	}
	if a.Status == Running {
		if step, _, ok := def.Current(passed); ok {
			v.Current = step.ID
		}
	}
	return v, nil
}

// Passed are user's passed attempts, oldest first (their completions and XP).
func (s *Service) Passed(ctx context.Context, user int64) ([]Attempt, error) {
	return s.repo.Passed(ctx, user)
}

// Recover makes the attempts a stopped process left in setup running, so they can be abandoned (their operation
// is failed by operations.Service.Recover).
func (s *Service) Recover(ctx context.Context) (int64, error) {
	return s.repo.Recover(ctx)
}
