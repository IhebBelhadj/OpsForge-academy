package tutor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// fakeRunner starts fakeSessions; installed says which agents are available, startErr fails every start.
type fakeRunner struct {
	mu        sync.Mutex
	installed map[string]bool
	startErr  error
	gate      chan struct{} // when set, Start waits for it (or ctx)
	sessions  []*fakeSession
	endpoints []Endpoint
}

func (r *fakeRunner) Available(a Agent) (bool, string) {
	if r.installed[a.ID] {
		return true, ""
	}
	return false, a.Command[0] + " is not on the PATH"
}

func (r *fakeRunner) Start(ctx context.Context, _ Agent, tools Endpoint) (Session, error) {
	if r.gate != nil {
		select {
		case <-r.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	s := &fakeSession{answers: make(chan func(func(Update)) (string, error), 4), cancelled: make(chan struct{}, 1)}
	r.sessions = append(r.sessions, s)
	r.endpoints = append(r.endpoints, tools)
	return s, nil
}

func (r *fakeRunner) session(t *testing.T, i int) *fakeSession {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= len(r.sessions) {
		t.Fatalf("session %d was not started (%d were)", i, len(r.sessions))
	}
	return r.sessions[i]
}

// fakeSession answers each prompt with the next function queued on answers.
type fakeSession struct {
	mu        sync.Mutex
	prompts   []string
	answers   chan func(func(Update)) (string, error)
	cancelled chan struct{}
	closed    bool
}

func (s *fakeSession) Prompt(ctx context.Context, text string, update func(Update)) (string, error) {
	s.mu.Lock()
	s.prompts = append(s.prompts, text)
	s.mu.Unlock()
	select {
	case answer := <-s.answers:
		return answer(update)
	case <-s.cancelled:
		return "cancelled", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (s *fakeSession) Cancel() { s.cancelled <- struct{}{} }

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// events records what the pages were told.
type events struct {
	mu  sync.Mutex
	got []Event
}

func (e *events) Send(_ int64, m platform.Message) {
	var ev Event
	if m.Event != EventName || json.Unmarshal(m.Data, &ev) != nil {
		panic("not a tutor event: " + string(m.Data))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, ev)
}

// await waits for an event matching ok.
func (e *events) await(t *testing.T, ok func(Event) bool) Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		for _, ev := range e.got {
			if ok(ev) {
				e.mu.Unlock()
				return ev
			}
		}
		e.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no such event in %+v", e.got)
	return Event{}
}

func status(id string, st Status) func(Event) bool {
	return func(e Event) bool { return e.Conversation == id && e.Type == "status" && *e.Status == st }
}

var agents = []Agent{{ID: "claude", Name: "Claude Code", Command: []string{"npx", "claude-agent-acp"}},
	{ID: "gemini", Name: "Gemini CLI", Command: []string{"gemini", "--experimental-acp"}}}

// learner 7's attempt 3 (workspace sb-3) is the only one there is.
func sources() Sources {
	return Sources{
		Attempt: func(_ context.Context, user, attempt int64) (Attempt, error) {
			if user != 7 || attempt != 3 {
				return Attempt{}, ErrAttemptNotFound
			}
			return Attempt{ID: 3, Lab: "regions-and-latency", Title: "Regions and latency", Status: "running",
				Current: "measure", Workspace: "sb-3",
				Steps: []Step{{ID: "measure", Title: "Measure", Text: "Ping c from a.", Hints: []string{}}}}, nil
		},
		State: func(context.Context) (lab.State, error) {
			return lab.State{Up: true, Placeholders: map[string]string{"${node:a}": "a-cp1"}}, nil
		},
		Terminal: func(ws string) ([]byte, bool) {
			if ws != "sb-3" {
				return nil, false
			}
			return []byte("\x1b[1m$\x1b[0m ping c\r\nold line\r\n64 bytes from c: time=80 ms\r\n"), true
		},
	}
}

func newTestService(t *testing.T) (*Service, *fakeRunner, *events) {
	t.Helper()
	runner := &fakeRunner{installed: map[string]bool{"claude": true}}
	ev := &events{}
	s := NewService(runner, agents, sources(), ev, platform.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetEndpoint("http://127.0.0.1:8090/mcp")
	t.Cleanup(s.Shutdown)
	return s, runner, ev
}

func TestAgentsSayWhichCanRun(t *testing.T) {
	s, _, _ := newTestService(t)
	got := s.Agents()
	if len(got) != 2 || !got[0].Available || got[1].Available || got[1].Detail != "gemini is not on the PATH" {
		t.Fatalf("%+v", got)
	}
}

func TestStartRefusals(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	for _, tc := range []struct {
		agent   string
		user    int64
		attempt int64
		want    error
	}{
		{"codex", 7, 3, ErrNoSuchAgent},
		{"gemini", 7, 3, ErrAgentUnavailable},
		{"claude", 7, 4, ErrAttemptNotFound},
		{"claude", 8, 3, ErrAttemptNotFound}, // someone else's
	} {
		if _, err := s.Start(ctx, tc.user, tc.agent, tc.attempt); !errors.Is(err, tc.want) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}

func TestAConversation(t *testing.T) {
	s, runner, ev := newTestService(t)
	ctx := context.Background()
	c, err := s.Start(ctx, 7, "claude", 3)
	if err != nil || c.Status != Starting || c.Agent != "claude" || c.Attempt != 3 {
		t.Fatalf("%+v %v", c, err)
	}
	ev.await(t, status(c.ID, Ready))
	if ep := runner.endpoints[0]; ep.URL != "http://127.0.0.1:8090/mcp" || len(ep.Token) < 32 {
		t.Fatalf("%+v", ep)
	}

	sess := runner.session(t, 0)
	sess.answers <- func(update func(Update)) (string, error) {
		update(Update{Kind: UpdateTool, Tool: ToolCall{ID: "t1", Title: "mcp__academy__lab_attempt", Status: "pending"}})
		update(Update{Kind: UpdateTool, Tool: ToolCall{ID: "t1", Status: "completed"}})
		update(Update{Kind: UpdateText, Text: "Your ping "})
		update(Update{Kind: UpdateText, Text: "took 80 ms."})
		return "end_turn", nil
	}
	if err := s.Send(ctx, 7, c.ID, "  why is it slow?  "); err != nil {
		t.Fatal(err)
	}
	done := ev.await(t, func(e Event) bool { return e.Type == "done" })
	if *done.Stop != "end_turn" || done.Error != nil {
		t.Fatalf("%+v", done)
	}
	ev.await(t, func(e Event) bool { return e.Type == "tool" && e.Tool.Status == "completed" && e.Tool.Title != "" })

	got, err := s.Get(ctx, 7, c.ID)
	if err != nil || got.Status != Ready || len(got.Messages) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if m := got.Messages[0]; m.Role != Learner || m.Text != "why is it slow?" {
		t.Fatalf("%+v", m)
	}
	if m := got.Messages[1]; m.Role != Tutor || m.Text != "Your ping took 80 ms." || len(m.Tools) != 1 ||
		m.Tools[0] != (ToolCall{ID: "t1", Title: "mcp__academy__lab_attempt", Status: "completed"}) {
		t.Fatalf("%+v", m)
	}
	// The preamble goes with the first message only
	sess.answers <- func(func(Update)) (string, error) { return "end_turn", nil }
	if err := s.Send(ctx, 7, c.ID, "thanks"); err != nil {
		t.Fatal(err)
	}
	ev.await(t, func(e Event) bool { return e.Type == "done" && len(ev.got) > 0 && sess.promptCount() == 2 })
	if p := sess.prompt(0); !strings.HasPrefix(p, "You are the tutor") || !strings.HasSuffix(p, "why is it slow?") {
		t.Fatalf("%q", p)
	}
	if p := sess.prompt(1); p != "thanks" {
		t.Fatalf("%q", p)
	}

	// Another learner cannot see or use it
	if _, err := s.Get(ctx, 8, c.ID); !errors.Is(err, ErrConversationNotFound) {
		t.Fatal(err)
	}
	if err := s.Send(ctx, 8, c.ID, "hi"); !errors.Is(err, ErrConversationNotFound) {
		t.Fatal(err)
	}
	if len(s.List(ctx, 7)) != 1 || len(s.List(ctx, 8)) != 0 {
		t.Fatal("list")
	}

	if err := s.Close(ctx, 7, c.ID); err != nil {
		t.Fatal(err)
	}
	ev.await(t, status(c.ID, Closed))
	if !sess.isClosed() {
		t.Fatal("the agent still runs")
	}
	if _, err := s.Get(ctx, 7, c.ID); !errors.Is(err, ErrConversationNotFound) {
		t.Fatal(err)
	}
}

func (s *fakeSession) promptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.prompts)
}

func (s *fakeSession) prompt(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prompts[i]
}

func TestSendRefusals(t *testing.T) {
	s, runner, ev := newTestService(t)
	ctx := context.Background()
	runner.gate = make(chan struct{})
	c, _ := s.Start(ctx, 7, "claude", 3)
	if err := s.Send(ctx, 7, c.ID, "hi"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("while starting: %v", err)
	}
	close(runner.gate)
	ev.await(t, status(c.ID, Ready))
	for _, text := range []string{"", "  \n ", strings.Repeat("x", MaxMessage+1)} {
		if err := s.Send(ctx, 7, c.ID, text); !errors.Is(err, ErrEmptyMessage) {
			t.Fatalf("%d bytes: %v", len(text), err)
		}
	}
	if err := s.Send(ctx, 7, c.ID, "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(ctx, 7, c.ID, "two"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("while answering: %v", err)
	}
	// Cancel ends the answer
	if err := s.Cancel(ctx, 7, c.ID); err != nil {
		t.Fatal(err)
	}
	if done := ev.await(t, func(e Event) bool { return e.Type == "done" }); *done.Stop != "cancelled" {
		t.Fatalf("%+v", done)
	}
}

func TestABrokenAgent(t *testing.T) {
	s, runner, ev := newTestService(t)
	ctx := context.Background()

	runner.startErr = errors.New("exec: npx: not found")
	c, _ := s.Start(ctx, 7, "claude", 3)
	failed := ev.await(t, status(c.ID, Failed))
	if *failed.Error != "Claude Code did not start: exec: npx: not found" {
		t.Fatalf("%q", *failed.Error)
	}
	// It stays readable (a page that missed the event learns why), and cannot be written to
	if got, err := s.Get(ctx, 7, c.ID); err != nil || got.Status != Failed || got.Error != *failed.Error {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.Send(ctx, 7, c.ID, "hi"); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}

	// An answer that breaks off ends the conversation
	runner.startErr = nil
	c, _ = s.Start(ctx, 7, "claude", 3)
	ev.await(t, status(c.ID, Ready))
	sess := runner.session(t, 0)
	sess.answers <- func(update func(Update)) (string, error) {
		update(Update{Kind: UpdateText, Text: "Let me"})
		return "", io.ErrUnexpectedEOF
	}
	_ = s.Send(ctx, 7, c.ID, "hi")
	if done := ev.await(t, func(e Event) bool { return e.Type == "done" }); *done.Error != "unexpected EOF" {
		t.Fatalf("%+v", done)
	}
	ev.await(t, status(c.ID, Failed))
	if !sess.isClosed() {
		t.Fatal("the broken agent still runs")
	}
	// Closing it forgets it
	if err := s.Close(ctx, 7, c.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.List(ctx, 7)) != 0 {
		t.Fatal("still listed")
	}
}

func TestOneConversationPerLearner(t *testing.T) {
	s, runner, ev := newTestService(t)
	ctx := context.Background()
	first, _ := s.Start(ctx, 7, "claude", 3)
	ev.await(t, status(first.ID, Ready))
	second, _ := s.Start(ctx, 7, "claude", 3)
	ev.await(t, status(first.ID, Closed))
	ev.await(t, status(second.ID, Ready))
	if !runner.session(t, 0).isClosed() || runner.session(t, 1).isClosed() {
		t.Fatal("the first agent should be the one stopped")
	}
	if list := s.List(ctx, 7); len(list) != 1 || list[0].ID != second.ID {
		t.Fatalf("%+v", list)
	}
}

func TestIdleConversationsEnd(t *testing.T) {
	s, runner, ev := newTestService(t)
	s.SetIdleTimeout(50 * time.Millisecond)
	c, _ := s.Start(context.Background(), 7, "claude", 3)
	closed := ev.await(t, status(c.ID, Closed))
	if !strings.HasPrefix(*closed.Error, "closed after 50ms") || !runner.session(t, 0).isClosed() {
		t.Fatalf("%+v", closed)
	}
	if got, _ := s.Get(context.Background(), 7, c.ID); got.Status != Closed {
		t.Fatalf("%+v", got)
	}
}

func TestShutdownStopsAgentsStillStarting(t *testing.T) {
	s, runner, ev := newTestService(t)
	runner.gate = make(chan struct{}) // never opens
	c, _ := s.Start(context.Background(), 7, "claude", 3)
	done := make(chan struct{})
	go func() { s.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown waited for the start")
	}
	ev.await(t, status(c.ID, Closed))
	if _, err := s.Start(context.Background(), 7, "claude", 3); !errors.Is(err, errShutdown) {
		t.Fatal(err)
	}
}

func TestTools(t *testing.T) {
	s, _, ev := newTestService(t)
	ctx := context.Background()
	c, _ := s.Start(ctx, 7, "claude", 3)
	ev.await(t, status(c.ID, Ready))
	token := s.convs[c.ID].token

	a, err := s.LabAttempt(ctx, token)
	if err != nil || a.Lab != "regions-and-latency" || a.Steps[0].Text != "Ping c from a." {
		t.Fatalf("%+v %v", a, err)
	}
	if doc, _ := json.Marshal(a); strings.Contains(string(doc), "sb-3") {
		t.Fatalf("the workspace is not the agent's business: %s", doc)
	}
	st, err := s.LabState(ctx, token)
	if err != nil || !st.Up || st.Placeholders != nil {
		t.Fatalf("%+v %v", st, err)
	}
	out, ok, err := s.TerminalOutput(ctx, token, 0)
	if err != nil || !ok || out != "$ ping c\nold line\n64 bytes from c: time=80 ms\n" {
		t.Fatalf("%q %v %v", out, ok, err)
	}
	if out, _, _ := s.TerminalOutput(ctx, token, 30); out != "64 bytes from c: time=80 ms\n" {
		t.Fatalf("%q", out)
	}

	for _, bad := range []string{"", "nope"} {
		if _, err := s.LabAttempt(ctx, bad); !errors.Is(err, ErrUnknownToken) {
			t.Fatal(err)
		}
		if _, err := s.LabState(ctx, bad); !errors.Is(err, ErrUnknownToken) {
			t.Fatal(err)
		}
	}
	// An ended conversation's token opens nothing
	s.mu.Lock()
	ended := s.endLocked(s.convs[c.ID], Closed, "")
	s.mu.Unlock()
	s.stop(ended)
	if _, _, err := s.TerminalOutput(ctx, token, 0); !errors.Is(err, ErrUnknownToken) {
		t.Fatal(err)
	}
}
