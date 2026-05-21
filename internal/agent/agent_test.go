package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/goweft/cas/internal/agent"
	mcpclient "github.com/goweft/cas/internal/mcp"
	"github.com/goweft/cas/internal/webview"
	"github.com/goweft/cas/internal/llm"
)

// ── GenerationAgent ───────────────────────────────────────────────

func TestGenerationAgentContractBadType(t *testing.T) {
	a := agent.NewGenerationAgent()
	_, err := a.Generate(context.Background(), agent.GenerationRequest{
		WSType: "spreadsheet",
		Title:  "Test",
		Prompt: "write something",
	})
	if err == nil {
		t.Fatal("expected contract violation for invalid wsType, got nil")
	}
	if !strings.Contains(err.Error(), "workspace_type_allowed") {
		t.Errorf("expected workspace_type_allowed violation, got: %v", err)
	}
}

func TestGenerationAgentContractEmptyPrompt(t *testing.T) {
	a := agent.NewGenerationAgent()
	_, err := a.Generate(context.Background(), agent.GenerationRequest{
		WSType: "document",
		Title:  "Test",
		Prompt: "   ",
	})
	if err == nil {
		t.Fatal("expected contract violation for empty prompt, got nil")
	}
	if !strings.Contains(err.Error(), "prompt_not_empty") {
		t.Errorf("expected prompt_not_empty violation, got: %v", err)
	}
}

func TestGenerationAgentContractEmptyTitle(t *testing.T) {
	a := agent.NewGenerationAgent()
	_, err := a.Generate(context.Background(), agent.GenerationRequest{
		WSType: "document",
		Title:  "",
		Prompt: "write something",
	})
	if err == nil {
		t.Fatal("expected contract violation for empty title, got nil")
	}
	if !strings.Contains(err.Error(), "title_not_empty") {
		t.Errorf("expected title_not_empty violation, got: %v", err)
	}
}

func TestGenerationAgentValidTypes(t *testing.T) {
	t.Skip("skipped: requires live LLM endpoint")
}

// ── EditAgent ─────────────────────────────────────────────────────

func TestEditAgentContractEmptyContent(t *testing.T) {
	a := agent.NewEditAgent()
	_, err := a.Edit(context.Background(), agent.EditRequest{
		WSType:         "document",
		Title:          "Test",
		CurrentContent: "  ",
		EditRequest:    "add a section",
	})
	if err == nil {
		t.Fatal("expected contract violation for empty content, got nil")
	}
	if !strings.Contains(err.Error(), "current_content_not_empty") {
		t.Errorf("expected current_content_not_empty violation, got: %v", err)
	}
}

func TestEditAgentContractEmptyRequest(t *testing.T) {
	a := agent.NewEditAgent()
	_, err := a.Edit(context.Background(), agent.EditRequest{
		WSType:         "document",
		Title:          "Test",
		CurrentContent: "# Hello\n\nSome content.",
		EditRequest:    "",
	})
	if err == nil {
		t.Fatal("expected contract violation for empty edit request, got nil")
	}
	if !strings.Contains(err.Error(), "edit_request_not_empty") {
		t.Errorf("expected edit_request_not_empty violation, got: %v", err)
	}
}

func TestEditAgentContractBadType(t *testing.T) {
	a := agent.NewEditAgent()
	_, err := a.Edit(context.Background(), agent.EditRequest{
		WSType:         "pdf",
		Title:          "Test",
		CurrentContent: "some content",
		EditRequest:    "change it",
	})
	if err == nil {
		t.Fatal("expected contract violation for invalid wsType, got nil")
	}
	if !strings.Contains(err.Error(), "workspace_type_allowed") {
		t.Errorf("expected workspace_type_allowed violation, got: %v", err)
	}
}

func TestEditAgentValidPreconditions(t *testing.T) {
	t.Skip("skipped: requires live LLM endpoint")
}

// ── CombineAgent ─────────────────────────────────────────────────

