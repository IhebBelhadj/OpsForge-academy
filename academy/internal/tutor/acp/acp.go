// Package acp runs the tutor's agents (tutor.Runner): each one a process speaking the Agent Client Protocol on its
// stdio (github.com/coder/acp-go-sdk), in a working directory of its own, empty and removed after. Academy is a
// client with no file system or terminal to lend: the agent works through Academy's MCP server (over HTTP when it
// says it can, else through `academy mcp-proxy` on stdio, which every ACP agent supports), and every permission it
// asks for is refused except for those tools (tutor.Permit).
package acp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	sdk "github.com/coder/acp-go-sdk"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor"
)

// stopGrace is how long an agent gets to exit once its stdin is closed, then after SIGTERM, before SIGKILL.
const stopGrace = 3 * time.Second

// stderrKeep is how much of an agent's stderr is kept to explain why it failed.
const stderrKeep = 4 << 10

// Runner starts agents. Proxy is the argv of `academy mcp-proxy` (the endpoint's URL is appended, its token goes
// in tutor.TokenEnv); Dir is where the working directories are made (os.TempDir when empty).
type Runner struct {
	Proxy []string
	Dir   string
	Log   *slog.Logger
}

var _ tutor.Runner = (*Runner)(nil)

// Available is whether the agent's command is on the PATH.
func (r *Runner) Available(a tutor.Agent) (bool, string) {
	if len(a.Command) == 0 {
		return false, "no command is configured"
	}
	if _, err := exec.LookPath(a.Command[0]); err != nil {
		return false, a.Command[0] + " is not installed (not on the PATH)"
	}
	return true, ""
}

// Start runs the agent, initializes ACP and opens a session with Academy's MCP server.
func (r *Runner) Start(ctx context.Context, agent tutor.Agent, tools tutor.Endpoint) (tutor.Session, error) {
	if ok, why := r.Available(agent); !ok {
		return nil, errors.New(why)
	}
	dir, err := os.MkdirTemp(r.Dir, "academy-tutor-")
	if err != nil {
		return nil, err
	}
	// Not CommandContext: the agent outlives ctx (the start) until Close. G204: the agents are Academy's own list
	cmd := exec.Command(agent.Command[0], agent.Command[1:]...) //nolint:gosec,noctx // see above
	cmd.Dir = dir
	// Its own process group: npx-started adapters run node children that must stop with it
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	s := &session{cmd: cmd, dir: dir, exited: make(chan struct{})}
	cmd.Stderr = &s.stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	s.stdin = stdin
	// A plain pipe for stdout (not StdoutPipe): Wait must not close it under the connection's reader
	out, in, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	cmd.Stdout = in
	if err := cmd.Start(); err != nil {
		_ = out.Close()
		_ = in.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	_ = in.Close()
	go func() {
		s.waitErr = cmd.Wait()
		_ = out.Close() // the connection's reader ends: pending requests fail
		close(s.exited)
	}()
	s.conn = sdk.NewClientSideConnection(s, stdin, out)
	log := r.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s.conn.SetLogger(log)

	init, err := s.conn.Initialize(ctx, sdk.InitializeRequest{
		ProtocolVersion:    sdk.ProtocolVersionNumber,
		ClientCapabilities: sdk.ClientCapabilities{},
		ClientInfo:         &sdk.Implementation{Name: "opsforge-academy", Version: "1"},
	})
	if err != nil {
		return nil, s.fail("initialize", err)
	}
	server, err := r.server(init.AgentCapabilities.McpCapabilities, tools)
	if err != nil {
		return nil, s.fail("session", err)
	}
	ns, err := s.conn.NewSession(ctx, sdk.NewSessionRequest{Cwd: dir, McpServers: []sdk.McpServer{server}})
	if err != nil {
		return nil, s.fail("session", err)
	}
	s.id = ns.SessionId
	return s, nil
}

// server is Academy's MCP server as the agent can reach it.
func (r *Runner) server(caps sdk.McpCapabilities, tools tutor.Endpoint) (sdk.McpServer, error) {
	if caps.Http {
		return sdk.McpServer{Http: &sdk.McpServerHttpInline{Type: "http", Name: tutor.ServerName, Url: tools.URL,
			Headers: []sdk.HttpHeader{{Name: "Authorization", Value: "Bearer " + tools.Token}}}}, nil
	}
	if len(r.Proxy) == 0 {
		return sdk.McpServer{}, errors.New("the agent reaches MCP servers only on stdio, and no proxy is configured")
	}
	args := append(append([]string{}, r.Proxy[1:]...), tools.URL)
	return sdk.McpServer{Stdio: &sdk.McpServerStdio{Name: tutor.ServerName, Command: r.Proxy[0], Args: args,
		Env: []sdk.EnvVariable{{Name: tutor.TokenEnv, Value: tools.Token}}}}, nil
}

// session is one running agent; it is also the ACP client the agent calls back.
type session struct {
	cmd     *exec.Cmd
	dir     string
	stdin   io.Closer
	conn    *sdk.ClientSideConnection
	id      sdk.SessionId
	stderr  tail
	exited  chan struct{}
	waitErr error

	mu     sync.Mutex
	update func(tutor.Update) // the current prompt's
	once   sync.Once
}

// Prompt sends text and streams the answer until the agent ends its turn.
func (s *session) Prompt(ctx context.Context, text string, update func(tutor.Update)) (string, error) {
	s.mu.Lock()
	s.update = update
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.update = nil
		s.mu.Unlock()
	}()
	resp, err := s.conn.Prompt(ctx, sdk.PromptRequest{SessionId: s.id, Prompt: []sdk.ContentBlock{sdk.TextBlock(text)}})
	if err != nil {
		if ctx.Err() != nil {
			s.Cancel()
		}
		return "", s.explain(err)
	}
	return string(resp.StopReason), nil
}

