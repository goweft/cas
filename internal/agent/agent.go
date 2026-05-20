// Package agent provides the named sub-agents that perform LLM work in CAS.
//
// Each agent has a single responsibility, a named contract, and owns one
// category of LLM call. The shell delegates to agents; agents never call
// each other.
//
// Agents:
//   - GenerationAgent  creates new workspace content from a prompt
//   - EditAgent        applies a change request to existing content
//
// IntentAgent is not here — intent detection is pure regex in internal/intent
// and needs no LLM call. It is covered by a contract at the shell boundary.
//
// Contract enforcement:
//   Every agent checks preconditions before calling the LLM and postconditions
//   after. Violations are returned as *contract.Violation and are always fatal
//   to the operation — no retry, no fallback, no LLM-assisted recovery.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/goweft/cas/internal/contract"
	"github.com/goweft/cas/internal/llm"
)

// GenerationRequest is the input to GenerationAgent.
type GenerationRequest struct {
	WSType      string // "document" | "code" | "list"
	Title       string
	Prompt      string
	UserContext string // from conductor; injected into system prompt
	Temperature float64
}

// GenerationResult is the output from GenerationAgent.
type GenerationResult struct {
	Content string
}

// GenerationAgent creates workspace content from a prompt.
// It owns all KindCreate LLM calls.
type GenerationAgent struct{}

// NewGenerationAgent returns a GenerationAgent.
func NewGenerationAgent() *GenerationAgent { return &GenerationAgent{} }

// Generate produces content synchronously.
func (a *GenerationAgent) Generate(ctx context.Context, req GenerationRequest) (*GenerationResult, error) {
	if err := a.contract(req, 0).CheckPreconditions(); err != nil {
		return nil, err
	}

	sys := llm.SystemFor(llm.WorkspaceSystem, req.WSType, req.UserContext)
	msgs := llm.BuildWorkspaceMessages(sys, req.Title, req.Prompt)
	content, err := llm.Complete(ctx, msgs, llm.ModelFor(req.WSType), req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("generation-agent: %w", err)
	}

	result := &GenerationResult{Content: content}
	if err := a.contract(req, len(content)).CheckPostconditions(); err != nil {
		return nil, err
	}
	return result, nil
}

// Stream produces content via streaming, calling onToken for each token.
func (a *GenerationAgent) Stream(ctx context.Context, req GenerationRequest, onToken func(string)) (*GenerationResult, error) {
	if err := a.contract(req, 0).CheckPreconditions(); err != nil {
		return nil, err
	}

	sys := llm.SystemFor(llm.WorkspaceSystem, req.WSType, req.UserContext)
	msgs := llm.BuildWorkspaceMessages(sys, req.Title, req.Prompt)
	content, err := llm.Stream(ctx, msgs, llm.ModelFor(req.WSType), req.Temperature, onToken)
	if err != nil {
		return nil, fmt.Errorf("generation-agent: %w", err)
	}

	result := &GenerationResult{Content: content}
	if err := a.contract(req, len(content)).CheckPostconditions(); err != nil {
		return nil, err
	}
	return result, nil
}

// contract builds and freezes the GenerationAgent contract for a given request.
// contentSize is 0 during precondition checks (content not yet produced).
func (a *GenerationAgent) contract(req GenerationRequest, contentSize int) *contract.Contract {
	const maxContentBytes = 512 * 1024

	c := contract.New("generation-agent")
	c.Preconditions = []contract.Rule{
		{
			Name:        "workspace_type_allowed",
			Description: "wsType must be document, code, or list",
			Check: func() bool {
				return req.WSType == "document" || req.WSType == "code" || req.WSType == "list"
			},
		},
		{
			Name:        "prompt_not_empty",
			Description: "prompt must not be empty",
			Check:       func() bool { return strings.TrimSpace(req.Prompt) != "" },
		},
		{
			Name:        "title_not_empty",
			Description: "title must not be empty",
			Check:       func() bool { return strings.TrimSpace(req.Title) != "" },
		},
	}
	c.Postconditions = []contract.Rule{
		{
			Name:        "content_not_empty",
			Description: "generated content must not be empty",
			Check:       func() bool { return contentSize > 0 },
		},
		{
			Name:        "content_size_within_limit",
			Description: fmt.Sprintf("content must not exceed %d KB", maxContentBytes/1024),
			Check:       func() bool { return contentSize <= maxContentBytes },
		},
	}
	return c.Freeze()
}

