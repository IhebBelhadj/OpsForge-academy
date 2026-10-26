package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"
)

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

var t0 = fixedClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))

func open(t *testing.T, url string, clock fixedClock) *postgres.Store {
	t.Helper()
	store, err := postgres.Open(context.Background(), url, clock)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

// A database the server does not have yet is created (as the SQLite file was), migrated from scratch, and records
// its instance once.
func TestOpenCreatesMigratesAndRecordsTheInstanceOnce(t *testing.T) {
	ctx := context.Background()
	url := pgtest.Missing(t)
	store := open(t, url, t0)
	created, err := store.Queries.GetInstance(ctx)
	if err != nil || !created.Equal(time.Time(t0)) {
		t.Fatalf("instance created_at = %v, %v", created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	again := open(t, url, fixedClock(time.Time(t0).Add(time.Hour)))
	defer func() { _ = again.Close() }()
	if created, _ := again.Queries.GetInstance(ctx); !created.Equal(time.Time(t0)) {
		t.Fatalf("reopening rewrote created_at: %v", created)
	}
	if err := again.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}

// Times come back in UTC (not the host's zone), at the microsecond timestamptz keeps.
func TestTimesComeBackInUTC(t *testing.T) {
	store := open(t, pgtest.URL(t), t0)
	defer func() { _ = store.Close() }()
	cet := time.Date(2026, 10, 24, 10, 0, 0, 123456789, time.FixedZone("CET", 3600))
	var got time.Time
	if err := store.DB.QueryRow("SELECT $1::timestamptz", cet).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got.Location() != time.UTC || !got.Equal(postgres.Time(cet)) || got.Nanosecond() != 123456000 {
		t.Fatalf("%v (%v)", got, got.Location())
	}
}

func TestReadyFailsOnAClosedStore(t *testing.T) {
	store := open(t, pgtest.URL(t), t0)
	_ = store.Close()
	if err := store.Ready(context.Background()); err == nil {
		t.Fatal("Ready on a closed store: want an error")
	}
}

func TestOpenFailsWithoutAServer(t *testing.T) {
	_, err := postgres.Open(context.Background(), "postgres://academy:secret@127.0.0.1:1/academy", t0)
	if err == nil {
		t.Fatal("want an error")
	}
	if got := err.Error(); strings.Contains(got, "secret") {
		t.Fatalf("the error shows the password: %s", got)
	}
}

func TestUniqueViolations(t *testing.T) {
	store := open(t, pgtest.URL(t), t0)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	_, err := store.DB.ExecContext(ctx, "INSERT INTO instance (id, created_at) VALUES (1, now())")
	if !postgres.IsUniqueViolation(err) {
		t.Fatalf("a second instance row: %v", err)
	}
	if postgres.IsUniqueViolation(nil) {
		t.Fatal("nil")
	}
}
