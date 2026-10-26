package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"

	identitypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/identity/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/operations"
	operationspg "github.com/IhebBelhadj/opsforge-academy/academy/internal/operations/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
)

var t0 = time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)

func TestOperationsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := postgres.Open(ctx, pgtest.URL(t), platform.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u, _ := identitypg.NewRepo(store.DB).EnsureUser(ctx, "learner", t0)
	r := operationspg.NewRepo(store.DB)
	for _, id := range []string{"a", "b"} {
		if err := r.Create(ctx, operations.Operation{ID: id, UserID: u.ID, Kind: "lab.start", Subject: "meet",
			Status: operations.Running, CreatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Get(ctx, "a")
	if err != nil || got.Status != operations.Running || got.Result != nil || got.Error != nil || got.FinishedAt != nil ||
		!got.CreatedAt.Equal(t0) || got.UserID != u.ID {
		t.Fatalf("%+v %v", got, err)
	}
	end := t0.Add(time.Second)
	got.Status, got.Result, got.FinishedAt = operations.Done, json.RawMessage(`{"ok":1}`), &end
	if err := r.Finish(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get(ctx, "a"); got.Status != operations.Done || !catalog.SameJSON(got.Result, json.RawMessage(`{"ok":1}`)) ||
		!got.FinishedAt.Equal(end) {
		t.Fatalf("%+v", got)
	}
	if n, err := r.FailRunning(ctx, "gone", end); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if b, _ := r.Get(ctx, "b"); b.Status != operations.Failed || *b.Error != "gone" {
		t.Fatalf("%+v", b)
	}
	if _, err := r.Get(ctx, "nope"); !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}
