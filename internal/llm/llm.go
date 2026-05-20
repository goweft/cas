// Package llm provides the multi-provider LLM bridge for CAS.
// Provider is selected via CAS_PROVIDER env var (ollama | anthropic | groq).
// Model routing maps workspace type → model name per provider.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Provider selects the inference backend.
type Provider string

const (
	ProviderOllama    Provider = "ollama"
	ProviderAnthropic Provider = "anthropic"
	ProviderGroq      Provider = "groq"
	ProviderOpenAI    Provider = "openai"
	ProviderOpenRouter Provider = "openrouter"
)

// ActiveProvider returns the provider from CAS_PROVIDER env (default: ollama).
func ActiveProvider() Provider {
	switch strings.ToLower(os.Getenv("CAS_PROVIDER")) {
	case "anthropic":
		return ProviderAnthropic
	case "groq":
		return ProviderGroq
	case "openai":
		return ProviderOpenAI
	case "openrouter":
		return ProviderOpenRouter
	default:
		return ProviderOllama
	}
}

// ProviderStatus describes a provider's configuration state.
type ProviderStatus struct {
	Provider Provider
	Active   bool
	KeySet   bool
	KeyEnv   string // env var name for the API key, empty for Ollama
}

// AllProviders returns the configuration status of every provider.
// Useful for --providers output and startup validation.
func AllProviders() []ProviderStatus {
	active := ActiveProvider()
	return []ProviderStatus{
		{Provider: ProviderOllama, Active: active == ProviderOllama, KeySet: true, KeyEnv: ""},
		{Provider: ProviderAnthropic, Active: active == ProviderAnthropic, KeySet: os.Getenv("ANTHROPIC_API_KEY") != "", KeyEnv: "ANTHROPIC_API_KEY"},
		{Provider: ProviderGroq, Active: active == ProviderGroq, KeySet: os.Getenv("GROQ_API_KEY") != "", KeyEnv: "GROQ_API_KEY"},
		{Provider: ProviderOpenAI, Active: active == ProviderOpenAI, KeySet: os.Getenv("OPENAI_API_KEY") != "", KeyEnv: "OPENAI_API_KEY"},
		{Provider: ProviderOpenRouter, Active: active == ProviderOpenRouter, KeySet: os.Getenv("OPENROUTER_API_KEY") != "", KeyEnv: "OPENROUTER_API_KEY"},
	}
}

// ValidateProvider checks whether the active provider has its required API key set.
// Returns nil for Ollama (no key needed) or if the key is present.
// Returns an error with the env var name if the key is missing.
func ValidateProvider() error {
	active := ActiveProvider()
	for _, ps := range AllProviders() {
		if ps.Provider == active {
			if ps.KeyEnv != "" && !ps.KeySet {
				return fmt.Errorf("provider %q requires %s to be set", string(active), ps.KeyEnv)
			}
			return nil
		}
	}
	return nil
}

// defaultModels maps provider → wsType → model name.
var defaultModels = map[Provider]map[string]string{
	ProviderOllama: {
		"document": "qwen3.5:9b",
		"list":     "qwen3.5:9b",
		"code":     "qwen2.5-coder:7b",
		"chat":     "qwen3.5:9b",
	},
	ProviderAnthropic: {
		"document": "claude-sonnet-4-6",
		"list":     "claude-sonnet-4-6",
		"code":     "claude-haiku-4-5-20251001",
		"chat":     "claude-sonnet-4-6",
	},
	ProviderGroq: {
		"document": "llama-3.3-70b-versatile",
		"list":     "llama-3.3-70b-versatile",
		"code":     "llama-3.3-70b-versatile",
		"chat":     "llama-3.3-70b-versatile",
	},
	ProviderOpenAI: {
		"document": "gpt-4o",
		"list":     "gpt-4o",
		"code":     "gpt-4o-mini",
		"chat":     "gpt-4o",
	},
	ProviderOpenRouter: {
		"document": "meta-llama/llama-3.3-70b-instruct",
		"list":     "meta-llama/llama-3.3-70b-instruct",
		"code":     "meta-llama/llama-3.3-70b-instruct",
		"chat":     "meta-llama/llama-3.3-70b-instruct",
	},
}

// ModelFor returns the model name for a workspace type and the active provider.
// CAS_MODEL_{TYPE} env var overrides the default.
func ModelFor(wsType string) string {
	envKey := "CAS_MODEL_" + strings.ToUpper(wsType)
	if override := os.Getenv(envKey); override != "" {
		return override
	}
	p := ActiveProvider()
	if m, ok := defaultModels[p][wsType]; ok {
		return m
	}
	return defaultModels[p]["document"]
}

