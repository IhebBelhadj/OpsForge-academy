package tutor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// Timeouts of an agent's work: starting it (the first start of an npx adapter downloads it) and one answer.
const (
	StartTimeout  = 3 * time.Minute
	AnswerTimeout = 10 * time.Minute
)

// ErrUnknownToken is a tool call with a token of no open conversation.
var ErrUnknownToken = errors.New("no open tutor conversation has this token")

var errShutdown = errors.New("the tutor is shutting down")

// Service runs the learners' conversations. Lock order: Service.mu only; agents are called outside it.
type Service struct {
	runner  Runner
	sources Sources
	sender  Sender
	clock   platform.Clock
	log     *slog.Logger
	agents  []Agent
	idle    time.Duration
	base    context.Context // ended by Shutdown: agents still starting or answering stop
	cancel  context.CancelFunc

	mu       sync.Mutex
	endpoint string
	convs    map[string]*conv
	closed   bool
	work     sync.WaitGroup
}

type conv struct {
	Conversation
	title   string // the lab's, for the preamble
	token   string
	session Session
	timer   *time.Timer
}

// NewService runs agents (in this order on the page) with runner; the tools read sources; pages hear through sender.
func NewService(runner Runner, agents []Agent, sources Sources, sender Sender, clock platform.Clock,
	log *slog.Logger) *Service {
	base, cancel := context.WithCancel(context.Background())
	return &Service{runner: runner, agents: agents, sources: sources, sender: sender, clock: clock, log: log,
		idle: IdleTimeout, base: base, cancel: cancel, convs: map[string]*conv{}}
}

// SetEndpoint is the URL of Academy's MCP server (known once it listens), given to the agents started after.
func (s *Service) SetEndpoint(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endpoint = url
}

// SetIdleTimeout changes how long an untouched conversation lives (tests).
func (s *Service) SetIdleTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idle = d
}

// Agents are the agents the tutor knows, each with whether it can run here.
func (s *Service) Agents() []Agent {
	out := make([]Agent, len(s.agents))
	for i, a := range s.agents {
		var why string
		if a.Available, why = s.runner.Available(a); why != "" {
			a.Detail = why
		}
		out[i] = a
	}
	return out
}

func (s *Service) agent(id string) (Agent, error) {
	for _, a := range s.agents {
		if a.ID == id {
			if ok, _ := s.runner.Available(a); !ok {
				return Agent{}, ErrAgentUnavailable
			}
			return a, nil
		}
	}
	return Agent{}, ErrNoSuchAgent
}

// Start opens a conversation about one of user's attempts with an agent; it starts in the background (status
// events follow). The user's previous conversation, if any, is closed: one agent runs per learner.
func (s *Service) Start(ctx context.Context, user int64, agentID string, attempt int64) (Conversation, error) {
	agent, err := s.agent(agentID)
	if err != nil {
		return Conversation{}, err
	}
	a, err := s.sources.Attempt(ctx, user, attempt)
	if err != nil {
		return Conversation{}, err
	}
	c := &conv{Conversation: Conversation{ID: newID(), User: user, Attempt: attempt, Agent: agent.ID, Status: Starting,
		Messages: []Message{}, CreatedAt: s.clock.Now()}, title: a.Title, token: newID() + newID()}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Conversation{}, errShutdown
	}
	var replaced []*conv
	for _, old := range s.convs {
		if old.User == user {
			if old.Status != Failed && old.Status != Closed {
				replaced = append(replaced, s.endLocked(old, Closed, ""))
			}
			delete(s.convs, old.ID)
		}
	}
	s.convs[c.ID] = c
	s.touchLocked(c)
	endpoint := s.endpoint
	view := c.copy()
	s.work.Add(1)
	s.mu.Unlock()

	for _, old := range replaced {
		s.stop(old)
	}
	s.status(view)
	go s.open(c, agent, Endpoint{URL: endpoint, Token: c.token})
	return view, nil
}

// open starts the agent's process and session.
func (s *Service) open(c *conv, agent Agent, tools Endpoint) {
	defer s.work.Done()
	ctx, cancel := context.WithTimeout(s.base, StartTimeout)
	defer cancel()
	session, err := s.runner.Start(ctx, agent, tools)
	s.mu.Lock()
	if c.Status != Starting { // closed while it started
		s.mu.Unlock()
		if err == nil {
			_ = session.Close()
		}
		return
	}
	if err != nil {
		s.log.Warn("tutor agent did not start", slog.String("agent", agent.ID), slog.Any("error", err))
		c.Status, c.Error = Failed, fmt.Sprintf("%s did not start: %v", agent.Name, err)
		c.timer.Stop()
	} else {
		c.session, c.Status = session, Ready
	}
	view := c.Conversation
	s.mu.Unlock()
	s.status(view)
}

