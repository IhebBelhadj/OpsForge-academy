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
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace"
	workspacepg "github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace/postgres"
)

func TestOwners(t *testing.T) {
	ctx := context.Background()
	store, err := postgres.Open(ctx, pgtest.URL(t), platform.SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u, _ := identitypg.NewRepo(store.DB).EnsureUser(ctx, "learner", time.Now())
	r := workspacepg.NewRepo(store.DB)
	if err := r.Create(ctx, "lab-1", u.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, "lab-1", u.ID, time.Now()); err == nil {
		t.Fatal("created twice")
	}
	if owner, err := r.Owner(ctx, "lab-1"); err != nil || owner != u.ID {
		t.Fatalf("%d %v", owner, err)
	}
	if _, err := r.Owner(ctx, "lab-2"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}
