// Package tutor is the lab tutor: a conversation with a coding agent the learner already has (Claude Code, Codex,
// Gemini CLI, ...), started on this machine and spoken to over the Agent Client Protocol (ACP). The agent sees
// the learner's attempt only through Academy's own read-only tools (MCP: the attempt as the learner sees it, the
// lab's state, the terminal's recent output), and every other tool call it asks permission for is refused, so it
// explains and nudges but changes nothing. Conversations live in memory, one per learner: the agent's process
// ends when the conversation is closed, replaced, idle for too long or the platform stops.
package tutor

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// Errors of the service; the transport maps them to problems.
var (
	ErrNoSuchAgent          = errors.New("no such agent")
	ErrAgentUnavailable     = errors.New("the agent is not installed on this machine")
	ErrAttemptNotFound      = errors.New("no such attempt")
	ErrConversationNotFound = errors.New("no such conversation")
	ErrNotReady             = errors.New("the tutor is starting, answering, or has stopped")
	ErrEmptyMessage         = errors.New("the message is empty")
)

// ServerName is the name of Academy's MCP server as the agent sees it (its tools are "academy" tools).
const ServerName = "academy"

// TokenEnv carries a conversation's token to `academy mcp-proxy` (in its environment, not its arguments: those
// show in ps).
const TokenEnv = "ACADEMY_TUTOR_TOKEN"

// The tools the agent may call: Academy's own (internal/httpapi/mcp.go serves them), all read-only.
const (
	ToolLabAttempt     = "lab_attempt"
	ToolLabState       = "lab_state"
	ToolTerminalOutput = "terminal_output"
)

// Tools are the tool names Permit lets through.
var Tools = []string{ToolLabAttempt, ToolLabState, ToolTerminalOutput}

// Limits.
const (
	// MaxMessage bounds what the learner sends at once.
	MaxMessage = 8 << 10
	// TerminalBytes is how much recent terminal output terminal_output returns by default, MaxTerminalBytes at most
	// (the terminal keeps its last 64 KiB).
	TerminalBytes    = 8 << 10
	MaxTerminalBytes = 64 << 10
	// IdleTimeout ends a conversation nobody wrote to for this long (its agent holds memory).
	IdleTimeout = 30 * time.Minute
)

// Agent is a coding agent that speaks ACP on its stdio: Command is its argv (Command[0] is looked up on the PATH).
// Available and Detail are filled by the service from the runner (installed or not, and how it is fetched).
type Agent struct {
	ID        string
	Name      string
	Command   []string
	Available bool
	Detail    string
}

// Status is a conversation's: starting (the agent's process and its ACP session), ready, answering, or ended
// (closed, or failed with Error).
type Status string

// The statuses.
const (
	Starting  Status = "starting"
	Ready     Status = "ready"
	Answering Status = "answering"
	Failed    Status = "failed"
	Closed    Status = "closed"
)

// Role is who wrote a message.
type Role string

// The roles.
const (
	Learner Role = "learner"
	Tutor   Role = "tutor"
)

// ToolCall is a tool the agent called while answering. Status is ACP's (pending, in_progress, completed, failed)
// or "denied" when Permit refused it.
type ToolCall struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Denied is the status of a tool call Permit refused.
const Denied = "denied"

// Message is one turn of the conversation; a tutor message grows while the agent answers.
type Message struct {
	Role  Role       `json:"role"`
	Text  string     `json:"text"`
	Tools []ToolCall `json:"tools"`
}

// Conversation is one learner's talk with an agent about one lab attempt.
type Conversation struct {
	ID        string
	User      int64
	Attempt   int64
	Agent     string
	Status    Status
	Error     string
	Messages  []Message
	CreatedAt time.Time
}

// UpdateKind is what an agent's update carries.
type UpdateKind int

// The kinds of update.
const (
	UpdateText UpdateKind = iota // a piece of the answer
	UpdateTool                   // a tool call started or changed
)

// Update is one piece of an agent's answer as it streams.
type Update struct {
	Kind UpdateKind
	Text string
	Tool ToolCall
}