func TestCombineAgentContractTooFewSources(t *testing.T) {
	a := agent.NewCombineAgent()
	_, err := a.Combine(context.Background(), agent.CombineRequest{
		Sources: []struct{ Title, Type, Content string }{
			{Title: "One", Type: "document", Content: "some content"},
		},
		Instruction: "combine them",
	})
	if err == nil {
		t.Fatal("expected contract violation for < 2 sources, got nil")
	}
	if !strings.Contains(err.Error(), "minimum_sources") {
		t.Errorf("expected minimum_sources violation, got: %v", err)
	}
}

func TestCombineAgentContractEmptySource(t *testing.T) {
	a := agent.NewCombineAgent()
	_, err := a.Combine(context.Background(), agent.CombineRequest{
		Sources: []struct{ Title, Type, Content string }{
			{Title: "One", Type: "document", Content: "some content"},
			{Title: "Two", Type: "document", Content: "   "},
		},
		Instruction: "combine them",
	})
	if err == nil {
		t.Fatal("expected contract violation for empty source, got nil")
	}
	if !strings.Contains(err.Error(), "sources_not_empty") {
		t.Errorf("expected sources_not_empty violation, got: %v", err)
	}
}

func TestCombineAgentValidPreconditions(t *testing.T) {
	t.Skip("skipped: requires live LLM endpoint")
}

// ── ChatAgent ─────────────────────────────────────────────────────

func TestChatAgentContractEmptyMessage(t *testing.T) {
	a := agent.NewChatAgent()
	_, err := a.Chat(context.Background(), agent.ChatRequest{
		Message: "   ",
	})
	if err == nil {
		t.Fatal("expected contract violation for empty message, got nil")
	}
	if !strings.Contains(err.Error(), "message_not_empty") {
		t.Errorf("expected message_not_empty violation, got: %v", err)
	}
}

func TestChatAgentContractExcessiveHistory(t *testing.T) {
	a := agent.NewChatAgent()
	history := make([]llm.Message, 21)
	for i := range history {
		history[i] = llm.Message{Role: "user", Content: "msg"}
	}
	_, err := a.Chat(context.Background(), agent.ChatRequest{
		Message: "hello",
		History: history,
	})
	if err == nil {
		t.Fatal("expected contract violation for excessive history, got nil")
	}
	if !strings.Contains(err.Error(), "history_not_excessive") {
		t.Errorf("expected history_not_excessive violation, got: %v", err)
	}
}

func TestChatAgentValidPreconditions(t *testing.T) {
	t.Skip("skipped: requires live LLM endpoint")
}

// ── MCPAgent ──────────────────────────────────────────────────────

func TestMCPAgentContractEmptyInstruction(t *testing.T) {
	a := agent.NewMCPAgent()
	conn := &mcpclient.Connection{
		ServerURL: "http://localhost:3000",
		Tools:     []mcpclient.Tool{{Name: "ping", Description: "ping the server"}},
	}
	_, err := a.Act(context.Background(), agent.MCPRequest{
		Instruction: "  ",
		Connection:  conn,
		Autonomy:    agent.AutonomySuggest,
	})
	if err == nil {
		t.Fatal("expected contract violation for empty instruction, got nil")
	}
	if !strings.Contains(err.Error(), "instruction_not_empty") {
		t.Errorf("expected instruction_not_empty violation, got: %v", err)
	}
}

func TestMCPAgentContractNilConnection(t *testing.T) {
	a := agent.NewMCPAgent()
	_, err := a.Act(context.Background(), agent.MCPRequest{
		Instruction: "list issues",
		Connection:  nil,
		Autonomy:    agent.AutonomySuggest,
	})
	if err == nil {
		t.Fatal("expected contract violation for nil connection, got nil")
	}
	if !strings.Contains(err.Error(), "connection_present") {
		t.Errorf("expected connection_present violation, got: %v", err)
	}
}

func TestMCPAgentContractNoTools(t *testing.T) {
	a := agent.NewMCPAgent()
	conn := &mcpclient.Connection{
		ServerURL: "http://localhost:3000",
		Tools:     []mcpclient.Tool{},
	}
	_, err := a.Act(context.Background(), agent.MCPRequest{
		Instruction: "do something",
		Connection:  conn,
		Autonomy:    agent.AutonomySuggest,
	})
	if err == nil {
		t.Fatal("expected contract violation for no tools, got nil")
	}
	if !strings.Contains(err.Error(), "connection_has_tools") {
		t.Errorf("expected connection_has_tools violation, got: %v", err)
	}
}