// Message is a single turn in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete sends messages to the active provider and returns the full response.
func Complete(ctx context.Context, messages []Message, model string, temperature float64) (string, error) {
	switch ActiveProvider() {
	case ProviderAnthropic:
		return anthropicComplete(ctx, messages, model, temperature)
	case ProviderGroq:
		return groqComplete(ctx, messages, model, temperature)
	case ProviderOpenAI:
		return openaiComplete(ctx, messages, model, temperature)
	case ProviderOpenRouter:
		return openrouterComplete(ctx, messages, model, temperature)
	default:
		return ollamaComplete(ctx, messages, model, temperature)
	}
}

// Stream sends messages and calls onToken for each streamed token.
// Returns the full accumulated text when done.
func Stream(ctx context.Context, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	switch ActiveProvider() {
	case ProviderAnthropic:
		return anthropicStream(ctx, messages, model, temperature, onToken)
	case ProviderGroq:
		return groqStream(ctx, messages, model, temperature, onToken)
	case ProviderOpenAI:
		return openaiStream(ctx, messages, model, temperature, onToken)
	case ProviderOpenRouter:
		return openrouterStream(ctx, messages, model, temperature, onToken)
	default:
		return ollamaStream(ctx, messages, model, temperature, onToken)
	}
}

// ── Ollama ────────────────────────────────────────────────────────

func ollamaBase() string {
	if base := os.Getenv("OLLAMA_BASE_URL"); base != "" {
		return base
	}
	return "http://localhost:11434"
}

type ollamaRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	Options  struct {
		Temperature float64 `json:"temperature"`
	} `json:"options"`
}

type ollamaResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// stripThink removes <think>...</think> chain-of-thought blocks emitted by
// qwen3.x models before their actual response. Without stripping, the UI
// blocks for 60-120s while the model reasons internally.
func stripThink(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s, "</think>")
		if end == -1 {
			// unclosed think block — strip from start to end
			s = strings.TrimSpace(s[:start])
			break
		}
		s = s[:start] + s[end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}

func ollamaComplete(ctx context.Context, messages []Message, model string, temperature float64) (string, error) {
	req := ollamaRequest{Model: model, Messages: messages, Stream: false}
	req.Options.Temperature = temperature

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaBase()+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("ollama decode: %w", err)
	}
	return stripThink(result.Message.Content), nil
}

func ollamaStream(ctx context.Context, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	req := ollamaRequest{Model: model, Messages: messages, Stream: true}
	req.Options.Temperature = temperature

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		ollamaBase()+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("ollama stream: %w", err)
	}
	defer resp.Body.Close()

	var buf strings.Builder
	var inThink bool
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var chunk ollamaResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}
		token := chunk.Message.Content
		if token != "" {
			// Suppress <think>...</think> chain-of-thought tokens.
			// qwen3.x models emit these before the actual response.
			if strings.Contains(token, "<think>") {
				inThink = true
			}
			if !inThink {
				onToken(token)
				buf.WriteString(token)
			}
			if strings.Contains(token, "</think>") {
				inThink = false
			}
		}
		if chunk.Done {
			break
		}
	}
	return buf.String(), scanner.Err()
}

// ── Anthropic ────────────────────────────────────────────────────

const anthropicBase = "https://api.anthropic.com"
const anthropicVersion = "2023-06-01"

func anthropicKey() (string, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	return key, nil
}

type anthropicRequest struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	System      string    `json:"system,omitempty"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream,omitempty"`
}

// splitSystem separates a leading system message from user/assistant turns.
func splitSystem(messages []Message) (system string, rest []Message) {
	if len(messages) > 0 && messages[0].Role == "system" {
		return messages[0].Content, messages[1:]
	}
	return "", messages
}

func anthropicComplete(ctx context.Context, messages []Message, model string, temperature float64) (string, error) {
	key, err := anthropicKey()
	if err != nil {
		return "", err
	}

	system, rest := splitSystem(messages)
	req := anthropicRequest{
		Model:       model,
		MaxTokens:   4096,
		Temperature: temperature,
		System:      system,
		Messages:    rest,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		anthropicBase+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("anthropic decode: %w", err)
	}

	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func anthropicStream(ctx context.Context, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	key, err := anthropicKey()
	if err != nil {
		return "", err
	}

	system, rest := splitSystem(messages)
	req := anthropicRequest{
		Model:       model,
		MaxTokens:   4096,
		Temperature: temperature,
		System:      system,
		Messages:    rest,
		Stream:      true,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		anthropicBase+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropic stream: %w", err)
	}
	defer resp.Body.Close()

	var buf strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			token := event.Delta.Text
			if token != "" {
				onToken(token)
				buf.WriteString(token)
			}
		}
	}
	return buf.String(), scanner.Err()
}