// Send is the learner's next message; the answer streams as events. Refused unless the conversation is ready.
func (s *Service) Send(_ context.Context, user int64, id, text string) error {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > MaxMessage {
		return ErrEmptyMessage
	}
	s.mu.Lock()
	c, err := s.ownLocked(user, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if c.Status != Ready {
		s.mu.Unlock()
		return ErrNotReady
	}
	prompt := text
	if len(c.Messages) == 0 {
		prompt = Preamble(Attempt{Title: c.title}) + "\n\n---\n\nThe learner asks:\n\n" + text
	}
	c.Messages = append(c.Messages, Message{Role: Learner, Text: text, Tools: []ToolCall{}},
		Message{Role: Tutor, Tools: []ToolCall{}})
	c.Status = Answering
	s.touchLocked(c)
	session, view := c.session, c.Conversation
	s.work.Add(1)
	s.mu.Unlock()

	s.status(view)
	go s.answer(c, session, prompt)
	return nil
}

// answer runs one prompt to its end.
func (s *Service) answer(c *conv, session Session, prompt string) {
	defer s.work.Done()
	ctx, cancel := context.WithTimeout(s.base, AnswerTimeout)
	defer cancel()
	stop, err := session.Prompt(ctx, prompt, func(u Update) { s.update(c, u) })
	s.mu.Lock()
	if c.Status != Answering { // closed meanwhile
		s.mu.Unlock()
		return
	}
	e := Event{Conversation: c.ID, Type: "done"}
	var ended *conv
	if err != nil {
		// A broken answer is a broken agent (its process ended, or it stopped answering): the conversation ends
		msg := err.Error()
		e.Error = &msg
		ended = s.endLocked(c, Failed, "the agent stopped: "+msg)
	} else {
		e.Stop = &stop
		c.Status = Ready
		s.touchLocked(c)
	}
	view := c.Conversation
	s.mu.Unlock()
	s.send(c.User, e)
	if ended != nil {
		s.stop(ended) // says it failed
		return
	}
	s.status(view)
}

// update adds a piece of the answer to the conversation and tells the pages.
func (s *Service) update(c *conv, u Update) {
	s.mu.Lock()
	if c.Status != Answering || len(c.Messages) == 0 {
		s.mu.Unlock()
		return
	}
	last := &c.Messages[len(c.Messages)-1]
	e := Event{Conversation: c.ID}
	switch u.Kind {
	case UpdateText:
		if u.Text == "" {
			s.mu.Unlock()
			return
		}
		last.Text += u.Text
		e.Type, e.Text = "text", &u.Text
	case UpdateTool:
		call := u.Tool
		found := false
		for i := range last.Tools {
			if last.Tools[i].ID == call.ID {
				if call.Title == "" {
					call.Title = last.Tools[i].Title
				}
				if call.Status == "" {
					call.Status = last.Tools[i].Status
				}
				last.Tools[i], found = call, true
			}
		}
		if !found {
			last.Tools = append(last.Tools, call)
		}
		e.Type, e.Tool = "tool", &call
	}
	s.mu.Unlock()
	s.send(c.User, e)
}

// Cancel asks the agent to stop answering (the answer ends with stop "cancelled").
func (s *Service) Cancel(_ context.Context, user int64, id string) error {
	s.mu.Lock()
	c, err := s.ownLocked(user, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	session, answering := c.session, c.Status == Answering
	s.mu.Unlock()
	if answering {
		session.Cancel()
	}
	return nil
}

// Close ends a conversation and its agent.
func (s *Service) Close(_ context.Context, user int64, id string) error {
	s.mu.Lock()
	c, err := s.ownLocked(user, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	var ended *conv
	if c.Status != Failed && c.Status != Closed {
		ended = s.endLocked(c, Closed, "")
	}
	delete(s.convs, c.ID)
	s.mu.Unlock()
	if ended != nil {
		s.stop(ended)
	}
	return nil
}

// Get is one of user's conversations: open, or ended (failed, closed when idle) until it is closed or replaced.
func (s *Service) Get(_ context.Context, user int64, id string) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.ownLocked(user, id)
	if err != nil {
		return Conversation{}, err
	}
	return c.copy(), nil
}

// List is user's conversations (at most one).
func (s *Service) List(_ context.Context, user int64) []Conversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Conversation{}
	for _, c := range s.convs {
		if c.User == user {
			out = append(out, c.copy())
		}
	}
	return out
}

// Shutdown ends every conversation and waits for their agents' work to stop. None opens afterwards.
func (s *Service) Shutdown() {
	s.mu.Lock()
	s.closed = true
	var ended []*conv
	for _, c := range s.convs {
		if c.Status != Failed && c.Status != Closed {
			ended = append(ended, s.endLocked(c, Closed, ""))
		}
	}
	s.mu.Unlock()
	s.cancel()
	for _, c := range ended {
		s.stop(c)
	}
	s.work.Wait()
}

// The tools (served to the agent by internal/httpapi's MCP handler, each call with its conversation's token).

// Authorized says whether token is an open conversation's (the MCP server's bearer check).
func (s *Service) Authorized(token string) bool {
	_, _, err := s.scope(token)
	return err == nil
}

// LabAttempt is the conversation's attempt as the learner sees it now.
func (s *Service) LabAttempt(ctx context.Context, token string) (Attempt, error) {
	user, attempt, err := s.scope(token)
	if err != nil {
		return Attempt{}, err
	}
	return s.sources.Attempt(ctx, user, attempt)
}

// LabState is the lab's sites, links and nodes.
func (s *Service) LabState(ctx context.Context, token string) (lab.State, error) {
	if _, _, err := s.scope(token); err != nil {
		return lab.State{}, err
	}
	st, err := s.sources.State(ctx)
	st.Placeholders = nil // the platform's, for resolving texts
	return st, err
}

// TerminalOutput is the last n bytes (TerminalBytes when n <= 0, MaxTerminalBytes at most) of the attempt's
// terminal as plain text; "" with false when no terminal is open.
func (s *Service) TerminalOutput(ctx context.Context, token string, n int) (string, bool, error) {
	a, err := s.LabAttempt(ctx, token)
	if err != nil {
		return "", false, err
	}
	if n <= 0 {
		n = TerminalBytes
	}
	n = min(n, MaxTerminalBytes)
	out, ok := s.sources.Terminal(a.Workspace)
	if !ok || a.Workspace == "" {
		return "", false, nil
	}
	return string(Tail([]byte(PlainText(out)), n)), true, nil
}

func (s *Service) scope(token string) (user, attempt int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.convs {
		if c.token == token && token != "" && c.Status != Failed && c.Status != Closed {
			return c.User, c.Attempt, nil
		}
	}
	return 0, 0, ErrUnknownToken
}

func (s *Service) ownLocked(user int64, id string) (*conv, error) {
	c := s.convs[id]
	if c == nil || c.User != user {
		return nil, ErrConversationNotFound
	}
	return c, nil
}

// endLocked gives c its final status (it stays readable, its token opens nothing); the caller stops it (outside
// the lock), which says so to the pages.
func (s *Service) endLocked(c *conv, status Status, msg string) *conv {
	c.Status, c.Error = status, msg
	if c.timer != nil {
		c.timer.Stop()
	}
	return c
}

// stop closes an ended conversation's agent and tells the pages.
func (s *Service) stop(c *conv) {
	s.mu.Lock()
	session, view := c.session, c.Conversation
	c.session = nil
	s.mu.Unlock()
	if session != nil {
		if err := session.Close(); err != nil {
			s.log.Warn("closing a tutor agent", slog.Any("error", err))
		}
	}
	s.status(view)
}

// touchLocked restarts c's idle timer.
func (s *Service) touchLocked(c *conv) {
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(s.idle, func() {
		s.mu.Lock()
		if s.convs[c.ID] != c || c.Status != Ready && c.Status != Starting {
			s.mu.Unlock()
			return
		}
		ended := s.endLocked(c, Closed, "closed after "+s.idle.String()+" without a message")
		s.mu.Unlock()
		s.stop(ended)
	})
}

func (s *Service) status(c Conversation) {
	st := c.Status
	e := Event{Conversation: c.ID, Type: "status", Status: &st}
	if c.Error != "" {
		e.Error = &c.Error
	}
	s.send(c.User, e)
}

func (s *Service) send(user int64, e Event) {
	data, err := json.Marshal(e)
	if err != nil {
		s.log.Error("tutor event", slog.Any("error", err))
		return
	}
	s.sender.Send(user, platform.Message{Event: EventName, Data: data})
}

// copy is the conversation with its own messages (the service keeps changing c's).
func (c *conv) copy() Conversation {
	out := c.Conversation
	out.Messages = make([]Message, len(c.Messages))
	for i, m := range c.Messages {
		m.Tools = append([]ToolCall{}, m.Tools...)
		out.Messages[i] = m
	}
	return out
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
