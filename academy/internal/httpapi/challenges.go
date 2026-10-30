package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
)

// Challenges is what the transport needs from the challenge module.
type Challenges interface {
	Run(ctx context.Context, user int64, course, item, language, code string) (operations.Operation, error)
	Submit(ctx context.Context, user int64, course, item, language, code string) (operations.Operation, error)
	Submissions(ctx context.Context, user int64, course, item string) ([]challenge.Submission, error)
	Solution(ctx context.Context, user int64, course, item string) (map[string]string, error)
}

// challengeProblem maps a challenge error to its problem document (status 0: not a domain error; catalog.ErrNotFound
// is the caller's: itemNotFound).
func challengeProblem(err error) Problem {
	switch {
	case errors.Is(err, challenge.ErrNotACodeChallenge):
		return problem(http.StatusConflict, "/problems/not-a-code-challenge", "Not a coding challenge",
			"this item is not a coding challenge")
	case errors.Is(err, challenge.ErrSolutionLocked):
		return problem(http.StatusConflict, "/problems/solution-locked", "The solution is locked", err.Error())
	case errors.Is(err, challenge.ErrBusy):
		return problem(http.StatusConflict, "/problems/lab-busy", "The lab is busy",
			"the lab is running other work (a lab you have open runs alone); try again when it ends")
	case errors.Is(err, challenge.ErrLabDown):
		return problem(http.StatusConflict, "/problems/lab-down", "The lab is down", "start it with ./opsforge up")
	case errors.Is(err, challenge.ErrUnavailable):
		return problem(http.StatusConflict, "/problems/lab-unavailable", "Not on this lab", err.Error())
	case errors.Is(err, challenge.ErrLanguage):
		return problem(http.StatusUnprocessableEntity, "/problems/bad-language", "Not a language of this challenge",
			err.Error())
	case errors.Is(err, challenge.ErrCodeTooLarge):
		return problem(http.StatusUnprocessableEntity, "/problems/code-too-large", "The code is too large", err.Error())
	case errors.Is(err, challenge.ErrOffline):
		return problem(http.StatusServiceUnavailable, "/problems/labs-offline", "Labs are offline",
			"code runs on OpsForge, which Academy cannot reach right now; the courses and quizzes still work")
	}
	return Problem{}
}

// RunCode judges code on a challenge's examples.
func (s *server) RunCode(ctx context.Context, req RunCodeRequestObject) (RunCodeResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	op, err := s.deps.Challenges.Run(ctx, user, req.Course, req.Item, string(req.Body.Language), req.Body.Code)
	if errors.Is(err, catalog.ErrNotFound) {
		return RunCode404ApplicationProblemPlusJSONResponse{itemNotFound(req.Course, req.Item)}, nil
	}
	if p := challengeProblem(err); p.Status != 0 {
		switch p.Status {
		case http.StatusConflict:
			return RunCode409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusUnprocessableEntity:
			return RunCode422ApplicationProblemPlusJSONResponse(p), nil
		default:
			return RunCode503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := operation(op)
	return RunCode202JSONResponse(out), err
}

// SubmitCode judges code on every case and records the submission.
func (s *server) SubmitCode(ctx context.Context, req SubmitCodeRequestObject) (SubmitCodeResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	op, err := s.deps.Challenges.Submit(ctx, user, req.Course, req.Item, string(req.Body.Language), req.Body.Code)
	if errors.Is(err, catalog.ErrNotFound) {
		return SubmitCode404ApplicationProblemPlusJSONResponse{itemNotFound(req.Course, req.Item)}, nil
	}
	if p := challengeProblem(err); p.Status != 0 {
		switch p.Status {
		case http.StatusConflict:
			return SubmitCode409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusUnprocessableEntity:
			return SubmitCode422ApplicationProblemPlusJSONResponse(p), nil
		default:
			return SubmitCode503ApplicationProblemPlusJSONResponse(p), nil
		}
	}
	if err != nil {
		return nil, err
	}
	out, err := operation(op)
	return SubmitCode202JSONResponse(out), err
}

// ListSubmissions is the learner's submissions to a challenge.
func (s *server) ListSubmissions(ctx context.Context, req ListSubmissionsRequestObject) (ListSubmissionsResponseObject,
	error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	subs, err := s.deps.Challenges.Submissions(ctx, user, req.Course, req.Item)
	if errors.Is(err, catalog.ErrNotFound) {
		return ListSubmissions404ApplicationProblemPlusJSONResponse{itemNotFound(req.Course, req.Item)}, nil
	}
	if p := challengeProblem(err); p.Status == http.StatusConflict {
		return ListSubmissions409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	out := ListSubmissions200JSONResponse{Submissions: make([]Submission, len(subs))}
	for i, sub := range subs {
		var result ChallengeOutcome
		if err := convert(challenge.Outcome{Result: sub.Result, Submission: &sub.ID, XP: sub.XP}, &result); err != nil {
			return nil, err
		}
		out.Submissions[i] = Submission{Id: sub.ID, Language: CodeLanguage(sub.Language), Code: sub.Code,
			Verdict: Verdict(sub.Verdict), Passed: sub.Passed, Total: sub.Total, Seconds: sub.Seconds, Xp: sub.XP,
			SubmittedAt: sub.SubmittedAt, Result: result}
	}
	return out, nil
}

// GetSolution is a challenge's reference solution, once the learner solved it.
func (s *server) GetSolution(ctx context.Context, req GetSolutionRequestObject) (GetSolutionResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	files, err := s.deps.Challenges.Solution(ctx, user, req.Course, req.Item)
	if errors.Is(err, catalog.ErrNotFound) {
		return GetSolution404ApplicationProblemPlusJSONResponse{itemNotFound(req.Course, req.Item)}, nil
	}
	if p := challengeProblem(err); p.Status == http.StatusConflict {
		return GetSolution409ApplicationProblemPlusJSONResponse(p), nil
	}
	if err != nil {
		return nil, err
	}
	return GetSolution200JSONResponse{Files: files}, nil
}

// convert copies a module's document into its transport type through JSON (same field names).
func convert(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}
