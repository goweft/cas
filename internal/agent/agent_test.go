package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/goweft/cas/internal/agent"
)

// ── GenerationAgent ───────────────────────────────────────────────

func TestGenerationAgentContractBadType(t *testing.T) {
	a := agent.NewGenerationAgent()
	_, err := a.Generate(context.Background(), agent.GenerationRequest{
		WSType: "spreadsheet", // invalid
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
		Prompt: "   ", // whitespace only
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
	// Just check that contract pre-checks pass for valid types.
	// We don't make a real LLM call — we expect a network/auth error, not a contract error.
	a := agent.NewGenerationAgent()
	for _, wsType := range []string{"document", "code", "list"} {
		_, err := a.Generate(context.Background(), agent.GenerationRequest{
			WSType: wsType,
			Title:  "Test",
			Prompt: "write something",
		})
		// Should not be a contract violation
		if err != nil && strings.Contains(err.Error(), "contract violation") {
			t.Errorf("wsType %q should pass preconditions, got contract violation: %v", wsType, err)
		}
	}
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
	a := agent.NewEditAgent()
	_, err := a.Edit(context.Background(), agent.EditRequest{
		WSType:         "code",
		Title:          "Test",
		CurrentContent: "package main\n\nfunc main() {}",
		EditRequest:    "add a print statement",
	})
	if err != nil && strings.Contains(err.Error(), "contract violation") {
		t.Errorf("valid request should pass preconditions, got: %v", err)
	}
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
			{Title: "Two", Type: "document", Content: "   "}, // empty
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
	a := agent.NewCombineAgent()
	_, err := a.Combine(context.Background(), agent.CombineRequest{
		Sources: []struct{ Title, Type, Content string }{
			{Title: "One", Type: "document", Content: "content one"},
			{Title: "Two", Type: "document", Content: "content two"},
		},
		Instruction: "combine them",
	})
	if err != nil && strings.Contains(err.Error(), "contract violation") {
		t.Errorf("valid request should pass preconditions, got: %v", err)
	}
}
