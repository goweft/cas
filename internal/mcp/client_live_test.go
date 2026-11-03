package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// These tests drive Connect/Call against a real mcp-go SSE server running
// in-process over httptest. Unlike the offline Connection{Tools: ...} fakes
// used by the agent and shell tests, this exercises the actual wire path:
// SSE handshake, initialize, tools/list, tools/call, and the Content
// decoding in Call. It is the test that catches an mcp-go upgrade that
// compiles but changes behaviour.

func liveServer(t *testing.T) string {
	t.Helper()
	srv := server.NewMCPServer("cas-live-test", "0.0.0")
	srv.AddTool(
		mcpgo.NewTool("echo",
			mcpgo.WithDescription("echoes the message back"),
			mcpgo.WithString("message", mcpgo.Required(), mcpgo.Description("text to echo")),
		),
		func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			msg, _ := req.GetArguments()["message"].(string)
			return mcpgo.NewToolResultText("echo: " + msg), nil
		},
	)
	srv.AddTool(
		mcpgo.NewTool("fail", mcpgo.WithDescription("always returns an error result")),
		func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultError("deliberate failure"), nil
		},
	)
	// Same as mcp-go's servertest.NewTestServer, inlined so this file builds
	// against both the 0.x and 1.x lines of the library.
	sse := server.NewSSEServer(srv)
	ts := httptest.NewServer(sse)
	server.WithBaseURL(ts.URL)(sse)
	t.Cleanup(ts.Close)
	return ts.URL + "/sse"
}

func TestLive_ConnectDiscoversTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect(ctx, liveServer(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	if conn.client == nil {
		t.Fatal("Connect returned a Connection with no live client")
	}
	if len(conn.Tools) != 2 {
		t.Fatalf("want 2 tools, got %d: %+v", len(conn.Tools), conn.Tools)
	}
	var echo *Tool
	for i := range conn.Tools {
		if conn.Tools[i].Name == "echo" {
			echo = &conn.Tools[i]
		}
	}
	if echo == nil {
		t.Fatalf("echo tool not discovered: %+v", conn.Tools)
	}
	if echo.Description != "echoes the message back" {
		t.Errorf("echo description = %q", echo.Description)
	}
	if _, ok := echo.InputSchema["message"]; !ok {
		t.Errorf("echo InputSchema missing 'message' property: %+v", echo.InputSchema)
	}
	if !strings.Contains(conn.ToolSummary(), "- echo: echoes the message back") {
		t.Errorf("ToolSummary = %q", conn.ToolSummary())
	}
}

func TestLive_CallReturnsTextContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect(ctx, liveServer(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	res, err := conn.Call(ctx, "echo", map[string]interface{}{"message": "hello over the wire"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.IsError {
		t.Fatalf("Call reported IsError; content=%q", res.Content)
	}
	// The exact assertion that an mcp-go content-type change would break:
	// if TextContent stopped decoding as a value type, Content is "" here.
	if res.Content != "echo: hello over the wire" {
		t.Fatalf("Content = %q, want %q", res.Content, "echo: hello over the wire")
	}
}

func TestLive_CallSurfacesToolError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect(ctx, liveServer(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	res, err := conn.Call(ctx, "fail", nil)
	if err != nil {
		t.Fatalf("Call: transport error for a tool-level error: %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError = false for a tool that returned an error result")
	}
	if !strings.Contains(res.Content, "deliberate failure") {
		t.Errorf("Content = %q, want the error text", res.Content)
	}
}

func TestLive_CallUnknownToolIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect(ctx, liveServer(t))
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	res, err := conn.Call(ctx, "no_such_tool", nil)
	if err == nil && (res == nil || !res.IsError) {
		t.Fatalf("unknown tool neither errored nor set IsError: res=%+v", res)
	}
}

func TestLive_ConnectRefusesDeadServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Nothing listens here; Connect must fail, not hang or return a half-open Connection.
	_, err := Connect(ctx, "http://127.0.0.1:1/sse")
	if err == nil {
		t.Fatal("Connect succeeded against a closed port")
	}
}
