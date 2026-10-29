package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTheMCPProxyPassesTheAgentsCallsThrough(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "academy", Version: "1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "lab_state"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "up"}}}, nil, nil
		})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agentSide, proxySide := mcp.NewInMemoryTransports()
	proxied := make(chan error, 1)
	go func() { proxied <- proxyMCP(ctx, proxySide, srv.URL, "tok") }()

	agent := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	cs, err := agent.Connect(ctx, agentSide, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "lab_state"})
	if err != nil || res.IsError || res.Content[0].(*mcp.TextContent).Text != "up" {
		t.Fatalf("%+v %v", res, err)
	}
	// The agent leaving ends the proxy
	_ = cs.Close()
	select {
	case err := <-proxied:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the proxy outlived the agent")
	}
}

func TestTheMCPProxyWithAWrongToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agentSide, proxySide := mcp.NewInMemoryTransports()
	proxied := make(chan error, 1)
	go func() { proxied <- proxyMCP(ctx, proxySide, srv.URL, "wrong") }()
	agent := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	if _, err := agent.Connect(ctx, agentSide, nil); err == nil {
		t.Fatal("connected through a refused token")
	}
	if err := <-proxied; err == nil {
		t.Fatal("the proxy should say the server refused it")
	}
}
