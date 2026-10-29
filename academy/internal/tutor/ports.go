package tutor

import (
	"context"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
)

// Runner starts agents (internal/tutor/acp: a process speaking ACP on its stdio).
type Runner interface {
	// Available says whether agent can run here; detail says why not, or how it is fetched.
	Available(agent Agent) (ok bool, detail string)
	// Start runs agent and opens its session, with Academy's tools reachable at tools.
	Start(ctx context.Context, agent Agent, tools Endpoint) (Session, error)
}

// Endpoint is Academy's MCP server for one conversation: its URL and the conversation's bearer token.
type Endpoint struct {
	URL   string
	Token string
}

// Session is a running agent's conversation.
type Session interface {
	// Prompt sends text and streams the answer to update until the agent ends its turn; the result is ACP's stop
	// reason (end_turn, cancelled, refusal, ...).
	Prompt(ctx context.Context, text string, update func(Update)) (string, error)
	// Cancel asks the agent to stop the current answer (Prompt then returns "cancelled").
	Cancel()
	// Close ends the agent's process.
	Close() error
}

// Sources are what the tools read, from the other modules (cmd/academy adapts them).
type Sources struct {
	// Attempt is one of user's lab attempts as the learner sees it, or ErrAttemptNotFound.
	Attempt func(ctx context.Context, user, attempt int64) (Attempt, error)
	// State is the lab's state (labstate.Service).
	State func(ctx context.Context) (lab.State, error)
	// Terminal is the recent output of the terminal opened in a workspace (terminal.Service); false when none is.
	Terminal func(workspace string) ([]byte, bool)
}

// Sender pushes a message to a user's open pages (platform.Hub).
type Sender interface {
	Send(user int64, m platform.Message)
}
