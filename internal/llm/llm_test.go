package llm_test

import (
	"os"
	"testing"

	"github.com/goweft/cas/internal/llm"
)

func TestModelForOllamaDefaults(t *testing.T) {
	os.Unsetenv("CAS_PROVIDER")
	os.Unsetenv("CAS_MODEL_DOCUMENT")
	os.Unsetenv("CAS_MODEL_CODE")
	os.Unsetenv("CAS_MODEL_LIST")
	os.Unsetenv("CAS_MODEL_CHAT")

	cases := map[string]string{
		"document": "qwen3.5:9b",
		"list":     "qwen3.5:9b",
		"code":     "qwen2.5-coder:7b",
		"chat":     "qwen3.5:9b",
		"unknown":  "qwen3.5:9b",
	}
	for wsType, want := range cases {
		got := llm.ModelFor(wsType)
		if got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", wsType, got, want)
		}
	}
}

func TestModelForAnthropicDefaults(t *testing.T) {
	os.Setenv("CAS_PROVIDER", "anthropic")
	defer os.Unsetenv("CAS_PROVIDER")

	cases := map[string]string{
		"document": "claude-sonnet-4-6",
		"list":     "claude-sonnet-4-6",
		"code":     "claude-haiku-4-5-20251001",
		"chat":     "claude-sonnet-4-6",
	}
	for wsType, want := range cases {
		got := llm.ModelFor(wsType)
		if got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", wsType, got, want)
		}
	}
}

func TestModelForGroqDefaults(t *testing.T) {
	os.Setenv("CAS_PROVIDER", "groq")
	defer os.Unsetenv("CAS_PROVIDER")

	cases := map[string]string{
		"document": "llama-3.3-70b-versatile",
		"list":     "llama-3.3-70b-versatile",
		"code":     "llama-3.3-70b-versatile",
		"chat":     "llama-3.3-70b-versatile",
		"unknown":  "llama-3.3-70b-versatile",
	}
	for wsType, want := range cases {
		got := llm.ModelFor(wsType)
		if got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", wsType, got, want)
		}
	}
}

func TestModelForOpenAIDefaults(t *testing.T) {
	os.Setenv("CAS_PROVIDER", "openai")
	defer os.Unsetenv("CAS_PROVIDER")

	cases := map[string]string{
		"document": "gpt-4o",
		"list":     "gpt-4o",
		"code":     "gpt-4o-mini",
		"chat":     "gpt-4o",
	}
	for wsType, want := range cases {
		got := llm.ModelFor(wsType)
		if got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", wsType, got, want)
		}
	}
}

func TestModelForOpenRouterDefaults(t *testing.T) {
	os.Setenv("CAS_PROVIDER", "openrouter")
	defer os.Unsetenv("CAS_PROVIDER")

	want := "meta-llama/llama-3.3-70b-instruct"
	for _, wsType := range []string{"document", "list", "code", "chat"} {
		got := llm.ModelFor(wsType)
		if got != want {
			t.Errorf("ModelFor(%q) = %q, want %q", wsType, got, want)
		}
	}
}

func TestModelForEnvOverride(t *testing.T) {
	os.Setenv("CAS_PROVIDER", "groq")
	os.Setenv("CAS_MODEL_CODE", "llama3-8b-8192")
	defer os.Unsetenv("CAS_PROVIDER")
	defer os.Unsetenv("CAS_MODEL_CODE")

	got := llm.ModelFor("code")
	if got != "llama3-8b-8192" {
		t.Errorf("ModelFor(code) = %q, want %q", got, "llama3-8b-8192")
	}
	got = llm.ModelFor("document")
	if got != "llama-3.3-70b-versatile" {
		t.Errorf("ModelFor(document) = %q, want %q", got, "llama-3.3-70b-versatile")
	}
}

