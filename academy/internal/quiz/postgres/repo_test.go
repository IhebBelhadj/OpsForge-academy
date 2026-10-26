package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"

	identitypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/identity/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/quiz"
	quizpg "github.com/IhebBelhadj/opsforge-academy/academy/internal/quiz/postgres"
)

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

func TestAttemptsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := postgres.Open(ctx, pgtest.URL(t), platform.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u, _ := identitypg.NewRepo(store.DB).EnsureUser(ctx, "learner", t0)
	r := quizpg.NewRepo(store.DB)

	deadline := t0.Add(time.Minute)
	see := "what-is-a-pod"
	a := quiz.Attempt{UserID: u.ID, Course: "c", Item: "q", Hash: "h", StartedAt: t0, Deadline: &deadline, Pass: 70,
		Offered: 20, Questions: []quiz.Drawn{{ID: "x", Type: quiz.Single, Prompt: "?", Answer: []int{1}, See: &see,
			Choices: []quiz.Choice{{Index: 1, Text: "b"}, {Index: 0, Text: "a"}}}}}
	id, err := r.Create(ctx, a)
	if err != nil || id != 1 {
		t.Fatalf("%d %v", id, err)
	}
	got, err := r.Attempt(ctx, id)
	if err != nil || got.SubmittedAt != nil || !got.Deadline.Equal(deadline) || got.Pass != 70 || got.Offered != 20 ||
		got.Questions[0].Choices[0].Index != 1 || *got.Questions[0].See != see || got.Answers != nil {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := got.Grade(map[string][]int{"x": {1}}, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got.XP = 20
	if err := r.Submit(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := r.Submit(ctx, got); !errors.Is(err, quiz.ErrSubmitted) {
		t.Fatalf("twice: %v", err)
	}
	done, _ := r.Attempt(ctx, id)
	if !done.Passed || done.Score != 100 || done.XP != 20 || done.Answers["x"][0] != 1 ||
		!done.SubmittedAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("%+v", done)
	}
	if _, err := r.Create(ctx, quiz.Attempt{UserID: u.ID, Course: "c", Item: "q", StartedAt: t0}); err != nil {
		t.Fatal(err)
	}
	list, err := r.Attempts(ctx, u.ID, "c", "q")
	if err != nil || len(list) != 2 || list[0].ID != 2 {
		t.Fatalf("%+v %v", list, err)
	}
	passed, err := r.Passed(ctx, u.ID)
	if err != nil || len(passed) != 1 || passed[0].ID != 1 {
		t.Fatalf("%+v %v", passed, err)
	}
	if _, err := r.Attempt(ctx, 99); !errors.Is(err, quiz.ErrAttemptNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}