// ── Groq ─────────────────────────────────────────────────────────

const groqBase = "https://api.groq.com/openai/v1"

func groqKey() (string, error) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		return "", fmt.Errorf("GROQ_API_KEY is not set")
	}
	return key, nil
}

func groqComplete(ctx context.Context, messages []Message, model string, temperature float64) (string, error) {
	key, err := groqKey()
	if err != nil {
		return "", err
	}
	return openaiCompatComplete(ctx, groqBase, key, "", messages, model, temperature)
}

func groqStream(ctx context.Context, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	key, err := groqKey()
	if err != nil {
		return "", err
	}
	return openaiCompatStream(ctx, groqBase, key, "", messages, model, temperature, onToken)
}


// ── OpenAI ───────────────────────────────────────────────────────

const openaiBase = "https://api.openai.com/v1"

func openaiKey() (string, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is not set")
	}
	return key, nil
}

func openaiComplete(ctx context.Context, messages []Message, model string, temperature float64) (string, error) {
	key, err := openaiKey()
	if err != nil {
		return "", err
	}
	return openaiCompatComplete(ctx, openaiBase, key, "", messages, model, temperature)
}

func openaiStream(ctx context.Context, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	key, err := openaiKey()
	if err != nil {
		return "", err
	}
	return openaiCompatStream(ctx, openaiBase, key, "", messages, model, temperature, onToken)
}

// ── OpenRouter ───────────────────────────────────────────────────

const openrouterBase = "https://openrouter.ai/api/v1"

func openrouterKey() (string, error) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return "", fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	return key, nil
}

func openrouterComplete(ctx context.Context, messages []Message, model string, temperature float64) (string, error) {
	key, err := openrouterKey()
	if err != nil {
		return "", err
	}
	return openaiCompatComplete(ctx, openrouterBase, key, "https://github.com/goweft/cas", messages, model, temperature)
}

func openrouterStream(ctx context.Context, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	key, err := openrouterKey()
	if err != nil {
		return "", err
	}
	return openaiCompatStream(ctx, openrouterBase, key, "https://github.com/goweft/cas", messages, model, temperature, onToken)
}

// ── OpenAI-compatible shared implementation ──────────────────────
//
// Both OpenAI and OpenRouter use the OpenAI chat completions API format.
// referer is sent as HTTP-Referer and X-Title for OpenRouter attribution;
// pass empty string for OpenAI.

type openaiCompatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	Stream      bool      `json:"stream,omitempty"`
}

type openaiCompatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func openaiCompatComplete(ctx context.Context, base, key, referer string, messages []Message, model string, temperature float64) (string, error) {
	req := openaiCompatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   4096,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)
	if referer != "" {
		httpReq.Header.Set("HTTP-Referer", referer)
		httpReq.Header.Set("X-Title", "CAS")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai-compat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai-compat: status %d: %s", resp.StatusCode, string(b))
	}

	var result openaiCompatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("openai-compat decode: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("openai-compat: empty choices")
	}
	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

func openaiCompatStream(ctx context.Context, base, key, referer string, messages []Message, model string, temperature float64, onToken func(string)) (string, error) {
	req := openaiCompatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   4096,
		Stream:      true,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)
	if referer != "" {
		httpReq.Header.Set("HTTP-Referer", referer)
		httpReq.Header.Set("X-Title", "CAS")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai-compat stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai-compat stream: status %d: %s", resp.StatusCode, string(b))
	}

	var buf strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			token := chunk.Choices[0].Delta.Content
			if token != "" {
				onToken(token)
				buf.WriteString(token)
			}
		}
	}
	return buf.String(), scanner.Err()
}

// ── Message builders ──────────────────────────────────────────────

// BuildWorkspaceMessages constructs the message slice for workspace generation.
func BuildWorkspaceMessages(system, title, userMessage string) []Message {
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: fmt.Sprintf("Title: %s\nRequest: %s", title, userMessage)},
	}
}

func BuildEditMessages(system, title, current, editRequest string) []Message {
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: fmt.Sprintf(
			"Title: %s\n\nCurrent content:\n%s\n\nChange request: %s",
			title, current, editRequest,
		)},
	}
}

