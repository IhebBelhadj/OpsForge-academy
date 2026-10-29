package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor"
)

// Tutors is what the transport needs from the tutor module: the conversations for the pages, the tools for the
// agents (by their conversation's token).
type Tutors interface {
	Agents() []tutor.Agent
	Start(ctx context.Context, user int64, agent string, attempt int64) (tutor.Conversation, error)
	Send(ctx context.Context, user int64, id, text string) error
	Cancel(ctx context.Context, user int64, id string) error
	Close(ctx context.Context, user int64, id string) error
	Get(ctx context.Context, user int64, id string) (tutor.Conversation, error)
	List(ctx context.Context, user int64) []tutor.Conversation
	Authorized(token string) bool
	LabAttempt(ctx context.Context, token string) (tutor.Attempt, error)
	LabState(ctx context.Context, token string) (lab.State, error)
	TerminalOutput(ctx context.Context, token string, n int) (string, bool, error)
}

// tutorProblem maps a tutor error to its problem document (status 0: not a domain error).
func tutorProblem(err error) Problem {
	switch {
	case errors.Is(err, tutor.ErrNoSuchAgent):
		return problem(http.StatusNotFound, "/problems/not-found", "Not found", "no such agent")
	case errors.Is(err, tutor.ErrAttemptNotFound):
		return problem(http.StatusNotFound, "/problems/not-found", "Not found", "no such attempt")
	case errors.Is(err, tutor.ErrConversationNotFound):
		return problem(http.StatusNotFound, "/problems/not-found", "Not found", "no such tutor conversation")
	case errors.Is(err, tutor.ErrAgentUnavailable):
		return problem(http.StatusConflict, "/problems/agent-unavailable", "The agent is not installed",
			"install it on the machine Academy runs on, then sign in to it once in a terminal")
	case errors.Is(err, tutor.ErrNotReady):
		return problem(http.StatusConflict, "/problems/tutor-not-ready", "The tutor is not ready",
			"it is starting or still answering")
	case errors.Is(err, tutor.ErrEmptyMessage):
		return problem(http.StatusUnprocessableEntity, "/problems/bad-message", "Bad message",
			"write something, at most 8 KiB")
	}
	return Problem{}
}

// ListTutorAgents is the agents the tutor knows and whether each is installed.
func (s *server) ListTutorAgents(context.Context, ListTutorAgentsRequestObject) (ListTutorAgentsResponseObject, error) {
	out := ListTutorAgents200JSONResponse{Agents: []TutorAgent{}}
	for _, a := range s.deps.Tutor.Agents() {
		var detail *string
		if a.Detail != "" {
			detail = &a.Detail
		}
		out.Agents = append(out.Agents, TutorAgent{Id: a.ID, Name: a.Name, Available: a.Available, Detail: detail})
	}
	return out, nil
}

// ListTutorConversations is the learner's open conversation, if any.
func (s *server) ListTutorConversations(ctx context.Context, _ ListTutorConversationsRequestObject) (
	ListTutorConversationsResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	out := ListTutorConversations200JSONResponse{Conversations: []TutorConversation{}}
	for _, c := range s.deps.Tutor.List(ctx, user) {
		out.Conversations = append(out.Conversations, tutorConversation(c))
	}
	return out, nil
}

