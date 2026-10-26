package opsforgeapi_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi/opsforgefake"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/terminal"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace"
)

func client(t *testing.T, url, token string) *opsforgeapi.Client {
	t.Helper()
	c, err := opsforgeapi.New(url, opsforgeapi.StaticToken(token), 5*time.Second, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Wait = time.Second
	return c
}

func fake(t *testing.T) (*opsforgefake.Fake, *opsforgeapi.Client) {
	t.Helper()
	f := opsforgefake.New("token")
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, client(t, srv.URL, "token")
}

// golden is one of the pinned contract's golden documents (academy/contract/golden/<name>).
func golden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contract", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// serve answers each "METHOD /path" with a status and a body (problem bodies get their media type).
func serve(t *testing.T, routes map[string]func() (int, string)) *opsforgeapi.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		status, body := answer()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body, `"/problems/`) && status >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return client(t, srv.URL, "token")
}

func TestNewRefusesWhatIsNotAnHTTPURL(t *testing.T) {
	for _, u := range []string{"127.0.0.1:8091", "ftp://x", "http://"} {
		if _, err := opsforgeapi.New(u, opsforgeapi.StaticToken("t"), time.Second, time.Second); err == nil {
			t.Errorf("%s: no error", u)
		}
	}
}

func TestReadiness(t *testing.T) {
	f, c := fake(t)
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("ready: %v", err)
	}
	f.Minor = 7 // newer is fine
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("newer minor: %v", err)
	}
	// Below MinMinor (0 today: only reachable once Academy uses an addition) readyz names the release it needs
	old := &opsforgeapi.TooOldError{Got: opsforgeapi.Version{Contract: 1, Minor: 0, OpsForge: "1.0.0"}}
	if !strings.Contains(old.Error(), "academy needs 1.0 (opsforge 1.0.0 or later)") {
		t.Fatalf("too old: %v", old)
	}
	wrong := client(t, c.URL(), "nope")
	var p *opsforgeapi.Problem
	if err := wrong.Ready(context.Background()); !errors.As(err, &p) || p.Status != 401 || p.Slug() != "no-token" {
		t.Fatalf("wrong token: %v", err)
	}
	missing, _ := opsforgeapi.New(c.URL(), opsforgeapi.TokenFile(filepath.Join(t.TempDir(), "token")), time.Second,
		time.Second)
	if err := missing.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "opsforge-token-file") {
		t.Fatalf("missing token file: %v", err)
	}
}

// An OpsForge on another machine serves a self-signed certificate: refused until Academy trusts it (--opsforge-ca-file).
func TestSelfSignedOpsForge(t *testing.T) {
	srv := httptest.NewTLSServer(opsforgefake.New("token"))
	t.Cleanup(srv.Close)
	c := client(t, srv.URL, "token")
	if err := c.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("untrusted certificate: %v", err)
	}
	ca := filepath.Join(t.TempDir(), "lab.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, cert, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.TrustCA(ca); err != nil {
		t.Fatal(err)
	}
	if err := c.Ready(context.Background()); err != nil {
		t.Fatalf("trusted: %v", err)
	}
	if err := c.TrustCA(filepath.Join(t.TempDir(), "none.pem")); err == nil {
		t.Fatal("missing ca: no error")
	}
}

func TestAnotherMajorIsRefused(t *testing.T) {
	c := serve(t, map[string]func() (int, string){
		"GET /v1/version": func() (int, string) { return 200, `{"contract": 2, "minor": 0, "opsforge": "2.0.0"}` },
		"GET /v1/state": func() (int, string) {
			return 400, `{"contract": 2, "type": "/problems/x", "title": "x", "detail": "x"}`
		},
	})
	var ce *opsforgeapi.ContractError
	if _, err := c.Version(context.Background()); !errors.As(err, &ce) || ce.Got != 2 {
		t.Fatalf("version: %v", err)
	}
	if _, err := c.State(context.Background()); !errors.As(err, &ce) {
		t.Fatalf("problem of another major: %v", err)
	}
}

