package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/httpapi"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor"
)

const (
	tutorAgentsOp = "/api/v1/tutor/agents"
	tutorConvsOp  = "/api/v1/tutor/conversations"
	tutorConvOp   = "/api/v1/tutor/conversations/{conversation}"
	tutorSendOp   = "/api/v1/tutor/conversations/{conversation}/messages"
	tutorCancelOp = "/api/v1/tutor/conversations/{conversation}/cancel"
	scriptedAgent = "scripted"
	notInstalled  = "gemini"
)

// scriptedRunner's agent answers like a real one would, through the MCP endpoint it was given (the last one is
// kept: an agent's token): "look" calls lab_attempt and terminal_output and answers with what they said; "slow"
// answers when cancelled; anything else is echoed.
type scriptedRunner struct {
	mu   sync.Mutex
	last tutor.Endpoint
}

func (*scriptedRunner) Available(a tutor.Agent) (bool, string) {
	if a.ID == scriptedAgent {
		return true, ""
	}
	return false, a.Command[0] + " is not installed (not on the PATH)"
}

func (r *scriptedRunner) Start(_ context.Context, _ tutor.Agent, tools tutor.Endpoint) (tutor.Session, error) {
	r.mu.Lock()
	r.last = tools
	r.mu.Unlock()
	return &scriptedSession{tools: tools, cancel: make(chan struct{}, 1)}, nil
}

type scriptedSession struct {
	tools  tutor.Endpoint
	cancel chan struct{}
}

func (s *scriptedSession) Prompt(ctx context.Context, text string, update func(tutor.Update)) (string, error) {
	switch {
	case strings.HasSuffix(text, "look"):
		cs, err := mcpClient(ctx, s.tools.URL, s.tools.Token)
		if err != nil {
			return "", err
		}
		defer func() { _ = cs.Close() }()
		update(tutor.Update{Kind: tutor.UpdateTool, Tool: tutor.ToolCall{ID: "t1", Title: "mcp__academy__lab_attempt",
			Status: "in_progress"}})
		attempt, err := callTool(ctx, cs, tutor.ToolLabAttempt, nil)
		if err != nil {
			return "", err
		}
		term, err := callTool(ctx, cs, tutor.ToolTerminalOutput, map[string]any{"bytes": 100})
		if err != nil {
			return "", err
		}
		update(tutor.Update{Kind: tutor.UpdateTool, Tool: tutor.ToolCall{ID: "t1", Status: "completed"}})
		update(tutor.Update{Kind: tutor.UpdateText, Text: "attempt: " + attempt + "\nterminal: " + term})
	case strings.HasSuffix(text, "slow"):
		select {
		case <-s.cancel:
			return "cancelled", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	default:
		update(tutor.Update{Kind: tutor.UpdateText, Text: "echo"})
	}
	return "end_turn", nil
}

func (s *scriptedSession) Cancel()      { s.cancel <- struct{}{} }
func (s *scriptedSession) Close() error { return nil }

// mcpClient connects to Academy's MCP server as an agent does, with the conversation's token.
func mcpClient(ctx context.Context, url, token string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearerTransport(token)}}, nil)
}

type bearerTransport string

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

func callTool(ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) (string, error) {
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		return "", errors.New(text)
	}
	return text, nil
}

