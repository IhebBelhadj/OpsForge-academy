package acp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor"
)

// fakeEnv makes the test binary a fake ACP agent: its value is the mode ("http" announces HTTP MCP servers,
// "exit" exits at once, complaining on stderr).
const fakeEnv = "ACADEMY_TEST_FAKE_AGENT"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakeAgent(mode))
	}
	os.Exit(m.Run())
}

// fakeAgentImpl answers prompts by what they say: "servers" describes the MCP server it was given, "tools" asks
// permission for an academy tool and a shell command, "wait" answers when cancelled, "crash" exits mid-answer.
type fakeAgentImpl struct {
	mode    string
	conn    *sdk.AgentSideConnection
	servers []sdk.McpServer
	cancel  chan struct{}
}

func fakeAgent(mode string) int {
	if mode == "exit" {
		fmt.Fprintln(os.Stderr, "starting...\nError: not signed in; run `fake login`")
		return 1
	}
	a := &fakeAgentImpl{mode: mode, cancel: make(chan struct{}, 1)}
	a.conn = sdk.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	<-a.conn.Done()
	return 0
}

func (a *fakeAgentImpl) Initialize(context.Context, sdk.InitializeRequest) (sdk.InitializeResponse, error) {
	return sdk.InitializeResponse{ProtocolVersion: sdk.ProtocolVersionNumber, AgentCapabilities: sdk.AgentCapabilities{
		McpCapabilities: sdk.McpCapabilities{Http: a.mode == "http"}}}, nil
}

func (a *fakeAgentImpl) NewSession(_ context.Context, p sdk.NewSessionRequest) (sdk.NewSessionResponse, error) {
	a.servers = p.McpServers
	if wd, _ := os.Getwd(); wd != p.Cwd {
		return sdk.NewSessionResponse{}, fmt.Errorf("started in %s, not the session's %s", wd, p.Cwd)
	}
	return sdk.NewSessionResponse{SessionId: "s1"}, nil
}

func (a *fakeAgentImpl) say(ctx context.Context, u sdk.SessionUpdate) {
	_ = a.conn.SessionUpdate(ctx, sdk.SessionNotification{SessionId: "s1", Update: u})
}

func (a *fakeAgentImpl) ask(ctx context.Context, id, title string) string {
	resp, err := a.conn.RequestPermission(ctx, sdk.RequestPermissionRequest{SessionId: "s1",
		ToolCall: sdk.ToolCallUpdate{ToolCallId: sdk.ToolCallId(id), Title: sdk.Ptr(title)},
		Options: []sdk.PermissionOption{{OptionId: "yes", Name: "Allow", Kind: sdk.PermissionOptionKindAllowOnce},
			{OptionId: "no", Name: "Reject", Kind: sdk.PermissionOptionKindRejectOnce}}})
	switch {
	case err != nil:
		return "error"
	case resp.Outcome.Selected != nil:
		return string(resp.Outcome.Selected.OptionId)
	}
	return "cancelled"
}

func (a *fakeAgentImpl) Prompt(ctx context.Context, p sdk.PromptRequest) (sdk.PromptResponse, error) {
	switch text := p.Prompt[0].Text.Text; text {
	case "servers":
		s := a.servers[0]
		switch {
		case s.Http != nil:
			a.say(ctx, sdk.UpdateAgentMessageText(fmt.Sprintf("http %s %s %s: %s", s.Http.Name, s.Http.Url,
				s.Http.Headers[0].Name, s.Http.Headers[0].Value)))
		case s.Stdio != nil:
			a.say(ctx, sdk.UpdateAgentMessageText(fmt.Sprintf("stdio %s %s %v %s=%s", s.Stdio.Name, s.Stdio.Command,
				s.Stdio.Args, s.Stdio.Env[0].Name, s.Stdio.Env[0].Value)))
		}
	case "tools":
		a.say(ctx, sdk.StartToolCall("t1", "mcp__academy__lab_state", sdk.WithStartStatus(sdk.ToolCallStatusPending)))
		mine := a.ask(ctx, "t1", "mcp__academy__lab_state")
		a.say(ctx, sdk.SessionUpdate{ToolCallUpdate: &sdk.SessionToolCallUpdate{ToolCallId: "t1",
			Status: sdk.Ptr(sdk.ToolCallStatusCompleted)}})
		shell := a.ask(ctx, "t2", "rm -rf ~")
		a.say(ctx, sdk.UpdateAgentThoughtText("(thinking)"))
		a.say(ctx, sdk.UpdateAgentMessageText("academy: "+mine+", shell: "+shell))
	case "wait":
		select {
		case <-a.cancel:
			return sdk.PromptResponse{StopReason: sdk.StopReasonCancelled}, nil
		case <-time.After(10 * time.Second):
		}
	case "crash":
		fmt.Fprintln(os.Stderr, "panic: the model went away")
		os.Exit(2)
	default:
		a.say(ctx, sdk.UpdateAgentMessageText("echo: "+text))
	}
	return sdk.PromptResponse{StopReason: sdk.StopReasonEndTurn}, nil
}

func (a *fakeAgentImpl) Cancel(context.Context, sdk.CancelNotification) error {
	a.cancel <- struct{}{}
	return nil
}

