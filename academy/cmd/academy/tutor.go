package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/catalog"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labsession"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/labstate"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/platform"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/terminal"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor"
	"github.com/IhebBelhadj/opsforge-academy/academy/internal/tutor/acp"
)

// tutorAgents are the ACP agents the tutor offers, in this order. Claude Code and Codex speak ACP through Zed's
// adapters (pinned: npx fetches them on first use); Gemini CLI speaks it itself. Each uses the learner's own
// sign-in to the agent.
var tutorAgents = []tutor.Agent{
	{ID: "claude", Name: "Claude Code", Command: []string{"npx", "-y", "@zed-industries/claude-agent-acp@0.23.1"},
		Detail: "Zed's ACP adapter, fetched by npx on first use; signs in as your Claude Code does"},
	{ID: "codex", Name: "Codex", Command: []string{"npx", "-y", "@zed-industries/codex-acp@0.16.0"},
		Detail: "Zed's ACP adapter, fetched by npx on first use; signs in as your Codex CLI does"},
	{ID: "gemini", Name: "Gemini CLI", Command: []string{"gemini", "--experimental-acp"},
		Detail: "signs in as your Gemini CLI does"},
}

// provideTutor runs the learners' tutor conversations with the agents installed on this machine; their tools
// reach Academy's MCP server over HTTP, or through this binary's mcp-proxy.
func provideTutor(courses *catalog.Service, labs *labsession.Service, state *labstate.Service,
	terminals *terminal.Service, hub *platform.Hub, clock platform.Clock, log *slog.Logger) (*tutor.Service, func(),
	error) {
	self, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("the tutor's MCP proxy: %w", err)
	}
	runner := &acp.Runner{Proxy: []string{self, "mcp-proxy"}, Log: log.With(slog.String("module", "tutor"))}
	svc := tutor.NewService(runner, tutorAgents, tutorSources(courses, labs, state, terminals), hub, clock, log)
	return svc, svc.Shutdown, nil
}

// tutorSources are what the tutor's tools read: the attempt as its page shows it (step texts resolved for the
// running lab, hints as opened), the lab's state, the attempt's terminal.
func tutorSources(courses *catalog.Service, labs *labsession.Service, state *labstate.Service,
	terminals *terminal.Service) tutor.Sources {
	return tutor.Sources{
		Attempt: func(ctx context.Context, user, id int64) (tutor.Attempt, error) {
			v, err := labs.View(ctx, user, id)
			if errors.Is(err, labsession.ErrAttemptNotFound) {
				return tutor.Attempt{}, tutor.ErrAttemptNotFound
			}
			if err != nil {
				return tutor.Attempt{}, err
			}
			out := tutor.Attempt{ID: v.ID, Lab: v.Lab, Title: v.Lab, Status: string(v.Status), Current: v.Current,
				Namespace: v.Namespace, Workspace: v.Workspace, Steps: []tutor.Step{}}
			e, err := courses.Entry(ctx, catalog.KindLab, v.Lab)
			if errors.Is(err, catalog.ErrNotFound) { // retired since
				return out, nil
			}
			if err != nil {
				return tutor.Attempt{}, err
			}
			l, err := catalog.LabView(e)
			if err != nil {
				return tutor.Attempt{}, err
			}
			out.Title = e.Title
			extra := map[string]string{}
			if v.Namespace != "" {
				extra["${ns}"] = v.Namespace
			}
			resolve := state.Resolver(ctx, extra)
			states := map[string]labsession.StepState{}
			for _, st := range v.Steps {
				states[st.ID] = st
			}
			for _, step := range l.Steps {
				st := states[step.ID]
				hints := make([]string, len(st.Hints))
				for i, h := range st.Hints {
					hints[i] = resolve(h)
				}
				out.Steps = append(out.Steps, tutor.Step{ID: step.ID, Title: step.Title, Text: resolve(step.Body),
					Passed: st.Passed, Hints: hints})
			}
			return out, nil
		},
		State:    state.State,
		Terminal: terminals.Recent,
	}
}

// mcpProxy is `academy mcp-proxy <url>`: an agent that reaches MCP servers only on stdio runs it to reach
// Academy's (streamable HTTP at url), with the conversation's token in $ACADEMY_TUTOR_TOKEN.
func mcpProxy(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: academy mcp-proxy <url>   (token in $"+tutor.TokenEnv+")")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := proxyMCP(ctx, &mcp.StdioTransport{}, args[0], os.Getenv(tutor.TokenEnv)); err != nil {
		fmt.Fprintf(os.Stderr, "academy: mcp-proxy: %v\n", err)
		return 1
	}
	return 0
}

// proxyMCP passes JSON-RPC messages between local (the agent) and the MCP server at url until either side ends.
func proxyMCP(ctx context.Context, local mcp.Transport, url, token string) error {
	remote, err := (&mcp.StreamableClientTransport{Endpoint: url, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearer(token)}}).Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = remote.Close() }()
	agent, err := local.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = agent.Close() }()
	done := make(chan error, 2)
	pass := func(from, to mcp.Connection) {
		for {
			msg, err := from.Read(ctx)
			if err == nil {
				err = to.Write(ctx, msg)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}
	go pass(agent, remote)
	go pass(remote, agent)
	err = <-done
	if errors.Is(err, io.EOF) || errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// bearer adds the conversation's token to every request.
type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}