func TestMCPAgentContractInvalidAutonomy(t *testing.T) {
	a := agent.NewMCPAgent()
	conn := &mcpclient.Connection{
		ServerURL: "http://localhost:3000",
		Tools:     []mcpclient.Tool{{Name: "ping", Description: "ping"}},
	}
	_, err := a.Act(context.Background(), agent.MCPRequest{
		Instruction: "do something",
		Connection:  conn,
		Autonomy:    agent.Autonomy("full-auto"),
	})
	if err == nil {
		t.Fatal("expected contract violation for invalid autonomy, got nil")
	}
	if !strings.Contains(err.Error(), "autonomy_valid") {
		t.Errorf("expected autonomy_valid violation, got: %v", err)
	}
}

func TestMCPAgentAutonomyConstants(t *testing.T) {
	if agent.AutonomySuggest == "" || agent.AutonomyConfirm == "" || agent.AutonomyRun == "" {
		t.Error("autonomy constants must not be empty")
	}
	if agent.AutonomySuggest == agent.AutonomyConfirm ||
		agent.AutonomyConfirm == agent.AutonomyRun ||
		agent.AutonomySuggest == agent.AutonomyRun {
		t.Error("autonomy constants must be distinct")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ── WebAgent ──────────────────────────────────────────────────────

func TestWebAgentContractEmptyInstruction(t *testing.T) {
	a := agent.NewWebAgent()
	sess, _ := webview.NewSession(nil, "https://example.com")
	page := &webview.PageState{URL: "https://example.com", Title: "Test"}
	_, err := a.Act(context.Background(), agent.WebRequest{
		Instruction: "  ",
		Session:     sess,
		PageState:   page,
		Autonomy:    agent.AutonomySuggest,
	})
	if err == nil {
		t.Fatal("expected contract violation for empty instruction, got nil")
	}
	if !strings.Contains(err.Error(), "instruction_not_empty") {
		t.Errorf("expected instruction_not_empty violation, got: %v", err)
	}
}

func TestWebAgentContractNilSession(t *testing.T) {
	a := agent.NewWebAgent()
	page := &webview.PageState{URL: "https://example.com"}
	_, err := a.Act(context.Background(), agent.WebRequest{
		Instruction: "summarise this page",
		Session:     nil,
		PageState:   page,
		Autonomy:    agent.AutonomySuggest,
	})
	if err == nil {
		t.Fatal("expected contract violation for nil session, got nil")
	}
	if !strings.Contains(err.Error(), "session_present") {
		t.Errorf("expected session_present violation, got: %v", err)
	}
}

func TestWebAgentContractNilPageState(t *testing.T) {
	a := agent.NewWebAgent()
	sess, _ := webview.NewSession(nil, "https://example.com")
	_, err := a.Act(context.Background(), agent.WebRequest{
		Instruction: "summarise this page",
		Session:     sess,
		PageState:   nil,
		Autonomy:    agent.AutonomySuggest,
	})
	if err == nil {
		t.Fatal("expected contract violation for nil page state, got nil")
	}
	if !strings.Contains(err.Error(), "page_state_present") {
		t.Errorf("expected page_state_present violation, got: %v", err)
	}
}

func TestWebAgentContractInvalidAutonomy(t *testing.T) {
	a := agent.NewWebAgent()
	sess, _ := webview.NewSession(nil, "https://example.com")
	page := &webview.PageState{URL: "https://example.com"}
	_, err := a.Act(context.Background(), agent.WebRequest{
		Instruction: "summarise",
		Session:     sess,
		PageState:   page,
		Autonomy:    agent.Autonomy("turbo"),
	})
	if err == nil {
		t.Fatal("expected contract violation for invalid autonomy, got nil")
	}
	if !strings.Contains(err.Error(), "autonomy_valid") {
		t.Errorf("expected autonomy_valid violation, got: %v", err)
	}
}
