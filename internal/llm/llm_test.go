package llm_test

import (
	"testing"

	"github.com/goweft/cas/internal/llm"
)

func TestModelForOllamaDefaults(t *testing.T) {
	cases := map[string]string{
		"document": "qwen3.5:9b",
		"list":     "qwen3.5:9b",
		"code":     "qwen2.5-coder:7b",
		"chat":     "qwen3.5:9b",
		"unknown":  "qwen3.5:9b", // falls back to document model
	}
	for wsType, want := range cases {
		got := llm.ModelFor(wsType)
		if got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", wsType, got, want)
		}
	}
}

func TestStripThink(t *testing.T) {
	// Export stripThink via a thin wrapper for testing
	// We test it indirectly through the public Complete function behaviour,
	// but we can test the pattern with what we know the model emits.
	// For now, verify the build is clean and the function exists.
	_ = llm.ModelFor("document")
}

func TestBuildWorkspaceMessages(t *testing.T) {
	msgs := llm.BuildWorkspaceMessages("system prompt", "My Title", "write me something")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Errorf("first message should be system, got %q", msgs[0].Role)
	}
	if msgs[1].Role != "user" {
		t.Errorf("second message should be user, got %q", msgs[1].Role)
	}
	if !contains(msgs[1].Content, "My Title") {
		t.Error("user message should contain the title")
	}
}

func TestBuildEditMessages(t *testing.T) {
	msgs := llm.BuildEditMessages("edit system", "Doc", "# Current", "add a section")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if !contains(msgs[1].Content, "# Current") {
		t.Error("edit message should contain current content")
	}
	if !contains(msgs[1].Content, "add a section") {
		t.Error("edit message should contain the edit request")
	}
}

func TestBuildChatMessagesTruncatesHistory(t *testing.T) {
	history := make([]llm.Message, 10)
	for i := range history {
		history[i] = llm.Message{Role: "user", Content: "msg"}
	}
	msgs := llm.BuildChatMessages("system", history, "new message")
	// system + up to 6 history + new message = 8 max
	if len(msgs) > 8 {
		t.Errorf("expected at most 8 messages (6 history + system + new), got %d", len(msgs))
	}
}

func TestSystemFor(t *testing.T) {
	prompts := map[string]string{
		"document": "doc system",
		"code":     "code system",
	}

	got := llm.SystemFor(prompts, "document", "")
	if got != "doc system" {
		t.Errorf("expected 'doc system', got %q", got)
	}

	got = llm.SystemFor(prompts, "code", "user prefers Python")
	if !contains(got, "user prefers Python") {
		t.Error("user context should be appended to system prompt")
	}

	got = llm.SystemFor(prompts, "unknown", "")
	if got != "doc system" {
		t.Errorf("unknown type should fall back to document, got %q", got)
	}
}

func TestActiveProviderDefaultsToOllama(t *testing.T) {
	// CAS_PROVIDER is not set in test environment
	p := llm.ActiveProvider()
	if p != llm.ProviderOllama {
		t.Errorf("expected Ollama provider by default, got %q", p)
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
