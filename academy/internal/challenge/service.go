package challenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// History event kinds.
const (
	KindSubmitted = "challenge.submitted"
	KindAccepted  = "challenge.accepted"
)

// Operation kinds.
const (
	OpRun    = "challenge.run"
	OpSubmit = "challenge.submit"
)

// SandboxExpiry is how long OpsForge keeps a judging sandbox Academy lost (seconds): it deletes each one itself.
const SandboxExpiry = 600

// Service runs and submits code, and lists submissions.
type Service struct {
	repo      Repo
	content   Content
	sandboxes Sandboxes
	ops       Operations
	events    Publisher
	clock     platform.Clock
}

// NewService judges on sandboxes and reads challenges through content.
func NewService(repo Repo, content Content, sandboxes Sandboxes, ops Operations, events Publisher,
	clock platform.Clock) *Service {
	return &Service{repo: repo, content: content, sandboxes: sandboxes, ops: ops, events: events, clock: clock}
}

// Outcome is a run's or a submission's operation result: the verdict, and the submission it recorded (submissions
// that OpsForge could not judge are not recorded).
type Outcome struct {
	Result
	Submission *int64 `json:"submission,omitempty"`
	XP         int    `json:"xp"`
}

// def reads a coding challenge item.
func (s *Service) def(ctx context.Context, course, item string) (catalog.ItemRef, Def, error) {
	ref, e, err := s.content.Content(ctx, course, item)
	if err != nil {
		return ref, Def{}, err
	}
	if ref.Type != catalog.ItemChallenge {
		return ref, Def{}, ErrNotACodeChallenge
	}
	d, err := Decode(e.Doc)
	return ref, d, err
}

// Run judges code on the challenge's sample cases (202: the operation's result is an Outcome).
func (s *Service) Run(ctx context.Context, user int64, course, item, language, code string) (operations.Operation,
	error) {
	return s.start(ctx, user, course, item, language, code, false)
}

// Submit judges code on every case and records the submission (202: the operation's result is an Outcome).
func (s *Service) Submit(ctx context.Context, user int64, course, item, language, code string) (operations.Operation,
	error) {
	return s.start(ctx, user, course, item, language, code, true)
}

func (s *Service) start(ctx context.Context, user int64, course, item, language, code string, submit bool) (
	operations.Operation, error) {
	ref, d, err := s.def(ctx, course, item)
	if err != nil {
		return operations.Operation{}, err
	}
	lang, err := d.Language(language)
	if err != nil {
		return operations.Operation{}, err
	}
	if len(code) > MaxCode {
		return operations.Operation{}, fmt.Errorf("%w: at most %d KiB", ErrCodeTooLarge, MaxCode/1024)
	}
	cases, kind := d.Cases, OpSubmit
	if !submit {
		cases, kind = d.Cases[:min(d.Samples, len(d.Cases))], OpRun
	}
	files, err := Files(d, lang, code, cases)
	if err != nil {
		return operations.Operation{}, err
	}
	steps, err := Steps(lang, d)
	if err != nil {
		return operations.Operation{}, err
	}
	pending, err := s.sandboxes.Create(ctx, lab.SandboxRequest{
		Kind: "challenge", Label: fmt.Sprintf("challenge %s/%s, %s, user %d", course, item, language, user),
		Requires:  lab.Requires{Needs: []string{"k3s"}, Runtimes: []string{lang.Runtime}},
		Namespace: true, Files: files, ExpiresAfter: SandboxExpiry,
	})
	if err != nil {
		return operations.Operation{}, refusal(err)
	}
	op, err := s.ops.Start(ctx, user, kind, course+"/"+item, func(ctx context.Context) (any, error) {
		res := s.judge(ctx, d, cases, pending, steps, submit)
		res.Language = language
		out := Outcome{Result: res}
		if !submit || res.Verdict == Error {
			return out, nil
		}
		sub, err := s.record(ctx, user, course, item, ref.Hash, d, language, code, res)
		if err != nil {
			return nil, err
		}
		out.Submission, out.XP = &sub.ID, sub.XP
		return out, nil
	})
	if err != nil {
		_, _ = s.sandboxes.Delete(context.WithoutCancel(ctx), pending.Sandbox)
		return operations.Operation{}, err
	}
	return op, nil
}