// Cancel asks the agent to stop answering.
func (s *session) Cancel() {
	_ = s.conn.Cancel(context.Background(), sdk.CancelNotification{SessionId: s.id})
}

// Close stops the agent: its stdin closed (an ACP agent exits at EOF), then SIGTERM, then SIGKILL to its process
// group; its working directory is removed.
func (s *session) Close() error {
	var err error
	s.once.Do(func() {
		_ = s.stdin.Close()
		for _, sig := range []syscall.Signal{0, syscall.SIGTERM, syscall.SIGKILL} {
			if sig != 0 {
				_ = syscall.Kill(-s.cmd.Process.Pid, sig)
			}
			select {
			case <-s.exited:
				err = os.RemoveAll(s.dir)
				return
			case <-time.After(stopGrace):
			}
		}
		err = fmt.Errorf("agent process %d did not exit", s.cmd.Process.Pid)
	})
	return err
}

// fail stops a session that could not start and says why, with what the agent printed.
func (s *session) fail(stage string, err error) error {
	err = s.explain(err)
	_ = s.Close()
	return fmt.Errorf("%s: %w", stage, err)
}

// explain adds the agent's last stderr lines to an error, once its process has ended.
func (s *session) explain(err error) error {
	select {
	case <-s.exited:
	case <-time.After(100 * time.Millisecond):
		return err
	}
	if msg := strings.TrimSpace(s.stderr.String()); msg != "" {
		lines := strings.Split(msg, "\n")
		return fmt.Errorf("%w (the agent exited: %s)", err, strings.Join(lines[max(0, len(lines)-3):], " | "))
	}
	if s.waitErr != nil {
		return fmt.Errorf("%w (the agent exited: %w)", err, s.waitErr)
	}
	return err
}

func (s *session) emit(u tutor.Update) {
	s.mu.Lock()
	update := s.update
	s.mu.Unlock()
	if update != nil {
		update(u)
	}
}

// The ACP client side: what the agent may ask of Academy.