// StartTutor opens a conversation; its agent starts in the background.
func (s *server) StartTutor(ctx context.Context, req StartTutorRequestObject) (StartTutorResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.deps.Tutor.Start(ctx, user, req.Body.Agent, req.Body.Attempt)
	if err != nil {
		switch p := tutorProblem(err); p.Status {
		case http.StatusNotFound:
			return StartTutor404ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return StartTutor409ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	return StartTutor201JSONResponse(tutorConversation(c)), nil
}

// GetTutorConversation is one of the learner's conversations.
func (s *server) GetTutorConversation(ctx context.Context, req GetTutorConversationRequestObject) (
	GetTutorConversationResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	c, err := s.deps.Tutor.Get(ctx, user, req.Conversation)
	if errors.Is(err, tutor.ErrConversationNotFound) {
		return GetTutorConversation404ApplicationProblemPlusJSONResponse(tutorProblem(err)), nil
	}
	if err != nil {
		return nil, err
	}
	return GetTutorConversation200JSONResponse(tutorConversation(c)), nil
}

// CloseTutor ends a conversation and its agent.
func (s *server) CloseTutor(ctx context.Context, req CloseTutorRequestObject) (CloseTutorResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	err = s.deps.Tutor.Close(ctx, user, req.Conversation)
	if errors.Is(err, tutor.ErrConversationNotFound) {
		return CloseTutor404ApplicationProblemPlusJSONResponse(tutorProblem(err)), nil
	}
	if err != nil {
		return nil, err
	}
	return CloseTutor204Response{}, nil
}

// SendTutorMessage asks the tutor; the answer streams as events.
func (s *server) SendTutorMessage(ctx context.Context, req SendTutorMessageRequestObject) (
	SendTutorMessageResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.deps.Tutor.Send(ctx, user, req.Conversation, req.Body.Text); err != nil {
		switch p := tutorProblem(err); p.Status {
		case http.StatusNotFound:
			return SendTutorMessage404ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusConflict:
			return SendTutorMessage409ApplicationProblemPlusJSONResponse(p), nil
		case http.StatusUnprocessableEntity:
			return SendTutorMessage422ApplicationProblemPlusJSONResponse(p), nil
		}
		return nil, err
	}
	c, err := s.deps.Tutor.Get(ctx, user, req.Conversation)
	if errors.Is(err, tutor.ErrConversationNotFound) { // ended at once (its agent died)
		return SendTutorMessage404ApplicationProblemPlusJSONResponse(tutorProblem(err)), nil
	}
	if err != nil {
		return nil, err
	}
	return SendTutorMessage202JSONResponse(tutorConversation(c)), nil
}

// CancelTutor asks the agent to stop its answer.
func (s *server) CancelTutor(ctx context.Context, req CancelTutorRequestObject) (CancelTutorResponseObject, error) {
	user, err := userID(ctx)
	if err != nil {
		return nil, err
	}
	err = s.deps.Tutor.Cancel(ctx, user, req.Conversation)
	if errors.Is(err, tutor.ErrConversationNotFound) {
		return CancelTutor404ApplicationProblemPlusJSONResponse(tutorProblem(err)), nil
	}
	if err != nil {
		return nil, err
	}
	return CancelTutor204Response{}, nil
}

func tutorConversation(c tutor.Conversation) TutorConversation {
	out := TutorConversation{Id: c.ID, Agent: c.Agent, Attempt: c.Attempt, Status: TutorStatus(c.Status),
		CreatedAt: c.CreatedAt, Messages: []TutorMessage{}}
	if c.Error != "" {
		out.Error = &c.Error
	}
	for _, m := range c.Messages {
		msg := TutorMessage{Role: TutorMessageRole(m.Role), Text: m.Text, Tools: []TutorToolCall{}}
		for _, t := range m.Tools {
			msg.Tools = append(msg.Tools, TutorToolCall{Id: t.ID, Title: t.Title, Status: t.Status})
		}
		out.Messages = append(out.Messages, msg)
	}
	return out
}

// The MCP server the agents call (POST /mcp, MCP's streamable HTTP, stateless): Academy's read-only tools, each
// request authorized by its conversation's token (Authorization: Bearer). Outside /api/: the agents have no
// session cookie; guard still refuses another site's page, hostGuard anything but loopback.

// mcpHandler serves the tools to an agent holding a conversation's token.
func mcpHandler(t Tutors) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return toolServer(t, bearer(r))
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !t.Authorized(bearer(r)) {
			writeProblem(w, http.StatusUnauthorized, "/problems/no-session", "No tutor conversation",
				"the token is no open conversation's")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return token
}

// terminalInput is terminal_output's argument.
type terminalInput struct {
	Bytes int `json:"bytes,omitempty" jsonschema:"how much of the most recent output to return, in bytes (default 8192, at most 65536)"`
}

// toolServer is the tools of one conversation (the token's).
func toolServer(t Tutors, token string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: tutor.ServerName, Title: "OpsForge Academy", Version: "1"},
		&mcp.ServerOptions{Instructions: "The learner's lab attempt as they see it in OpsForge Academy. Read-only."})
	mcp.AddTool(s, &mcp.Tool{Name: tutor.ToolLabAttempt, Title: "The learner's lab attempt",
		Description: "The lab the learner is doing: its steps (title, text), which are passed, the step to check next, " +
			"the hints the learner opened, and the attempt's Kubernetes namespace.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			a, err := t.LabAttempt(ctx, token)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(a)
		})
	mcp.AddTool(s, &mcp.Tool{Name: tutor.ToolLabState, Title: "The lab's state",
		Description: "The running lab: its sites (public address, VPC), the WAN links between them (configured and live " +
			"delay and loss, cut or changed), its nodes and their status.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			st, err := t.LabState(ctx, token)
			if err != nil {
				return nil, nil, err
			}
			return jsonResult(st)
		})
	mcp.AddTool(s, &mcp.Tool{Name: tutor.ToolTerminalOutput, Title: "The learner's terminal",
		Description: "The most recent output of the learner's terminal in the lab (commands and what they printed), as " +
			"plain text.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in terminalInput) (*mcp.CallToolResult, any, error) {
			out, ok, err := t.TerminalOutput(ctx, token, in.Bytes)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				out = "(the learner has no terminal open in this lab)"
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
		})
	return s
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	doc, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(doc)}}}, nil, nil
}
