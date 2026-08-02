package plugin

// examples_test.go loads and executes the shipped example plugins in
// examples/plugins/, so the examples are verified by CI on every push.
// If the plugin API changes, these tests break loudly instead of letting
// the examples rot.

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Fixture workspaces. notesContent is crafted so expected numbers are
// hand-checkable: 11 editor lines, 17 whitespace-separated words, a TODO
// on line 11, and a fenced "# not a heading" that toc must skip.
const notesContent = "# Overview\n\nWeekly notes.\n\n```lua\n# not a heading\n```\n\n## Details\n\n- TODO: write the intro\n"

const mainGoContent = "package main\n\n// FIXME: handle the error path\n"

func exampleDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../../examples/plugins")
	if err != nil {
		t.Fatalf("resolve examples dir: %v", err)
	}
	return dir
}

func loadExamples(t *testing.T) *Registry {
	t.Helper()
	r := New(exampleDir(t))
	if err := r.Load(); err != nil {
		t.Fatalf("load examples: %v", err)
	}
	if errs := r.Errors(); len(errs) > 0 {
		t.Fatalf("example plugins failed to load: %v", errs)
	}
	return r
}

func exampleContext() *Context {
	return &Context{
		Workspaces: []WorkspaceInfo{
			{ID: "ws1", Type: "file", Title: "main.go", Content: mainGoContent},
			{ID: "ws2", Type: "file", Title: "notes", Content: notesContent}, // active
		},
	}
}

func runExample(t *testing.T, r *Registry, message string, ctx *Context) string {
	t.Helper()
	cmd, ok := r.Match(message)
	if !ok {
		t.Fatalf("no example command matched %q", message)
	}
	reply, err := r.Execute(cmd, ctx)
	if err != nil {
		t.Fatalf("execute %q: %v", message, err)
	}
	return reply
}

func TestExamplesRegisterAllCommands(t *testing.T) {
	r := loadExamples(t)
	defer r.Close()

	for _, name := range []string{"standup", "wordcount", "wc", "toc", "todos"} {
		if _, ok := r.Match(name); !ok {
			t.Errorf("command %q not registered by examples", name)
		}
	}
}

func TestExampleStandup(t *testing.T) {
	r := loadExamples(t)
	defer r.Close()

	reply := runExample(t, r, "standup", exampleContext())
	for _, want := range []string{"## Standup", "- main.go (file)", "- notes (file)"} {
		if !strings.Contains(reply, want) {
			t.Errorf("standup reply missing %q:\n%s", want, reply)
		}
	}

	empty := runExample(t, r, "standup", &Context{})
	if !strings.Contains(empty, "No workspaces") {
		t.Errorf("standup with no workspaces = %q, want empty-state message", empty)
	}
}

func TestExampleWordcount(t *testing.T) {
	r := loadExamples(t)
	defer r.Close()

	reply := runExample(t, r, "wordcount", exampleContext())
	for _, want := range []string{
		"**notes**",
		"11 lines",
		"17 words",
		fmt.Sprintf("%d characters", len(notesContent)),
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("wordcount reply missing %q:\n%s", want, reply)
		}
	}

	// Alias must run the same handler.
	alias := runExample(t, r, "wc", exampleContext())
	if alias != reply {
		t.Errorf("wc alias reply differs from wordcount:\n%q\nvs\n%q", alias, reply)
	}

	empty := runExample(t, r, "wordcount", &Context{})
	if !strings.Contains(empty, "No active workspace") {
		t.Errorf("wordcount with no workspaces = %q, want empty-state message", empty)
	}
}

func TestExampleToc(t *testing.T) {
	r := loadExamples(t)
	defer r.Close()

	reply := runExample(t, r, "toc", exampleContext())
	for _, want := range []string{"## Contents — notes", "- Overview", "  - Details"} {
		if !strings.Contains(reply, want) {
			t.Errorf("toc reply missing %q:\n%s", want, reply)
		}
	}
	if strings.Contains(reply, "not a heading") {
		t.Errorf("toc must skip headings inside code fences:\n%s", reply)
	}
}

func TestExampleTodos(t *testing.T) {
	r := loadExamples(t)
	defer r.Close()

	reply := runExample(t, r, "todos", exampleContext())
	for _, want := range []string{
		"## Open markers",
		"### notes",
		"- L11: - TODO: write the intro",
		"### main.go",
		"- L3: // FIXME: handle the error path",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("todos reply missing %q:\n%s", want, reply)
		}
	}

	clean := runExample(t, r, "todos", &Context{
		Workspaces: []WorkspaceInfo{{ID: "w", Type: "file", Title: "empty", Content: "nothing here\n"}},
	})
	if !strings.Contains(clean, "No TODO/FIXME/HACK markers") {
		t.Errorf("todos with clean workspaces = %q, want empty-state message", clean)
	}
}