// SessionUpdate streams the answer: text and tool calls (thoughts and plans are not shown).
func (s *session) SessionUpdate(_ context.Context, n sdk.SessionNotification) error {
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		s.emit(tutor.Update{Kind: tutor.UpdateText, Text: u.AgentMessageChunk.Content.Text.Text})
	case u.ToolCall != nil:
		s.emit(tutor.Update{Kind: tutor.UpdateTool, Tool: tutor.ToolCall{ID: string(u.ToolCall.ToolCallId),
			Title: u.ToolCall.Title, Status: string(u.ToolCall.Status)}})
	case u.ToolCallUpdate != nil:
		call := tutor.ToolCall{ID: string(u.ToolCallUpdate.ToolCallId)}
		if u.ToolCallUpdate.Title != nil {
			call.Title = *u.ToolCallUpdate.Title
		}
		if u.ToolCallUpdate.Status != nil {
			call.Status = string(*u.ToolCallUpdate.Status)
		}
		s.emit(tutor.Update{Kind: tutor.UpdateTool, Tool: call})
	}
	return nil
}

// RequestPermission allows Academy's tools once and refuses everything else (the refusal shows in the answer).
func (s *session) RequestPermission(_ context.Context, p sdk.RequestPermissionRequest) (sdk.RequestPermissionResponse,
	error) {
	title := ""
	if p.ToolCall.Title != nil {
		title = *p.ToolCall.Title
	}
	allow := tutor.Permit(title)
	want := []sdk.PermissionOptionKind{sdk.PermissionOptionKindRejectOnce, sdk.PermissionOptionKindRejectAlways}
	if allow {
		want = []sdk.PermissionOptionKind{sdk.PermissionOptionKindAllowOnce, sdk.PermissionOptionKindAllowAlways}
	} else {
		s.emit(tutor.Update{Kind: tutor.UpdateTool, Tool: tutor.ToolCall{ID: string(p.ToolCall.ToolCallId), Title: title,
			Status: tutor.Denied}})
	}
	for _, kind := range want {
		for _, o := range p.Options {
			if o.Kind == kind {
				return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeSelected(o.OptionId)}, nil
			}
		}
	}
	return sdk.RequestPermissionResponse{Outcome: sdk.NewRequestPermissionOutcomeCancelled()}, nil
}

var errNoCapability = errors.New("the tutor lends no file system or terminal; use the academy tools")

// ReadTextFile is refused (the client capability is not announced).
func (s *session) ReadTextFile(context.Context, sdk.ReadTextFileRequest) (sdk.ReadTextFileResponse, error) {
	return sdk.ReadTextFileResponse{}, errNoCapability
}

// WriteTextFile is refused.
func (s *session) WriteTextFile(context.Context, sdk.WriteTextFileRequest) (sdk.WriteTextFileResponse, error) {
	return sdk.WriteTextFileResponse{}, errNoCapability
}

// CreateTerminal is refused.
func (s *session) CreateTerminal(context.Context, sdk.CreateTerminalRequest) (sdk.CreateTerminalResponse, error) {
	return sdk.CreateTerminalResponse{}, errNoCapability
}

// KillTerminal is refused.
func (s *session) KillTerminal(context.Context, sdk.KillTerminalRequest) (sdk.KillTerminalResponse, error) {
	return sdk.KillTerminalResponse{}, errNoCapability
}

// TerminalOutput is refused.
func (s *session) TerminalOutput(context.Context, sdk.TerminalOutputRequest) (sdk.TerminalOutputResponse, error) {
	return sdk.TerminalOutputResponse{}, errNoCapability
}

// ReleaseTerminal is refused.
func (s *session) ReleaseTerminal(context.Context, sdk.ReleaseTerminalRequest) (sdk.ReleaseTerminalResponse, error) {
	return sdk.ReleaseTerminalResponse{}, errNoCapability
}

// WaitForTerminalExit is refused.
func (s *session) WaitForTerminalExit(context.Context, sdk.WaitForTerminalExitRequest) (
	sdk.WaitForTerminalExitResponse, error) {
	return sdk.WaitForTerminalExitResponse{}, errNoCapability
}

// tail keeps the last stderrKeep bytes written to it.
type tail struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if extra := t.buf.Len() - stderrKeep; extra > 0 {
		t.buf.Next(extra)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}