// ── EditAgent ─────────────────────────────────────────────────────

// EditRequest is the input to EditAgent.
type EditRequest struct {
	WSType         string // "document" | "code" | "list"
	Title          string
	CurrentContent string
	EditRequest    string
	UserContext    string
	Refs           []struct{ Title, Content string } // cross-workspace context
	Temperature    float64
}

// EditResult is the output from EditAgent.
type EditResult struct {
	Content string
}

// EditAgent applies a change request to existing workspace content.
// It owns all KindEdit LLM calls.
type EditAgent struct{}

// NewEditAgent returns an EditAgent.
func NewEditAgent() *EditAgent { return &EditAgent{} }

// Edit applies the change synchronously.
func (a *EditAgent) Edit(ctx context.Context, req EditRequest) (*EditResult, error) {
	if err := a.contract(req, 0).CheckPreconditions(); err != nil {
		return nil, err
	}

	sys := llm.SystemFor(llm.EditSystem, req.WSType, req.UserContext)
	var msgs []llm.Message
	if len(req.Refs) > 0 {
		msgs = llm.BuildEditWithContextMessages(sys, req.Title, req.CurrentContent, req.EditRequest, req.Refs)
	} else {
		msgs = llm.BuildEditMessages(sys, req.Title, req.CurrentContent, req.EditRequest)
	}

	content, err := llm.Complete(ctx, msgs, llm.ModelFor(req.WSType), req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("edit-agent: %w", err)
	}

	result := &EditResult{Content: content}
	if err := a.contract(req, len(content)).CheckPostconditions(); err != nil {
		return nil, err
	}
	return result, nil
}

// Stream applies the change via streaming, calling onToken for each token.
func (a *EditAgent) Stream(ctx context.Context, req EditRequest, onToken func(string)) (*EditResult, error) {
	if err := a.contract(req, 0).CheckPreconditions(); err != nil {
		return nil, err
	}

	sys := llm.SystemFor(llm.EditSystem, req.WSType, req.UserContext)
	var msgs []llm.Message
	if len(req.Refs) > 0 {
		msgs = llm.BuildEditWithContextMessages(sys, req.Title, req.CurrentContent, req.EditRequest, req.Refs)
	} else {
		msgs = llm.BuildEditMessages(sys, req.Title, req.CurrentContent, req.EditRequest)
	}

	content, err := llm.Stream(ctx, msgs, llm.ModelFor(req.WSType), req.Temperature, onToken)
	if err != nil {
		return nil, fmt.Errorf("edit-agent: %w", err)
	}

	result := &EditResult{Content: content}
	if err := a.contract(req, len(content)).CheckPostconditions(); err != nil {
		return nil, err
	}
	return result, nil
}

// contract builds and freezes the EditAgent contract for a given request.
func (a *EditAgent) contract(req EditRequest, contentSize int) *contract.Contract {
	const maxContentBytes = 512 * 1024
	// Postcondition: result must be at least 10% of original length
	// unless the original was very short (< 50 bytes), to catch accidental truncation.
	minExpected := len(req.CurrentContent) / 10
	if minExpected < 10 {
		minExpected = 10
	}

	c := contract.New("edit-agent")
	c.Preconditions = []contract.Rule{
		{
			Name:        "workspace_type_allowed",
			Description: "wsType must be document, code, or list",
			Check: func() bool {
				return req.WSType == "document" || req.WSType == "code" || req.WSType == "list"
			},
		},
		{
			Name:        "current_content_not_empty",
			Description: "cannot edit an empty workspace",
			Check:       func() bool { return strings.TrimSpace(req.CurrentContent) != "" },
		},
		{
			Name:        "edit_request_not_empty",
			Description: "edit request must not be empty",
			Check:       func() bool { return strings.TrimSpace(req.EditRequest) != "" },
		},
	}
	c.Postconditions = []contract.Rule{
		{
			Name:        "result_not_empty",
			Description: "edited content must not be empty",
			Check:       func() bool { return contentSize > 0 },
		},
		{
			Name:        "result_size_within_limit",
			Description: fmt.Sprintf("content must not exceed %d KB", maxContentBytes/1024),
			Check:       func() bool { return contentSize <= maxContentBytes },
		},
		{
			Name:        "result_not_drastically_shorter",
			Description: fmt.Sprintf("result must be at least %d bytes (10%% of original) to prevent accidental truncation", minExpected),
			Check:       func() bool { return contentSize >= minExpected },
		},
	}
	return c.Freeze()
}