func BuildChatMessages(system string, history []Message, userMessage string) []Message {
	msgs := make([]Message, 0, 1+len(history)+1)
	msgs = append(msgs, Message{Role: "system", Content: system})
	if len(history) > 6 {
		history = history[len(history)-6:]
	}
	msgs = append(msgs, history...)
	msgs = append(msgs, Message{Role: "user", Content: userMessage})
	return msgs
}

// BuildCombineMessages constructs the message slice for cross-workspace combine.
// Each workspace's content is included as a labeled section.
func BuildCombineMessages(system, userMessage string, workspaces []struct{ Title, Type, Content string }) []Message {
	var sections strings.Builder
	for i, ws := range workspaces {
		if i > 0 {
			sections.WriteString("\n\n---\n\n")
		}
		sections.WriteString(fmt.Sprintf("## Source %d: %s (%s)\n\n%s", i+1, ws.Title, ws.Type, ws.Content))
	}
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: fmt.Sprintf(
			"Source workspaces:\n\n%s\n\nRequest: %s",
			sections.String(), userMessage,
		)},
	}
}

// BuildEditWithContextMessages constructs an edit message with additional workspace context.
func BuildEditWithContextMessages(system, title, current, editRequest string, refs []struct{ Title, Content string }) []Message {
	var context strings.Builder
	for _, ref := range refs {
		context.WriteString(fmt.Sprintf("\n\n--- Referenced workspace: %s ---\n%s", ref.Title, ref.Content))
	}
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: fmt.Sprintf(
			"Title: %s\n\nCurrent content:\n%s%s\n\nChange request: %s",
			title, current, context.String(), editRequest,
		)},
	}
}

// ── System prompts ────────────────────────────────────────────────

var WorkspaceSystem = map[string]string{
	"document": "You are a document drafting assistant. " +
		"Produce a well-structured markdown document with appropriate headings, sections, and content. " +
		"Output only the document — no preamble, no explanation, no code fences.",
	"code": "You are a coding assistant. " +
		"Produce clean, well-commented code that fulfils the request. " +
		"Output ONLY the raw code — no markdown fences, no explanation. " +
		"Start directly with the code.",
	"list": "You are a list-making assistant. " +
		"Produce a clean, structured markdown list with a top-level heading. " +
		"Output only the list — no preamble, no explanation.",
}

var EditSystem = map[string]string{
	"document": "You are a precise document editor. " +
		"Apply the requested change and return the complete updated content in markdown. " +
		"Preserve all existing sections not affected by the change. Output only the updated document.",
	"code": "You are a precise code editor. " +
		"Apply the requested change and return the complete updated code. " +
		"Preserve all existing logic not affected by the change. Output only the updated code.",
	"list": "You are a precise list editor. " +
		"Apply the requested change and return the complete updated list in markdown. " +
		"Preserve all existing items not affected by the change. Output only the updated list.",
}

var CombineSystem = "You are a document synthesis assistant. " +
	"You are given the content of multiple workspaces. " +
	"Combine them into a single cohesive document with clear structure. " +
	"Preserve important content from each source. " +
	"Use markdown with appropriate headings. " +
	"Output only the combined document — no preamble, no explanation."

const ChatSystem = `You are CAS — a Conversational Agent Shell.
CAS creates and edits workspaces (documents, code, lists) from conversation.
Workspace operations are handled by the routing layer — you never simulate them.

RULES:
- Never say "workspace created" or "I've saved that" — only the routing layer does that.
- Never ask about names, types, or where to save. CAS handles all of that.
- Keep responses short. This is a shell, not a chatbot.
- Plain text only in chat replies — no markdown formatting.
- If the user wants a workspace, tell them: write a [document type].`

// SystemFor returns the system prompt for a given wsType, with optional user context appended.
// For Ollama + qwen3.x models, prepends /no_think to disable chain-of-thought
// generation entirely — eliminating the 60-120s silent thinking gap.
func SystemFor(prompts map[string]string, wsType, userContext string) string {
	base, ok := prompts[wsType]
	if !ok {
		base = prompts["document"]
	}
	if userContext != "" {
		base = base + "\n\nUser context: " + userContext
	}
	if needsNoThink(wsType) {
		return "/no_think\n" + base
	}
	return base
}

// needsNoThink returns true when the active provider+model combination
// emits <think> blocks that should be suppressed via /no_think directive.
// Currently targets Ollama + qwen3.x family.
func needsNoThink(wsType string) bool {
	if ActiveProvider() != ProviderOllama {
		return false
	}
	model := strings.ToLower(ModelFor(wsType))
	return strings.HasPrefix(model, "qwen3")
}

// ReadAll drains an io.Reader — useful in tests.
func ReadAll(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