// Event is what the learner's pages are told (the SSE event `tutor`, the API's TutorEvent): type "status" (the
// conversation's new status, with error when it failed), "text" (a piece of the answer), "tool" (a tool call) or
// "done" (the answer ended: stop is ACP's stop reason, error when it broke off).
type Event struct {
	Conversation string    `json:"conversation"`
	Type         string    `json:"type"`
	Status       *Status   `json:"status"`
	Text         *string   `json:"text"`
	Tool         *ToolCall `json:"tool"`
	Stop         *string   `json:"stop"`
	Error        *string   `json:"error"`
}

// EventName is the SSE event type of tutor events.
const EventName = "tutor"

// Step is one step of the attempt as the learner sees it: its text (placeholders resolved), passed or not, and the
// hints they opened (never the others, nor the check's probe or a solution).
type Step struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Text   string   `json:"text"`
	Passed bool     `json:"passed"`
	Hints  []string `json:"hints_opened"`
}

// Attempt is what lab_attempt returns: the learner's attempt, no more than its page shows. Workspace (its
// sandbox) finds the terminal; it is not shown to the agent.
type Attempt struct {
	ID        int64  `json:"attempt"`
	Lab       string `json:"lab"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Current   string `json:"current_step"`
	Namespace string `json:"namespace"`
	Steps     []Step `json:"steps"`
	Workspace string `json:"-"`
}

// Permit says whether the agent may run a tool call it asked permission for: only Academy's read-only tools.
// Agents title an MCP tool call with the tool's name in some form ("mcp__academy__lab_state", "lab_state
// (academy MCP Server)"); anything else (a shell command, a file edit, a fetch) is refused.
func Permit(title string) bool {
	t := strings.ToLower(title)
	for _, name := range Tools {
		if strings.Contains(t, name) {
			return true
		}
	}
	return false
}

// ansi matches terminal control sequences: CSI (colours, cursor moves), OSC (titles, ended by BEL or ST),
// character set designations (ESC ( B) and two-byte escapes.
var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()*+].|\x1b[@-Z\\-_]`)

// PlainText is terminal output as text: control sequences removed, carriage returns resolved (a line redrawn
// keeps its last version), other control characters dropped.
func PlainText(out []byte) string {
	s := ansi.ReplaceAllString(strings.ToValidUTF8(string(out), ""), "")
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if j := strings.LastIndexByte(strings.TrimRight(line, "\r"), '\r'); j >= 0 {
			line = line[j+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' || r >= ' ' && r != 0x7f {
				return r
			}
			return -1
		}, line)
	}
	return strings.Join(lines, "\n")
}

// Tail is the last n bytes of out, starting on a line when it was cut.
func Tail(out []byte, n int) []byte {
	if len(out) <= n {
		return out
	}
	out = out[len(out)-n:]
	if i := strings.IndexByte(string(out), '\n'); i >= 0 {
		return out[i+1:]
	}
	return out
}

// Preamble opens the conversation: how to teach, sent before the learner's first message (ACP has no system
// prompt; every agent reads it as the start of the conversation).
func Preamble(attempt Attempt) string {
	return `You are the tutor of OpsForge Academy, a learning platform where the learner works in a real lab ` +
		`(Linux hosts, routers and a Kubernetes cluster spread over sites a, b and c, joined by a simulated WAN). ` +
		`They are doing the lab "` + attempt.Title + `" and opened you from it.

How to help:
- Look before you answer: the academy tools show what the learner sees. lab_attempt gives the steps, which are ` +
		`passed and the hints opened; terminal_output gives their terminal's recent output; lab_state the lab's ` +
		`sites, links and nodes.
- Teach, do not solve: explain why something happens, point at the evidence in their output, ask the question ` +
		`that leads to the next step. Give a full command or manifest only when they ask for it.
- Only the platform's checks decide whether a step passed; never say a step is done.
- You cannot run commands or change files here (those tools are refused); tell the learner what to run.
- Commands run on the lab host: "opsforge kubectl ..." (the attempt's namespace is the default), "opsforge ssh <node>".
- Answer in Markdown, short: a few sentences, a code block when it helps.`
}