// ── CombineAgent ─────────────────────────────────────────────────

// CombineRequest is the input to CombineAgent.
type CombineRequest struct {
	Sources     []struct{ Title, Type, Content string }
	Instruction string
	UserContext string
	Temperature float64
}

// CombineResult is the output from CombineAgent.
type CombineResult struct {
	Content string
}

// CombineAgent merges multiple workspace contents into one.
// It owns all KindCombine LLM calls.
type CombineAgent struct{}

// NewCombineAgent returns a CombineAgent.
func NewCombineAgent() *CombineAgent { return &CombineAgent{} }

// Combine merges sources synchronously.
func (a *CombineAgent) Combine(ctx context.Context, req CombineRequest) (*CombineResult, error) {
	if err := a.contract(req, 0).CheckPreconditions(); err != nil {
		return nil, err
	}

	sys := llm.CombineSystem
	if req.UserContext != "" {
		sys += "\n\nUser context: " + req.UserContext
	}
	msgs := llm.BuildCombineMessages(sys, req.Instruction, req.Sources)
	content, err := llm.Complete(ctx, msgs, llm.ModelFor("document"), req.Temperature)
	if err != nil {
		return nil, fmt.Errorf("combine-agent: %w", err)
	}

	result := &CombineResult{Content: content}
	if err := a.contract(req, len(content)).CheckPostconditions(); err != nil {
		return nil, err
	}
	return result, nil
}

// Stream merges sources via streaming.
func (a *CombineAgent) Stream(ctx context.Context, req CombineRequest, onToken func(string)) (*CombineResult, error) {
	if err := a.contract(req, 0).CheckPreconditions(); err != nil {
		return nil, err
	}

	sys := llm.CombineSystem
	if req.UserContext != "" {
		sys += "\n\nUser context: " + req.UserContext
	}
	msgs := llm.BuildCombineMessages(sys, req.Instruction, req.Sources)
	content, err := llm.Stream(ctx, msgs, llm.ModelFor("document"), req.Temperature, onToken)
	if err != nil {
		return nil, fmt.Errorf("combine-agent: %w", err)
	}

	result := &CombineResult{Content: content}
	if err := a.contract(req, len(content)).CheckPostconditions(); err != nil {
		return nil, err
	}
	return result, nil
}

// contract builds and freezes the CombineAgent contract.
func (a *CombineAgent) contract(req CombineRequest, contentSize int) *contract.Contract {
	const maxContentBytes = 512 * 1024

	c := contract.New("combine-agent")
	c.Preconditions = []contract.Rule{
		{
			Name:        "minimum_sources",
			Description: "combine requires at least 2 source workspaces",
			Check:       func() bool { return len(req.Sources) >= 2 },
		},
		{
			Name:        "sources_not_empty",
			Description: "all source workspaces must have content",
			Check: func() bool {
				for _, s := range req.Sources {
					if strings.TrimSpace(s.Content) == "" {
						return false
					}
				}
				return true
			},
		},
	}
	c.Postconditions = []contract.Rule{
		{
			Name:        "result_not_empty",
			Description: "combined content must not be empty",
			Check:       func() bool { return contentSize > 0 },
		},
		{
			Name:        "result_size_within_limit",
			Description: fmt.Sprintf("content must not exceed %d KB", maxContentBytes/1024),
			Check:       func() bool { return contentSize <= maxContentBytes },
		},
	}
	return c.Freeze()
}