// refusal maps OpsForge's refusal to create a sandbox to the module's errors.
func refusal(err error) error {
	var unmet *lab.UnmetError
	switch {
	case errors.As(err, &unmet):
		return fmt.Errorf("%w: %s", ErrUnavailable, strings.Join(unmet.Unmet, "; "))
	case errors.Is(err, lab.ErrBusy):
		return ErrBusy
	case errors.Is(err, lab.ErrLabDown):
		return ErrLabDown
	case errors.Is(err, lab.ErrOffline):
		return ErrOffline
	}
	return err
}

// judge waits for the sandbox, runs the judge in it, deletes it and compares the results.
func (s *Service) judge(ctx context.Context, d Def, cases []Case, p lab.Pending, steps []json.RawMessage,
	submit bool) Result {
	defer func() { _, _ = s.sandboxes.Delete(ctx, p.Sandbox) }()
	run := Run{StopAtFirstFail: submit}
	sb, err := s.sandboxes.Await(ctx, p)
	switch {
	case err != nil:
		run.Failure = "setting up the sandbox: " + err.Error()
	case sb.Status != lab.SandboxReady:
		run.Failure = "the sandbox could not be set up"
		if sb.Setup != nil && sb.Setup.Error != "" {
			run.Failure += ": " + sb.Setup.Error
		}
	}
	if run.Failure != "" {
		return Judge(d, cases, run)
	}
	out, err := s.sandboxes.Act(ctx, p.Sandbox, steps)
	switch {
	case errors.Is(err, lab.ErrBusy):
		run.Failure = "the lab is busy with other work; try again in a moment"
	case err != nil:
		run.Failure = err.Error()
	case out.Error != "":
		run.Failure = out.Error
	default:
		m := out.Metrics
		run.Exit, run.Seconds = m["judge.exit"], m["judge.seconds"]
		run.TimedOut, run.OOM = m["judge.timed_out"] == 1, m["judge.oom"] == 1
		run.Output = out.Outputs["judge"]
	}
	return Judge(d, cases, run)
}

// record stores a judged submission (the challenge's XP on the first accepted one) and tells the history.
func (s *Service) record(ctx context.Context, user int64, course, item, hash string, d Def, language, code string,
	res Result) (Submission, error) {
	sub := Submission{UserID: user, Course: course, Item: item, Hash: hash, Language: language, Code: code,
		Verdict: res.Verdict, Passed: res.Passed, Total: res.Total, Seconds: res.Seconds, Result: res,
		SubmittedAt: s.clock.Now()}
	if res.Verdict == Accepted {
		accepted, err := s.repo.Accepted(ctx, user)
		if err != nil {
			return sub, err
		}
		sub.XP = d.XP
		for _, a := range accepted {
			if a.Course == sub.Course && a.Item == sub.Item {
				sub.XP = 0
				break
			}
		}
	}
	var err error
	if sub.ID, err = s.repo.Create(ctx, sub); err != nil {
		return sub, err
	}
	kind := KindSubmitted
	if res.Verdict == Accepted {
		kind = KindAccepted
	}
	data, _ := json.Marshal(map[string]any{"submission": sub.ID, "language": language, "verdict": res.Verdict,
		"passed": res.Passed, "total": res.Total, "xp": sub.XP, "content_hash": sub.Hash})
	_, err = s.events.Publish(ctx, platform.Event{UserID: user, Kind: kind, Subject: sub.Course + "/" + sub.Item,
		Data: data, At: sub.SubmittedAt})
	return sub, err
}

// Submissions are user's submissions to a coding challenge, newest first.
func (s *Service) Submissions(ctx context.Context, user int64, course, item string) ([]Submission, error) {
	if _, _, err := s.def(ctx, course, item); err != nil {
		return nil, err
	}
	return s.repo.Submissions(ctx, user, course, item)
}

// Solution is the reference solution per language, once user has an accepted submission.
func (s *Service) Solution(ctx context.Context, user int64, course, item string) (map[string]string, error) {
	_, d, err := s.def(ctx, course, item)
	if err != nil {
		return nil, err
	}
	subs, err := s.repo.Submissions(ctx, user, course, item)
	if err != nil {
		return nil, err
	}
	for _, sub := range subs {
		if sub.Verdict == Accepted {
			out := map[string]string{}
			for _, name := range d.Languages {
				out[name] = d.Solution[Languages[name].File]
			}
			return out, nil
		}
	}
	return nil, ErrSolutionLocked
}

// Accepted are user's accepted submissions, oldest first (completions and XP).
func (s *Service) Accepted(ctx context.Context, user int64) ([]Submission, error) {
	return s.repo.Accepted(ctx, user)
}