func TestActiveProvider(t *testing.T) {
	cases := []struct {
		env  string
		want llm.Provider
	}{
		{"", llm.ProviderOllama},
		{"ollama", llm.ProviderOllama},
		{"OLLAMA", llm.ProviderOllama},
		{"anthropic", llm.ProviderAnthropic},
		{"ANTHROPIC", llm.ProviderAnthropic},
		{"groq", llm.ProviderGroq},
		{"GROQ", llm.ProviderGroq},
		{"openai", llm.ProviderOpenAI},
		{"OPENAI", llm.ProviderOpenAI},
		{"openrouter", llm.ProviderOpenRouter},
		{"OPENROUTER", llm.ProviderOpenRouter},
		{"unknown", llm.ProviderOllama},
	}
	for _, tc := range cases {
		if tc.env == "" {
			os.Unsetenv("CAS_PROVIDER")
		} else {
			os.Setenv("CAS_PROVIDER", tc.env)
		}
		got := llm.ActiveProvider()
		if got != tc.want {
			t.Errorf("ActiveProvider() with CAS_PROVIDER=%q = %q, want %q", tc.env, got, tc.want)
		}
	}
	os.Unsetenv("CAS_PROVIDER")
}

func TestValidateProviderOllamaNoKey(t *testing.T) {
	os.Unsetenv("CAS_PROVIDER")
	if err := llm.ValidateProvider(); err != nil {
		t.Errorf("ollama should not require a key, got error: %v", err)
	}
}

func TestValidateProviderMissingKey(t *testing.T) {
	cases := []struct {
		provider string
		keyEnv   string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"groq", "GROQ_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"openrouter", "OPENROUTER_API_KEY"},
	}
	for _, tc := range cases {
		os.Setenv("CAS_PROVIDER", tc.provider)
		os.Unsetenv(tc.keyEnv)
		err := llm.ValidateProvider()
		if err == nil {
			t.Errorf("provider %q with no key should return error", tc.provider)
		}
		os.Unsetenv("CAS_PROVIDER")
	}
}

func TestValidateProviderKeyPresent(t *testing.T) {
	os.Setenv("CAS_PROVIDER", "groq")
	os.Setenv("GROQ_API_KEY", "gsk_test")
	defer os.Unsetenv("CAS_PROVIDER")
	defer os.Unsetenv("GROQ_API_KEY")

	if err := llm.ValidateProvider(); err != nil {
		t.Errorf("groq with key set should not error, got: %v", err)
	}
}

func TestAllProvidersLength(t *testing.T) {
	statuses := llm.AllProviders()
	if len(statuses) != 5 {
		t.Errorf("expected 5 providers, got %d", len(statuses))
	}
}

func TestAllProvidersExactlyOneActive(t *testing.T) {
	for _, envVal := range []string{"", "ollama", "anthropic", "groq", "openai", "openrouter"} {
		if envVal == "" {
			os.Unsetenv("CAS_PROVIDER")
		} else {
			os.Setenv("CAS_PROVIDER", envVal)
		}
		statuses := llm.AllProviders()
		activeCount := 0
		for _, ps := range statuses {
			if ps.Active {
				activeCount++
			}
		}
		if activeCount != 1 {
			t.Errorf("CAS_PROVIDER=%q: expected exactly 1 active provider, got %d", envVal, activeCount)
		}
	}
	os.Unsetenv("CAS_PROVIDER")
}

func TestStripThink(t *testing.T) {
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
	if len(msgs) > 8 {
		t.Errorf("expected at most 8 messages (6 history + system + new), got %d", len(msgs))
	}
}

func TestSystemFor(t *testing.T) {
	os.Unsetenv("CAS_PROVIDER")

	prompts := map[string]string{
		"document": "doc system",
		"code":     "code system",
	}

	got := llm.SystemFor(prompts, "document", "")
	if !contains(got, "doc system") {
		t.Errorf("expected 'doc system' in result, got %q", got)
	}

	got = llm.SystemFor(prompts, "code", "user prefers Python")
	if !contains(got, "user prefers Python") {
		t.Error("user context should be appended to system prompt")
	}

	got = llm.SystemFor(prompts, "unknown", "")
	if !contains(got, "doc system") {
		t.Errorf("unknown type should fall back to document, got %q", got)
	}
}

func TestActiveProviderDefaultsToOllama(t *testing.T) {
	os.Unsetenv("CAS_PROVIDER")
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
