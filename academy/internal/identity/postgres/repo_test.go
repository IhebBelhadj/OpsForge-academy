package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/identity"
	identitypg "github.com/IhebBelhadj/opsforge-academy/academy/internal/identity/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
)

type clock struct{ now time.Time }

func (c clock) Now() time.Time { return c.now }

var now = time.Date(2026, 10, 24, 12, 0, 0, 123_000, time.UTC) // timestamptz keeps microseconds

func repo(t *testing.T) *identitypg.Repo {
	t.Helper()
	store, err := postgres.Open(context.Background(), pgtest.URL(t), clock{now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return identitypg.NewRepo(store.DB)
}

func TestEnsureUserCreatesOnce(t *testing.T) {
	r := repo(t)
	ctx := context.Background()
	a, err := r.EnsureUser(ctx, "learner", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.EnsureUser(ctx, "learner", now.Add(time.Hour))
	if err != nil || b != a || !a.CreatedAt.Equal(now) {
		t.Fatalf("a %+v, b %+v, %v", a, b, err)
	}
	if got, err := r.User(ctx, a.ID); err != nil || got != a {
		t.Fatalf("by id: %+v, %v", got, err)
	}
}

func TestSessionsRoundTripAndExpire(t *testing.T) {
	r := repo(t)
	ctx := context.Background()
	u, _ := r.EnsureUser(ctx, "learner", now)
	s := identity.Session{TokenHash: "h1", UserID: u.ID, CSRF: "c1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := r.CreateSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Session(ctx, "h1"); err != nil || got != s {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := r.Session(ctx, "nope"); !errors.Is(err, identity.ErrNoSession) {
		t.Fatalf("unknown: %v", err)
	}
	if err := r.CreateSession(ctx, identity.Session{TokenHash: "h2", UserID: 99, CSRF: "c", CreatedAt: now,
		ExpiresAt: now}); err == nil {
		t.Fatal("a session for no user was stored (foreign keys off?)")
	}

	if err := r.DeleteExpired(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Session(ctx, "h1"); err != nil {
		t.Fatal("deleted at the very instant it expires")
	}
	if err := r.DeleteExpired(ctx, now.Add(time.Hour+time.Microsecond)); err != nil { // timestamptz's resolution
		t.Fatal(err)
	}
	if _, err := r.Session(ctx, "h1"); !errors.Is(err, identity.ErrNoSession) {
		t.Fatalf("expired session kept: %v", err)
	}
}