// The golden documents of the pinned contract decode into the platform's types.
func TestGoldenDocuments(t *testing.T) {
	jobOf := func(output string) func() (int, string) {
		return func() (int, string) {
			var job map[string]any
			_ = json.Unmarshal(golden(t, "job.json"), &job)
			var out any
			_ = json.Unmarshal(golden(t, output), &out)
			job["output"] = out
			data, _ := json.Marshal(job)
			return 200, string(data)
		}
	}
	running := func() (int, string) {
		return 202, `{"contract": 1, "id": "0123456789abcdef", "kind": "create", "sandbox": "0123abcd",
			"status": "running", "log": [], "output": null, "problem": null}`
	}
	routes := map[string]func() (int, string){
		"GET /v1/version":               func() (int, string) { return 200, string(golden(t, "version.json")) },
		"GET /v1/state":                 func() (int, string) { return 200, string(golden(t, "state.up.json")) },
		"POST /v1/steps/validate":       func() (int, string) { return 200, string(golden(t, "validate-steps.json")) },
		"POST /v1/sandboxes":            running,
		"GET /v1/jobs/0123456789abcdef": jobOf("sandbox.json"),
	}
	c := serve(t, routes)
	ctx := context.Background()
	if v, err := c.Version(ctx); err != nil || v.Contract != 1 {
		t.Fatalf("version %+v %v", v, err)
	}
	if s, err := c.State(ctx); err != nil || !s.Up || len(s.Placeholders) == 0 || s.Links["a-c"].Condition != "cut" {
		t.Fatalf("state %+v %v", s, err)
	}
	if errs, err := c.CheckSteps(ctx, []lab.StepSet{{}}); err != nil || len(errs) != 5 || errs[0].Set != 1 {
		t.Fatalf("steps %+v %v", errs, err)
	}
	sandboxes := c.Sandboxes()
	pending, err := sandboxes.Create(ctx, lab.SandboxRequest{Kind: "lab", Label: "x"})
	if err != nil || pending.Sandbox != "0123abcd" || pending.Job != "0123456789abcdef" {
		t.Fatalf("create %+v %v", pending, err)
	}
	sb, err := sandboxes.Await(ctx, pending)
	if err != nil || sb.Status != lab.SandboxReady || *sb.Namespace != "sb-0123abcd" || sb.Placeholders["${ns}"] == "" ||
		sb.Setup == nil || sb.Setup.Error != "" || len(sb.Setup.Log) == 0 || sb.Teardown != nil {
		t.Fatalf("sandbox %+v %v", sb, err)
	}
	for _, name := range []string{"probe.passed.json", "probe.failed.json"} {
		routes["POST /v1/sandboxes/0123abcd/probes"] = running
		routes["GET /v1/jobs/0123456789abcdef"] = jobOf(name)
		p, err := serve(t, routes).Sandboxes().Probe(ctx, "0123abcd", lab.ProbeRequest{})
		if err != nil || len(p.Results) == 0 || p.Passed != (name == "probe.passed.json") {
			t.Fatalf("%s: %+v %v", name, p, err)
		}
	}
}

