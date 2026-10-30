package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/httpapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi/opsforgefake"
)

const (
	runsOp        = "/api/v1/courses/{course}/items/{item}/runs"
	submissionsOp = "/api/v1/courses/{course}/items/{item}/submissions"
	solutionOp    = "/api/v1/courses/{course}/items/{item}/solution"
	sumItem       = "/api/v1/courses/k8s-intro/items/sum"
)

// judged posts code to runs or submissions and reads the finished operation's outcome.
func judged(t *testing.T, w *world, b *browser, what, op, body string) httpapi.ChallengeOutcome {
	t.Helper()
	rec := post(t, b, sumItem+"/"+what, op, body)
	if rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	w.wait(t)
	done := get(t, b, "/api/v1/operations/"+decode[httpapi.Operation](t, rec.Body.Bytes()).Id, opOp)
	got := decode[httpapi.Operation](t, done.Body.Bytes())
	if got.Status != "done" || got.Result == nil {
		t.Fatalf("%s", done.Body)
	}
	out, err := got.Result.AsChallengeOutcome()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestACodingChallengeIsShownWithItsExamplesOnly(t *testing.T) {
	w := newCodeWorld(t)
	b := signIn(t, apiServer(t, w.deps))
	rec := get(t, b, sumItem, "/api/v1/courses/{course}/items/{item}")
	c := decode[httpapi.Item](t, rec.Body.Bytes()).Challenge
	if rec.Code != 200 || c == nil || c.Code == nil || c.Code.Function != "add" || c.Code.Cases != 2 ||
		len(c.Code.Examples) != 1 || len(c.Code.Languages) != 2 || c.Starter["solution.py"] == "" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRunSubmitAndTheSolution(t *testing.T) {
	w := newCodeWorld(t)
	b := signIn(t, apiServer(t, w.deps))
	run := judged(t, w, b, "runs", runsOp, `{"language":"python","code":"def add(a, b):\n    return a + b\n"}`)
	if run.Verdict != "accepted" || run.Total != 1 || len(run.Cases) != 1 || run.Submission != nil ||
		run.Output != "print from the program" {
		t.Fatalf("%+v %s", run, deref(run.Error))
	}
	rec := get(t, b, sumItem+"/solution", solutionOp)
	if rec.Code != 409 || decode[httpapi.Problem](t, rec.Body.Bytes()).Type != "/problems/solution-locked" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	wrong := judged(t, w, b, "submissions", submissionsOp, `{"language":"python","code":"wrong"}`)
	if wrong.Verdict != "wrong_answer" || wrong.Total != 2 || len(wrong.Cases) != 1 || wrong.Submission == nil {
		t.Fatalf("%+v", wrong)
	}
	right := judged(t, w, b, "submissions", submissionsOp, `{"language":"python","code":"def add(a, b): return a+b"}`)
	if right.Verdict != "accepted" || right.Xp != 30 || len(right.Cases) != 0 {
		t.Fatalf("%+v", right)
	}
	rec = get(t, b, sumItem+"/submissions", submissionsOp)
	list := decode[httpapi.SubmissionList](t, rec.Body.Bytes())
	if rec.Code != 200 || len(list.Submissions) != 2 || list.Submissions[0].Verdict != "accepted" ||
		list.Submissions[1].Code != "wrong" || list.Submissions[1].Result.Cases[0].N != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = get(t, b, sumItem+"/solution", solutionOp)
	if sol := decode[httpapi.Solution](t, rec.Body.Bytes()); rec.Code != 200 || sol.Files["go"] == "" ||
		sol.Files["python"] != "def add(a, b):\n    return a + b\n" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = get(t, b, "/api/v1/courses/k8s-intro/progress", "/api/v1/courses/{course}/progress")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	failed := judged(t, w, b, "submissions", submissionsOp, `{"language":"python","code":"crash"}`)
	if failed.Verdict != "error" || failed.Submission != nil || failed.Error == nil {
		t.Fatalf("%+v", failed)
	}
}

func TestCodeRefusals(t *testing.T) {
	w := newCodeWorld(t)
	b := signIn(t, apiServer(t, w.deps))
	py := `{"language":"python","code":"x"}`
	for _, c := range []struct {
		path, op, body string
		status         int
		problem        string
		setup          func()
	}{
		{"/api/v1/courses/k8s-intro/items/nope/runs", runsOp, py, 404, "/problems/not-found", nil},
		{"/api/v1/courses/k8s-intro/items/first-pod/runs", runsOp, py, 409, "/problems/not-a-code-challenge", nil},
		{sumItem + "/runs", runsOp, `{"language":"javascript","code":"x"}`, 422, "/problems/bad-language", nil},
		{sumItem + "/runs", runsOp, py, 409, "/problems/lab-busy", func() {
			w.opsforge.Refuse = func(json.RawMessage) *opsforgefake.Refusal {
				return &opsforgefake.Refusal{Slug: "lab-busy", Detail: "a lab runs alone"}
			}
		}},
		{sumItem + "/runs", runsOp, py, 503, "/problems/labs-offline", func() {
			w.opsforge.Refuse = nil
			w.opsforge.Offline.Store(true)
		}},
		{sumItem + "/submissions", submissionsOp, py, 503, "/problems/labs-offline", nil},
		{sumItem + "/submissions", submissionsOp, py, 409, "/problems/lab-unavailable", func() {
			w.opsforge.Offline.Store(false)
			w.opsforge.Refuse = func(json.RawMessage) *opsforgefake.Refusal {
				return &opsforgefake.Refusal{Slug: "unmet-requirements", Unmet: []string{"runtime python"}}
			}
		}},
		{"/api/v1/courses/k8s-intro/items/nope/submissions", submissionsOp, py, 404, "/problems/not-found", nil},
		{sumItem + "/submissions", submissionsOp, `{"language":"go","code":""}`, 202,
			"", func() { w.opsforge.Refuse = nil }},
		{sumItem + "/submissions", submissionsOp, `{"language":"ruby","code":"x"}`, 422, "/problems/bad-language", nil},
	} {
		if c.setup != nil {
			c.setup()
		}
		rec := post(t, b, c.path, c.op, c.body)
		if rec.Code != c.status || (c.problem != "" && decode[httpapi.Problem](t, rec.Body.Bytes()).Type != c.problem) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
		w.wait(t)
	}
	if rec := post(t, b, "/api/v1/courses/k8s-intro/items/first-pod/submissions", submissionsOp, py); rec.Code != 409 {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	for _, op := range []string{submissionsOp, solutionOp} {
		suffix := op[len("/api/v1/courses/{course}/items/{item}"):]
		if rec := get(t, b, "/api/v1/courses/k8s-intro/items/nope"+suffix, op); rec.Code != 404 {
			t.Errorf("%s: %d %s", op, rec.Code, rec.Body)
		}
		if rec := get(t, b, "/api/v1/courses/k8s-intro/items/first-pod"+suffix, op); rec.Code != 409 {
			t.Errorf("%s: %d %s", op, rec.Code, rec.Body)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