func (a *fakeAgentImpl) Authenticate(context.Context, sdk.AuthenticateRequest) (sdk.AuthenticateResponse, error) {
	return sdk.AuthenticateResponse{}, nil
}
func (a *fakeAgentImpl) Logout(context.Context, sdk.LogoutRequest) (sdk.LogoutResponse, error) {
	return sdk.LogoutResponse{}, nil
}
func (a *fakeAgentImpl) CloseSession(context.Context, sdk.CloseSessionRequest) (sdk.CloseSessionResponse, error) {
	return sdk.CloseSessionResponse{}, nil
}
func (a *fakeAgentImpl) ListSessions(context.Context, sdk.ListSessionsRequest) (sdk.ListSessionsResponse, error) {
	return sdk.ListSessionsResponse{}, nil
}
func (a *fakeAgentImpl) ResumeSession(context.Context, sdk.ResumeSessionRequest) (sdk.ResumeSessionResponse, error) {
	return sdk.ResumeSessionResponse{}, nil
}
func (a *fakeAgentImpl) SetSessionConfigOption(context.Context, sdk.SetSessionConfigOptionRequest) (
	sdk.SetSessionConfigOptionResponse, error) {
	return sdk.SetSessionConfigOptionResponse{}, nil
}
func (a *fakeAgentImpl) SetSessionMode(context.Context, sdk.SetSessionModeRequest) (sdk.SetSessionModeResponse, error) {
	return sdk.SetSessionModeResponse{}, nil
}

// start runs the fake agent in mode.
func start(t *testing.T, mode string) (tutor.Session, string) {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	dir := t.TempDir()
	r := &Runner{Proxy: []string{"/usr/bin/academy", "mcp-proxy"}, Dir: dir,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := r.Start(ctx, tutor.Agent{ID: "fake", Command: []string{os.Args[0]}},
		tutor.Endpoint{URL: "http://127.0.0.1:8090/mcp", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

// answer prompts and collects the answer's text and tool calls.
func answer(t *testing.T, s tutor.Session, text string) (string, []tutor.ToolCall, string) {
	t.Helper()
	var mu sync.Mutex
	var got strings.Builder
	var tools []tutor.ToolCall
	stop, err := s.Prompt(context.Background(), text, func(u tutor.Update) {
		mu.Lock()
		defer mu.Unlock()
		switch u.Kind {
		case tutor.UpdateText:
			got.WriteString(u.Text)
		case tutor.UpdateTool:
			tools = append(tools, u.Tool)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	return got.String(), tools, stop
}

func TestTheMCPServerTheAgentGets(t *testing.T) {
	s, _ := start(t, "http")
	if got, _, _ := answer(t, s, "servers"); got != "http academy http://127.0.0.1:8090/mcp Authorization: Bearer tok" {
		t.Fatal(got)
	}
	s, _ = start(t, "stdio")
	got, _, stop := answer(t, s, "servers")
	if got != "stdio academy /usr/bin/academy [mcp-proxy http://127.0.0.1:8090/mcp] ACADEMY_TUTOR_TOKEN=tok" ||
		stop != "end_turn" {
		t.Fatal(got, stop)
	}
}

func TestOnlyAcademysToolsAreAllowed(t *testing.T) {
	s, _ := start(t, "stdio")
	got, tools, _ := answer(t, s, "tools")
	if got != "academy: yes, shell: no" {
		t.Fatal(got)
	}
	want := []tutor.ToolCall{{ID: "t1", Title: "mcp__academy__lab_state", Status: "pending"},
		{ID: "t1", Status: "completed"}, {ID: "t2", Title: "rm -rf ~", Status: tutor.Denied}}
	// Notifications and permission requests reach the client on separate queues: only each call's own order holds
	if len(tools) != len(want) {
		t.Fatalf("%+v", tools)
	}
	for _, w := range want {
		if !slices.Contains(tools, w) {
			t.Fatalf("no %+v in %+v", w, tools)
		}
	}
}

func TestCancelAndClose(t *testing.T) {
	s, dir := start(t, "stdio")
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.Cancel()
	}()
	if _, _, stop := answer(t, s, "wait"); stop != "cancelled" {
		t.Fatal(stop)
	}
	if got, _, _ := answer(t, s, "hello"); got != "echo: hello" {
		t.Fatal(got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("the agent's working directory: %v", entries)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestAnAgentThatDies(t *testing.T) {
	s, _ := start(t, "stdio")
	_, err := s.Prompt(context.Background(), "crash", func(tutor.Update) {})
	if err == nil || !strings.Contains(err.Error(), "panic: the model went away") {
		t.Fatalf("%v", err)
	}

	t.Setenv(fakeEnv, "exit")
	r := &Runner{Dir: t.TempDir()}
	_, err = r.Start(context.Background(), tutor.Agent{ID: "fake", Command: []string{os.Args[0]}}, tutor.Endpoint{})
	if err == nil || !strings.Contains(err.Error(), "not signed in; run `fake login`") {
		t.Fatalf("%v", err)
	}
	if entries, _ := os.ReadDir(r.Dir); len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestAvailable(t *testing.T) {
	r := &Runner{}
	if ok, why := r.Available(tutor.Agent{Command: []string{"sh"}}); !ok || why != "" {
		t.Fatal(ok, why)
	}
	if ok, why := r.Available(tutor.Agent{Command: []string{"no-such-agent-cli"}}); ok ||
		why != "no-such-agent-cli is not installed (not on the PATH)" {
		t.Fatal(ok, why)
	}
}