// Outputs are open: a newer OpsForge of the same major may add fields.
func TestOutputsMayGainFields(t *testing.T) {
	c := serve(t, map[string]func() (int, string){
		"GET /v1/version": func() (int, string) {
			return 200, `{"contract": 1, "minor": 3, "opsforge": "1.3.0", "features": ["new"]}`
		},
	})
	if v, err := c.Version(context.Background()); err != nil || v.Minor != 3 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestInvalidDocumentsAreRefused(t *testing.T) {
	c := serve(t, map[string]func() (int, string){
		"GET /v1/version": func() (int, string) { return 200, `{"contract": 1, "minor": "zero"}` },
	})
	if _, err := c.Version(context.Background()); !errors.Is(err, opsforgeapi.ErrInvalidDocument) {
		t.Fatalf("err = %v", err)
	}
}

func TestSandboxes(t *testing.T) {
	f, c := fake(t)
	ctx := context.Background()
	sandboxes := c.Sandboxes()
	req := lab.SandboxRequest{Kind: "lab", Label: "lab tour (user 1)", Namespace: true,
		Files: map[string]string{"web.yaml": "kind: Pod\n"}, Setup: []json.RawMessage{json.RawMessage(`{"sleep":"1s"}`)}}
	f.Setup = func(in json.RawMessage) (string, error) {
		if !strings.Contains(string(in), `"contract":1`) || !strings.Contains(string(in), `"namespace":true`) {
			t.Errorf("sandbox input %s", in)
		}
		return "", nil
	}
	pending, err := sandboxes.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := sandboxes.Await(ctx, pending)
	if err != nil || sb.ID != pending.Sandbox || sb.Status != lab.SandboxReady || *sb.Namespace != "sb-"+sb.ID {
		t.Fatalf("ready %+v %v", sb, err)
	}
	if got, err := c.Files().Read(ctx, sb.ID, "web.yaml"); err != nil || string(got) != "kind: Pod\n" {
		t.Fatalf("its files %q %v", got, err)
	}
	if p, err := sandboxes.Probe(ctx, sb.ID, lab.ProbeRequest{}); err != nil || !p.Passed {
		t.Fatalf("probe %+v %v", p, err)
	}
	if sb, err := sandboxes.Reset(ctx, sb.ID); err != nil || sb.Status != lab.SandboxReady {
		t.Fatalf("reset %+v %v", sb, err)
	}
	if gone, err := sandboxes.Delete(ctx, sb.ID); err != nil || gone.Status != lab.SandboxDeleted || gone.Teardown == nil {
		t.Fatalf("delete %+v %v", gone, err)
	}
	for name, err := range map[string]error{
		"delete": func() error { _, err := sandboxes.Delete(ctx, sb.ID); return err }(),
		"probe":  func() error { _, err := sandboxes.Probe(ctx, sb.ID, lab.ProbeRequest{}); return err }(),
	} {
		if !errors.Is(err, lab.ErrGone) {
			t.Errorf("%s once deleted: %v", name, err)
		}
	}

	// A failing setup: the sandbox fails (OpsForge cleaned it up); a setup that could not run: a failed job
	f.Setup = func(json.RawMessage) (string, error) { return "steps[0]: exit 1", nil }
	pending, _ = sandboxes.Create(ctx, req)
	if sb, err := sandboxes.Await(ctx, pending); err != nil || sb.Status != lab.SandboxFailed ||
		sb.Setup.Error != "steps[0]: exit 1" || sb.Teardown == nil {
		t.Fatalf("failed setup %+v %v", sb, err)
	}
	if _, err := sandboxes.Probe(ctx, pending.Sandbox, lab.ProbeRequest{}); err == nil {
		t.Fatal("a probe in a failed sandbox")
	}
	f.Setup = func(json.RawMessage) (string, error) { return "", errors.New("lab is down") }
	pending, _ = sandboxes.Create(ctx, req)
	var p *opsforgeapi.Problem
	if _, err := sandboxes.Await(ctx, pending); !errors.As(err, &p) || p.Slug() != "lab-error" || p.Detail != "lab is down" {
		t.Fatalf("failed job: %v", err)
	}
}

func TestSandboxRefusals(t *testing.T) {
	f, c := fake(t)
	ctx := context.Background()
	for slug, want := range map[string]error{"lab-down": lab.ErrLabDown, "lab-busy": lab.ErrBusy,
		"unmet-requirements": lab.ErrUnmet} {
		f.Refuse = func(json.RawMessage) *opsforgefake.Refusal {
			return &opsforgefake.Refusal{Slug: slug, Detail: "no", Unmet: []string{"ci (not in topology.yml)"}}
		}
		if _, err := c.Sandboxes().Create(ctx, lab.SandboxRequest{Kind: "lab", Label: "x"}); !errors.Is(err, want) {
			t.Errorf("%s: %v", slug, err)
		}
	}
	f.Refuse = func(json.RawMessage) *opsforgefake.Refusal {
		return &opsforgefake.Refusal{Slug: "unmet-requirements", Detail: "no", Unmet: []string{"ci (not in topology.yml)"}}
	}
	var unmet *lab.UnmetError
	if _, err := c.Sandboxes().Create(ctx, lab.SandboxRequest{Kind: "lab", Label: "x"}); !errors.As(err, &unmet) ||
		len(unmet.Unmet) != 1 || unmet.Unmet[0] != "ci (not in topology.yml)" {
		t.Fatalf("unmet: %v", err)
	}
	lost := serve(t, map[string]func() (int, string){
		"GET /v1/jobs/0123456789abcdef": func() (int, string) {
			return 404, `{"contract": 1, "type": "/problems/not-found", "title": "Not found", "detail": "no job"}`
		},
	})
	if _, err := lost.Sandboxes().Await(ctx, lab.Pending{Sandbox: "0123abcd", Job: "0123456789abcdef"}); !errors.Is(err,
		opsforgeapi.ErrJobLost) {
		t.Fatalf("lost: %v", err)
	}
}

func TestAJobOutlivesItsPolls(t *testing.T) {
	polls := 0
	c := serve(t, map[string]func() (int, string){
		"DELETE /v1/sandboxes/0123abcd": func() (int, string) {
			return 202, `{"contract": 1, "id": "0123456789abcdef", "kind": "delete", "sandbox": "0123abcd",
				"status": "running", "log": [], "output": null, "problem": null}`
		},
		"GET /v1/jobs/0123456789abcdef": func() (int, string) {
			polls++
			status, output := "running", "null"
			if polls == 3 {
				var doc map[string]any
				_ = json.Unmarshal(golden(t, "sandbox.json"), &doc)
				doc["status"] = "deleted"
				data, _ := json.Marshal(doc)
				status, output = "done", string(data)
			}
			return 200, `{"contract": 1, "id": "0123456789abcdef", "kind": "delete", "sandbox": "0123abcd", "status": "` +
				status + `", "log": [], "output": ` + output + `, "problem": null}`
		},
	})
	if sb, err := c.Sandboxes().Delete(context.Background(), "0123abcd"); err != nil || sb.Status != "deleted" ||
		polls != 3 {
		t.Fatalf("deleted %+v after %d polls: %v", sb, polls, err)
	}
}

// sandbox makes a sandbox on c and returns its id (its workspace's too).
func sandbox(t *testing.T, c *opsforgeapi.Client) string {
	t.Helper()
	pending, err := c.Sandboxes().Create(context.Background(), lab.SandboxRequest{Kind: "lab", Label: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return pending.Sandbox
}

func TestFiles(t *testing.T) {
	_, c := fake(t)
	files, ctx := c.Files(), context.Background()
	ws := sandbox(t, c)
	if err := files.Write(ctx, ws, "k8s/pod one.yaml", []byte("kind: Pod\n")); err != nil {
		t.Fatal(err)
	}
	if err := files.Write(ctx, ws, "empty", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := files.Read(ctx, ws, "k8s/pod one.yaml"); err != nil || string(got) != "kind: Pod\n" {
		t.Fatalf("read %q %v", got, err)
	}
	if list, err := files.List(ctx, ws); err != nil || len(list) != 2 || list[1].Path != "k8s/pod one.yaml" ||
		list[1].Size != 10 {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := files.Rename(ctx, ws, "empty", "k8s/pod one.yaml"); !errors.Is(err, workspace.ErrExists) {
		t.Fatalf("onto a file: %v", err)
	}
	if err := files.Rename(ctx, ws, "empty", "moved"); err != nil {
		t.Fatal(err)
	}
	if err := files.Remove(ctx, ws, "moved"); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"read":   func() error { _, err := files.Read(ctx, ws, "moved"); return err }(),
		"list":   func() error { _, err := files.List(ctx, "0123abcd"); return err }(),
		"remove": files.Remove(ctx, ws, "moved"),
	} {
		if !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := files.Write(ctx, ws, "big", make([]byte, workspace.MaxFile+1)); !errors.Is(err, workspace.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
}

// readUntil reads a shell's output until it holds want.
func readUntil(t *testing.T, p terminal.Pty, want string) string {
	t.Helper()
	var out strings.Builder
	buf := make([]byte, 4096)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in %q", want, out.String())
		}
		n, err := p.Read(buf)
		out.Write(buf[:n])
		if err != nil && !strings.Contains(out.String(), want) {
			t.Fatalf("waiting for %q: %v; got %q", want, err, out.String())
		}
	}
	return out.String()
}

func TestShells(t *testing.T) {
	_, c := fake(t)
	ws := sandbox(t, c)
	p, err := c.Shells().Spawn(terminal.Spec{Workspace: ws, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "Lab terminal in /workspaces/"+ws)
	_, _ = p.Write([]byte("stty size\r"))
	readUntil(t, p, "30 100")
	if err := p.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write([]byte("stty size\r"))
	readUntil(t, p, "40 120")
	_, _ = p.Write([]byte("exit\r"))
	buf := make([]byte, 4096)
	for {
		if _, err := p.Read(buf); err != nil {
			break
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Shells().Spawn(terminal.Spec{Workspace: "0123abcd"}); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("unknown sandbox: %v", err)
	}
	if _, err := c.Shells().Spawn(terminal.Spec{}); !errors.Is(err, opsforgeapi.ErrNoWorkspace) {
		t.Fatalf("no workspace: %v", err)
	}
}
