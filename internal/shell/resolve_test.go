package shell

import (
	"testing"
	"time"

	"github.com/goweft/cas/internal/workspace"
)

func ws(id, typ, title string) *workspace.Workspace {
	return &workspace.Workspace{
		ID: id, Type: typ, Title: title,
		Content: "content of " + title, CreatedAt: time.Now(),
	}
}

// ── resolveTarget ─────────────────────────────────────────────────

func TestResolveTargetSingleWorkspace(t *testing.T) {
	active := []*workspace.Workspace{ws("1", "document", "My Proposal")}
	got := resolveTarget("update it", active)
	if got.ID != "1" {
		t.Errorf("expected ws 1, got %q", got.ID)
	}
}

func TestResolveTargetByTitle(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Project Proposal"),
		ws("2", "code", "Data Parser"),
		ws("3", "list", "Todo List"),
	}

	got := resolveTarget("update the project proposal", active)
	if got.ID != "1" {
		t.Errorf("expected ws 1 (Project Proposal), got %q %q", got.ID, got.Title)
	}

	got = resolveTarget("fix the parser", active)
	if got.ID != "2" {
		t.Errorf("expected ws 2 (Data Parser), got %q %q", got.ID, got.Title)
	}

	got = resolveTarget("add items to the todo list", active)
	if got.ID != "3" {
		t.Errorf("expected ws 3 (Todo List), got %q %q", got.ID, got.Title)
	}
}

func TestResolveTargetFallsBackToMostRecent(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Proposal"),
		ws("2", "code", "Script"),
	}
	got := resolveTarget("add error handling", active)
	if got.ID != "2" {
		t.Errorf("expected most recent (ws 2), got %q", got.ID)
	}
}

func TestResolveTargetEmpty(t *testing.T) {
	got := resolveTarget("anything", nil)
	if got != nil {
		t.Error("expected nil for empty active list")
	}
}

func TestResolveTargetPartialTitleMatch(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Q3 Revenue Report"),
		ws("2", "document", "Meeting Notes"),
	}
	got := resolveTarget("update the revenue report", active)
	if got.ID != "1" {
		t.Errorf("expected ws 1 (Q3 Revenue Report), got %q %q", got.ID, got.Title)
	}
}

func TestResolveTargetCaseInsensitive(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "My Proposal"),
	}
	got := resolveTarget("UPDATE MY PROPOSAL", active)
	if got.ID != "1" {
		t.Errorf("expected ws 1, got %q", got.ID)
	}
}

// ── resolveAll ────────────────────────────────────────────────────

func TestResolveAllByTitles(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Project Proposal"),
		ws("2", "code", "Data Parser"),
		ws("3", "list", "Todo List"),
	}

	got := resolveAll("combine the proposal and the todo list", active)
	if len(got) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(got))
	}
	ids := map[string]bool{}
	for _, w := range got {
		ids[w.ID] = true
	}
	if !ids["1"] || !ids["3"] {
		t.Errorf("expected ws 1 and 3, got %v", ids)
	}
}

func TestResolveAllKeyword(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Proposal"),
		ws("2", "code", "Script"),
	}

	got := resolveAll("summarize all workspaces", active)
	if len(got) != 2 {
		t.Errorf("expected all 2, got %d", len(got))
	}
}

func TestResolveAllEverything(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "A"),
		ws("2", "document", "B"),
		ws("3", "document", "C"),
	}
	got := resolveAll("combine everything into one", active)
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
}

func TestResolveAllNoMatch(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Proposal"),
		ws("2", "code", "Script"),
	}
	// No title match and no "all" keyword → returns all (default behavior)
	got := resolveAll("merge these documents", active)
	if len(got) != 2 {
		t.Errorf("expected all 2 (default), got %d", len(got))
	}
}

func TestResolveAllEmpty(t *testing.T) {
	got := resolveAll("anything", nil)
	if got != nil {
		t.Error("expected nil for empty active list")
	}
}


// ── crossWorkspaceRefs ────────────────────────────────────────────

func TestCrossWorkspaceRefsFindsReferenced(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Project Proposal"),
		ws("2", "code", "Data Parser"),
		ws("3", "list", "Todo List"),
	}
	target := active[0]

	refs := crossWorkspaceRefs("add the parser code to the proposal", active, target)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].ID != "2" {
		t.Errorf("expected ws 2 (Data Parser), got %q", refs[0].ID)
	}
}

func TestCrossWorkspaceRefsNone(t *testing.T) {
	active := []*workspace.Workspace{
		ws("1", "document", "Proposal"),
		ws("2", "code", "Script"),
	}
	target := active[0]

	refs := crossWorkspaceRefs("add a budget section", active, target)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestCrossWorkspaceRefsSingleWorkspace(t *testing.T) {
	active := []*workspace.Workspace{ws("1", "document", "Proposal")}
	refs := crossWorkspaceRefs("anything", active, active[0])
	if len(refs) != 0 {
		t.Errorf("expected 0 refs with single workspace, got %d", len(refs))
	}
}
