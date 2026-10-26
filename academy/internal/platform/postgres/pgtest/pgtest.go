// Package pgtest gives each test its own PostgreSQL database on a shared server (`make db`, or
// ACADEMY_TEST_DATABASE_URL; CI runs a service container): cloned from a template database migrated once per set of
// migrations (CREATE DATABASE ... TEMPLATE), so a test starts on a migrated, empty database in milliseconds, and
// dropped when the test ends.
package pgtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io/fs"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/migrations"
)

// server is the test server's URL (its database part is replaced).
func server() string {
	if u := os.Getenv("ACADEMY_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return postgres.DefaultURL
}

// at is the server's URL naming database name.
func at(name string) string {
	u, err := url.Parse(server())
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// template is the migrated template's name, one per set of migrations (so a changed migration gets a new one).
var template = sync.OnceValues(func() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := migrations.FS.ReadFile(path)
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", path, data)
		return err
	})
	if err != nil {
		return "", err
	}
	name := "academy_template_" + hex.EncodeToString(h.Sum(nil))[:12]
	ctx := context.Background()
	admin, err := sql.Open("pgx", at("postgres"))
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close() }()
	conn, err := admin.Conn(ctx) // the advisory lock is the connection's: test processes run in parallel
	if err != nil {
		return "", fmt.Errorf("no test database server at %s (make db, or set ACADEMY_TEST_DATABASE_URL): %w",
			postgres.Redact(server()), err)
	}
	defer func() { _ = conn.Close() }()
	key := fnv.New64a()
	_, _ = key.Write([]byte(name))
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", int64(key.Sum64())); err != nil {
		return "", err
	}
	defer func() { _, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", int64(key.Sum64())) }()
	var exists bool
	if err := conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
		name).Scan(&exists); err != nil || exists {
		return name, err
	}
	store, err := postgres.Open(ctx, at(name), fixed{}) // creates and migrates it
	if err != nil {
		return "", err
	}
	return name, store.Close()
})

// fixed is the template's clock: its instance row is the clones' (2026-01-01).
type fixed struct{}

func (fixed) Now() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// URL is a new migrated database of its own for t (dropped when t ends).
func URL(t testing.TB) string {
	t.Helper()
	tmpl, err := template()
	if err != nil {
		t.Fatal(err)
	}
	return create(t, "TEMPLATE "+postgres.QuoteIdent(tmpl))
}

// Empty is a new database without any table for t (migrations from scratch), dropped when t ends.
func Empty(t testing.TB) string {
	t.Helper()
	return create(t, "")
}

// Missing is the URL of a database that does not exist yet (Open creates it), dropped when t ends if it does then.
func Missing(t testing.TB) string {
	t.Helper()
	name, admin := newName(t)
	t.Cleanup(func() { drop(t, admin, name, "IF EXISTS ") })
	return at(name)
}

func create(t testing.TB, from string) string {
	t.Helper()
	name, admin := newName(t)
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+postgres.QuoteIdent(name)+" "+from); err != nil {
		_ = admin.Close()
		t.Fatalf("test database (make db, or set ACADEMY_TEST_DATABASE_URL): %v", err)
	}
	t.Cleanup(func() { drop(t, admin, name, "") })
	return at(name)
}

// newName is a database name of t's own and a connection to the server's maintenance database.
func newName(t testing.TB) (string, *sql.DB) {
	t.Helper()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	admin, err := sql.Open("pgx", at("postgres"))
	if err != nil {
		t.Fatal(err)
	}
	return "academy_test_" + hex.EncodeToString(b), admin
}

func drop(t testing.TB, admin *sql.DB, name, ifExists string) {
	defer func() { _ = admin.Close() }()
	q := "DROP DATABASE " + ifExists + postgres.QuoteIdent(name) + " WITH (FORCE)" //nolint:gosec // a quoted identifier
	if _, err := admin.ExecContext(context.Background(), q); err != nil {
		t.Errorf("drop %s: %v", name, err)
	}
}