// withTutor serves the world with a tutor on the scripted agent; the tools read the world's attempts (as
// cmd/academy adapts them) and a terminal that printed "$ ping c".
func withTutor(t *testing.T, w *world, svc **tutor.Service, runner *scriptedRunner) func(*httpapi.Deps) {
	t.Helper()
	sources := tutor.Sources{
		Attempt: func(ctx context.Context, user, id int64) (tutor.Attempt, error) {
			v, err := w.labs.View(ctx, user, id)
			if errors.Is(err, labsession.ErrAttemptNotFound) {
				return tutor.Attempt{}, tutor.ErrAttemptNotFound
			}
			if err != nil {
				return tutor.Attempt{}, err
			}
			e, err := w.catalog.Entry(ctx, catalog.KindLab, v.Lab)
			if err != nil {
				return tutor.Attempt{}, err
			}
			return tutor.Attempt{ID: v.ID, Lab: v.Lab, Title: e.Title, Status: string(v.Status), Current: v.Current,
				Namespace: v.Namespace, Workspace: v.Workspace, Steps: []tutor.Step{}}, nil
		},
		State: w.state.State,
		Terminal: func(string) ([]byte, bool) {
			return []byte("\x1b[1m$\x1b[0m ping c\r\n"), true
		},
	}
	agents := []tutor.Agent{{ID: scriptedAgent, Name: "Scripted", Command: []string{"scripted"}},
		{ID: notInstalled, Name: "Gemini CLI", Command: []string{"gemini", "--experimental-acp"},
			Detail: "signs in with your Google account"}}
	s := tutor.NewService(runner, agents, sources, w.hub, platform.SystemClock{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(s.Shutdown)
	*svc = s
	return func(d *httpapi.Deps) {
		w.deps(d)
		d.Tutor = s
	}
}

func tutorServer(t *testing.T) (*world, *browser, *tutor.Service, *scriptedRunner) {
	t.Helper()
	w := newWorld(t)
	var svc *tutor.Service
	runner := &scriptedRunner{}
	srv := apiServer(t, withTutor(t, w, &svc, runner))
	svc.SetEndpoint(srv.URL + "/mcp")
	return w, signIn(t, srv), svc, runner
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// awaitStatus reads the stream until the conversation has status st.
func awaitStatus(t *testing.T, s *stream, st string) {
	t.Helper()
	for {
		e := s.next(t)
		if e.event == "tutor" && strings.Contains(e.data, `"type":"status"`) && strings.Contains(e.data, `"status":"`+st+`"`) {
			return
		}
	}
}

// awaitDone reads the stream until an answer ends and returns its event's data.
func awaitDone(t *testing.T, s *stream) string {
	t.Helper()
	for {
		if e := s.next(t); e.event == "tutor" && strings.Contains(e.data, `"type":"done"`) {
			return e.data
		}
	}
}

func TestTutorAgents(t *testing.T) {
	_, b, _, _ := tutorServer(t)
	rec := get(t, b, "/api/v1/tutor/agents", tutorAgentsOp)
	list := decode[httpapi.TutorAgentList](t, rec.Body.Bytes())
	if rec.Code != 200 || len(list.Agents) != 2 || !list.Agents[0].Available || list.Agents[0].Detail != nil ||
		list.Agents[1].Available || *list.Agents[1].Detail != "gemini is not installed (not on the PATH)" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestATutorConversation(t *testing.T) {
	w, b, _, _ := tutorServer(t)
	events := openStream(t, b, "", nil)
	attempt := startLab(t, w, b, "meet-the-cluster").Attempt.Id

	rec := post(t, b, "/api/v1/tutor/conversations", tutorConvsOp, `{"agent":"scripted","attempt":`+itoa(attempt)+`}`)
	c := decode[httpapi.TutorConversation](t, rec.Body.Bytes())
	if rec.Code != 201 || c.Status != httpapi.TutorStatusStarting || c.Agent != scriptedAgent || c.Attempt != attempt ||
		len(c.Messages) != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	awaitStatus(t, events, "ready")
	path := "/api/v1/tutor/conversations/" + c.Id

	// The agent looks through Academy's tools and answers with what it saw
	rec = post(t, b, path+"/messages", tutorSendOp, `{"text":"look"}`)
	if c := decode[httpapi.TutorConversation](t, rec.Body.Bytes()); rec.Code != 202 ||
		c.Status != httpapi.TutorStatusAnswering || c.Messages[0].Text != "look" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if done := awaitDone(t, events); !strings.Contains(done, `"stop":"end_turn"`) {
		t.Fatal(done)
	}
	rec = get(t, b, path, tutorConvOp)
	got := decode[httpapi.TutorConversation](t, rec.Body.Bytes())
	if rec.Code != 200 || got.Status != httpapi.TutorStatusReady || len(got.Messages) != 2 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	answer := got.Messages[1]
	if answer.Role != httpapi.Tutor || !strings.Contains(answer.Text, `"lab": "meet-the-cluster"`) ||
		!strings.Contains(answer.Text, `"namespace": "sb-00000001"`) || strings.Contains(answer.Text, "workspace") ||
		!strings.HasSuffix(answer.Text, "terminal: $ ping c\n") ||
		len(answer.Tools) != 1 || answer.Tools[0].Status != "completed" {
		t.Fatalf("%+v", answer)
	}

	rec = get(t, b, "/api/v1/tutor/conversations", tutorConvsOp)
	if list := decode[httpapi.TutorConversationList](t, rec.Body.Bytes()); rec.Code != 200 ||
		len(list.Conversations) != 1 || list.Conversations[0].Id != c.Id {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// One answer at a time, and it can be stopped
	post(t, b, path+"/messages", tutorSendOp, `{"text":"slow"}`)
	rec = post(t, b, path+"/messages", tutorSendOp, `{"text":"and?"}`)
	if p := decode[httpapi.Problem](t, rec.Body.Bytes()); rec.Code != 409 || p.Type != "/problems/tutor-not-ready" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec = post(t, b, path+"/cancel", tutorCancelOp, ""); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if done := awaitDone(t, events); !strings.Contains(done, `"stop":"cancelled"`) {
		t.Fatal(done)
	}
	rec = post(t, b, path+"/messages", tutorSendOp, `{"text":"   "}`)
	if p := decode[httpapi.Problem](t, rec.Body.Bytes()); rec.Code != 422 || p.Type != "/problems/bad-message" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if rec = sendAs(t, b, http.MethodDelete, path, tutorConvOp, ""); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	awaitStatus(t, events, "closed")
	events.check(t) // the tutor events match the contract

	// Gone
	for _, rec := range []*httptest.ResponseRecorder{
		get(t, b, path, tutorConvOp),
		sendAs(t, b, http.MethodDelete, path, tutorConvOp, ""),
		post(t, b, path+"/messages", tutorSendOp, `{"text":"hi"}`),
		post(t, b, path+"/cancel", tutorCancelOp, ""),
	} {
		if p := decode[httpapi.Problem](t, rec.Body.Bytes()); rec.Code != 404 || p.Type != "/problems/not-found" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
}

func TestStartingATutorRefusals(t *testing.T) {
	w, b, _, _ := tutorServer(t)
	attempt := startLab(t, w, b, "meet-the-cluster").Attempt.Id
	for body, want := range map[string]string{
		`{"agent":"codex","attempt":` + itoa(attempt) + `}`:  "/problems/not-found",
		`{"agent":"scripted","attempt":999}`:                 "/problems/not-found",
		`{"agent":"gemini","attempt":` + itoa(attempt) + `}`: "/problems/agent-unavailable",
	} {
		rec := post(t, b, "/api/v1/tutor/conversations", tutorConvsOp, body)
		if p := decode[httpapi.Problem](t, rec.Body.Bytes()); p.Type != want {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestTheMCPEndpointNeedsAConversationsToken(t *testing.T) {
	w, b, _, runner := tutorServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := b.srv.URL + "/mcp"
	if _, err := mcpClient(ctx, url, "made-up"); err == nil {
		t.Fatal("an unknown token connected")
	}
	resp := b.send(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		http.Header{"Origin": nil, "X-Csrf-Token": nil})
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}

	// With one: the three read-only tools
	attempt := startLab(t, w, b, "meet-the-cluster").Attempt.Id
	events := openStream(t, b, "", nil)
	rec := post(t, b, "/api/v1/tutor/conversations", tutorConvsOp, `{"agent":"scripted","attempt":`+itoa(attempt)+`}`)
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	awaitStatus(t, events, "ready")
	runner.mu.Lock()
	token := runner.last.Token
	runner.mu.Unlock()
	cs, err := mcpClient(ctx, url, token)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not marked read-only", tool.Name)
		}
	}
	if strings.Join(names, " ") != "lab_attempt lab_state terminal_output" {
		t.Fatal(names)
	}
	st, err := callTool(ctx, cs, tutor.ToolLabState, nil)
	if err != nil || !strings.Contains(st, `"condition": "cut"`) || strings.Contains(st, "${node:c}") {
		t.Fatalf("%s %v", st, err)
	}

	// Another site's page is refused before the token is looked at
	resp = b.send(t, http.MethodPost, "/mcp", `{}`, http.Header{"Origin": {"http://evil.example"},
		"Authorization": {"Bearer " + token}})
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-site: %d", resp.StatusCode)
	}
}
