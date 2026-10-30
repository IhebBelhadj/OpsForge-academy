package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge"
	challengepg "github.com/IhebBelhadj/opsforge-academy/academy/internal/challenge/postgres"
	identitypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/identity/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
)

var t0 = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)

func TestSubmissionsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := postgres.Open(ctx, pgtest.URL(t), platform.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u, _ := identitypg.NewRepo(store.DB).EnsureUser(ctx, "learner", t0)
	r := challengepg.NewRepo(store.DB)

	wrong := challenge.Submission{UserID: u.ID, Course: "dsa", Item: "two-sum", Hash: "h", Language: "python",
		Code: "def two_sum(n, t): return []", Verdict: challenge.WrongAnswer, Passed: 1, Total: 5, Seconds: 0.5,
		SubmittedAt: t0, Result: challenge.Result{Verdict: challenge.WrongAnswer, Passed: 1, Total: 5,
			Cases: []challenge.CaseResult{{N: 2, Want: []byte("[0,1]"), Got: []byte("[]")}}, Output: "hi"}}
	if id, err := r.Create(ctx, wrong); err != nil || id != 1 {
		t.Fatalf("%d %v", id, err)
	}
	ok := wrong
	ok.Verdict, ok.Passed, ok.XP, ok.SubmittedAt = challenge.Accepted, 5, 50, t0.Add(time.Minute)
	ok.Result = challenge.Result{Verdict: challenge.Accepted, Passed: 5, Total: 5, Cases: []challenge.CaseResult{}}
	if _, err := r.Create(ctx, ok); err != nil {
		t.Fatal(err)
	}
	subs, err := r.Submissions(ctx, u.ID, "dsa", "two-sum")
	if err != nil || len(subs) != 2 || subs[0].Verdict != challenge.Accepted || subs[1].Result.Output != "hi" ||
		!challenge.Same(subs[1].Result.Cases[0].Want, []byte("[0,1]"), "exact") || subs[1].Code != wrong.Code || !subs[1].SubmittedAt.Equal(t0) {
		t.Fatalf("%+v %v", subs, err)
	}
	accepted, err := r.Accepted(ctx, u.ID)
	if err != nil || len(accepted) != 1 || accepted[0].XP != 50 {
		t.Fatalf("%+v %v", accepted, err)
	}
	if other, _ := r.Submissions(ctx, u.ID, "dsa", "other"); len(other) != 0 {
		t.Fatalf("%+v", other)
	}
}
