package opsforgeapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/opsforgeapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/terminal"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/workspace"
)

// real is a client of a real `opsforge api serve`, on this machine or another (ACADEMY_OPSFORGE_URL,
// ACADEMY_OPSFORGE_TOKEN_FILE and, for a self-signed https one, ACADEMY_OPSFORGE_CA_FILE); the test is skipped
// without one. The lab need not be up: lab work runs host steps.
func real(t *testing.T) *opsforgeapi.Client {
	t.Helper()
	url, tokenFile := os.Getenv("ACADEMY_OPSFORGE_URL"), os.Getenv("ACADEMY_OPSFORGE_TOKEN_FILE")
	if url == "" || tokenFile == "" {
		t.Skip("no OpsForge API: set ACADEMY_OPSFORGE_URL and ACADEMY_OPSFORGE_TOKEN_FILE")
	}
	c, err := opsforgeapi.New(url, opsforgeapi.TokenFile(tokenFile), 30*time.Second, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ca := os.Getenv("ACADEMY_OPSFORGE_CA_FILE"); ca != "" {
		if err := c.TrustCA(ca); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func echo(name string, value int) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"name": name, "run": map[string]any{"cmd": "echo " + string(rune('0'+value))}})
	return data
}

func TestRealOpsForge(t *testing.T) {
	c := real(t)
	ctx := context.Background()
	if err := c.Ready(ctx); err != nil {
		t.Fatalf("ready: %v", err)
	}

	errs, err := c.CheckSteps(ctx, []lab.StepSet{
		{Steps: []any{map[string]any{"sleep": "1s"}}},
		{Steps: []any{map[string]any{"partition": "c"}}, NoFaults: true},
	})
	if err != nil || len(errs) != 1 || errs[0].Set != 1 || !strings.Contains(errs[0].Message, "fault steps") {
		t.Fatalf("steps %+v %v", errs, err)
	}

	sandboxes := c.Sandboxes()
	sb := liveSandbox(t, c, lab.SandboxRequest{Kind: "lab", Label: "academy integration test",
		Setup: []json.RawMessage{echo("x", 7)}})
	if sb.Status != lab.SandboxReady || sb.Setup.Error != "" || sb.Setup.Metrics["x.value"] != 7 || len(sb.Setup.Log) == 0 {
		t.Fatalf("setup %+v", sb.Setup)
	}
	probe, err := sandboxes.Probe(ctx, sb.ID, lab.ProbeRequest{Steps: []json.RawMessage{echo("a", 1), echo("b", 2)},
		Expect: json.RawMessage(`{"a.value": "== 0", "b.value": "== 2"}`)})
	if err != nil || probe.Passed || len(probe.Results) != 2 || probe.Results[1].Checked {
		t.Fatalf("probe stops at the first failing named step: %+v %v", probe, err)
	}
	var p *opsforgeapi.Problem
	if _, err := sandboxes.Probe(ctx, sb.ID, lab.ProbeRequest{Steps: []json.RawMessage{json.RawMessage(`{"partition":"c"}`)},
		Expect: json.RawMessage(`{"x.value": "== 1"}`)}); !errors.As(err, &p) || p.Status != 400 || p.Slug() != "bad-input" {
		t.Fatalf("a probe that would break links: %v", err)
	}
	if gone, err := sandboxes.Delete(ctx, sb.ID); err != nil || gone.Status != lab.SandboxDeleted {
		t.Fatalf("delete %+v %v", gone, err)
	}
	if _, err := sandboxes.Delete(ctx, sb.ID); !errors.Is(err, lab.ErrGone) {
		t.Fatalf("deleted twice: %v", err)
	}
}

// liveSandbox makes a sandbox on the real OpsForge and awaits it; skips the test when the lab is down (OpsForge
// makes sandboxes only on a running lab: CI's has none) after checking that refusal. Deleted at the end.
func liveSandbox(t *testing.T, c *opsforgeapi.Client, req lab.SandboxRequest) lab.Sandbox {
	t.Helper()
	ctx := context.Background()
	pending, err := c.Sandboxes().Create(ctx, req)
	if errors.Is(err, lab.ErrLabDown) {
		t.Skipf("the lab is down (sandboxes need it): %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.Sandboxes().Delete(context.Background(), pending.Sandbox) })
	sb, err := c.Sandboxes().Await(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

func TestRealWorkspaceAndShell(t *testing.T) {
	c := real(t)
	ctx := context.Background()
	files := c.Files()
	sb := liveSandbox(t, c, lab.SandboxRequest{Kind: "lab", Label: "academy integration test (shell)", Namespace: true,
		Files: map[string]string{"k8s/hello.yaml": "kind: ConfigMap\n"}})
	ws := sb.ID
	for _, bad := range []string{"../escape", "a//b"} {
		if err := files.Write(ctx, ws, bad, []byte("x")); !errors.Is(err, workspace.ErrBadPath) {
			t.Errorf("%s: %v", bad, err)
		}
	}

	// bash on the lab host, in the workspace: Phase 10's terminal (paste, a resize, job control, exit)
	shell, err := c.Shells().Spawn(terminal.Spec{Workspace: ws, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shell.Close() }()
	readUntil(t, shell, "Lab terminal in")
	_, _ = shell.Write([]byte("\x1b[200~echo parity-$((6*7))\x1b[201~\r"))
	readUntil(t, shell, "parity-42")
	_, _ = shell.Write([]byte("stty size; cat k8s/hello.yaml; echo ns=$OPSFORGE_NAMESPACE; type opsforge | head -1\r"))
	out := readUntil(t, shell, "opsforge is a function")
	if !strings.Contains(out, "30 100") || !strings.Contains(out, "kind: ConfigMap") ||
		!strings.Contains(out, "ns="+*sb.Namespace) {
		t.Fatalf("shell: %q", out)
	}
	if err := shell.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	_, _ = shell.Write([]byte("stty size; set -o | grep -q 'monitor *on' && echo job-control\r"))
	readUntil(t, shell, "job-control")
	_, _ = shell.Write([]byte("echo made-here > new.txt; exit\r"))
	buf := make([]byte, 4096)
	for {
		if _, err := shell.Read(buf); err != nil {
			break
		}
	}
	if got, err := files.Read(ctx, ws, "new.txt"); err != nil || string(got) != "made-here\n" {
		t.Fatalf("a file made in the terminal: %q %v", got, err)
	}
	if _, err := c.Sandboxes().Delete(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := files.List(ctx, ws); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("the files go with the sandbox: %v", err)
	}
}
