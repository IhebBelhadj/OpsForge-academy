package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/config"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/legacy"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi/opsforgefake"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform/postgres/pgtest"
)

// testConfig is the wired application's configuration over a fake OpsForge (its token copied into the data folder),
// a database of its own and an empty content folder.
func testConfig(t *testing.T, dir string, args ...string) config.Config {
	t.Helper()
	fake := httptest.NewServer(opsforgefake.New("token"))
	t.Cleanup(fake.Close)
	if err := os.WriteFile(filepath.Join(dir, "opsforge-token"), []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(append([]string{"--addr", "127.0.0.1:0", "--data", dir, "--content",
		filepath.Join(dir, "content"), "--opsforge-url", fake.URL, "--database", pgtest.URL(t), "--log-level",
		"error"}, args...),
		func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The wired application end to end: a real PostgreSQL database, a fake OpsForge, a real listener.
func TestServeAnswersHealthAndReadinessThenShutsDownCleanly(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	app, cleanup, err := initApp(ctx, cfg, platform.Build{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	app.ready = make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	base := "http://" + <-app.ready

	for path, want := range map[string]string{"/healthz": "ok", "/readyz": "ready"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Status string `json:"status"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || body.Status != want {
			t.Fatalf("%s: %d %+v", path, resp.StatusCode, body)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no shutdown within 5 s")
	}
	cleanup()
	store, err := postgres.Open(context.Background(), cfg.Database, platform.SystemClock{})
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.Queries.GetInstance(context.Background()); err != nil {
		t.Fatalf("the database has no instance: %v", err)
	}
}

func TestVersionCommand(t *testing.T) {
	if code := run([]string{"version"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestBadFlagsExitWithUsageError(t *testing.T) {
	if code := run([]string{"serve", "--addr", "0.0.0.0:1"}); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if code := run([]string{"migrate-legacy", "--data", t.TempDir()}); code != 2 {
		t.Fatalf("migrate-legacy without --legacy-db: exit %d", code)
	}
}

// Without OpsForge's token Academy still starts and is ready (only lab work needs OpsForge), and readyz says what
// is missing.
func TestAMissingTokenIsAReadinessProblem(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t, dir)
	cfg.OpsForgeTokenFile = filepath.Join(dir, "missing")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, cleanup, err := initApp(ctx, cfg, platform.Build{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	app.ready = make(chan string, 1)
	go func() { _ = app.Run(ctx) }()
	resp, err := http.Get("http://" + <-app.ready + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "opsforge-token-file") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}

// Academy serves its courses while OpsForge is out of reach (steps unchecked), and checks the steps once it answers.
func TestTheCoursesAreServedWithoutOpsForge(t *testing.T) {
	recheckEvery = 20 * time.Millisecond
	t.Cleanup(func() { recheckEvery = time.Minute })
	var down atomic.Bool
	var checked atomic.Int32
	down.Store(true)
	fake := opsforgefake.New("token")
	opsforge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() { // out of reach: the connection drops
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		if r.URL.Path == "/v1/steps/validate" {
			checked.Add(1)
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(opsforge.Close)
	dir := t.TempDir()
	cfg := testConfig(t, dir, "--opsforge-url", opsforge.URL)
	cfg.Content = "../../testdata/packs"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app, cleanup, err := initApp(ctx, cfg, platform.Build{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	app.ready = make(chan string, 1)
	go func() { _ = app.Run(ctx) }()
	base := "http://" + <-app.ready

	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Status string `json:"status"`
		Checks []struct {
			Name     string `json:"name"`
			Ok       bool   `json:"ok"`
			Required bool   `json:"required"`
		} `json:"checks"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ready)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || ready.Status != "ready" || len(ready.Checks) != 2 ||
		ready.Checks[1].Name != "opsforge" || ready.Checks[1].Ok || ready.Checks[1].Required {
		t.Fatalf("readyz: %d %+v", resp.StatusCode, ready)
	}
	jar, _ := cookiejar.New(nil)
	learner := &http.Client{Jar: jar}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/sessions", nil)
	req.Header.Set("Origin", base)
	resp, err = learner.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("session: %d", resp.StatusCode)
	}
	resp, err = learner.Get(base + "/api/v1/courses")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Courses []json.RawMessage `json:"courses"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(list.Courses) == 0 {
		t.Fatalf("no courses without OpsForge: %d %+v", resp.StatusCode, list)
	}

	down.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for checked.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the steps were never checked once OpsForge answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(10 * recheckEvery)
	if n := checked.Load(); n != 1 {
		t.Fatalf("the steps were checked %d times; the recheck should stop after the first", n)
	}
}

// migrate-legacy through the wired graph: the Phase 10 fixture, then the same file again.
func TestMigrateLegacyImportsOnce(t *testing.T) {
	dir := t.TempDir()
	script, err := os.ReadFile("../../testdata/labs.phase10.sql")
	if err != nil {
		t.Fatal(err)
	}
	legacyDB := filepath.Join(dir, "labs.db")
	db, err := sql.Open("sqlite", "file:"+legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	cfg := testConfig(t, dir, "--legacy-db", legacyDB)
	ctx := context.Background()
	app, cleanup, err := initApp(ctx, cfg, platform.Build{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if r, err := app.MigrateLegacy(ctx); err != nil || r.Attempts != 2 || r.Events != 7 || r.Awards != 1 {
		t.Fatalf("first run: %+v %v", r, err)
	}
	if r, err := app.MigrateLegacy(ctx); err != nil || r != (legacy.Report{}) {
		t.Fatalf("second run: %+v %v", r, err)
	}
}
