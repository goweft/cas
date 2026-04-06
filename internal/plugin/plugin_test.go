package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlugin(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// ── Load ──────────────────────────────────────────────────────────

func TestLoadEmptyDir(t *testing.T) {
	r := New(t.TempDir())
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if len(r.Commands()) != 0 {
		t.Error("expected no commands from empty dir")
	}
}

func TestLoadNonexistentDir(t *testing.T) {
	r := New("/tmp/cas-test-nonexistent-dir-999")
	if err := r.Load(); err != nil {
		t.Fatal("nonexistent dir should not error")
	}
}

func TestLoadSimplePlugin(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "hello.lua", `
cas.command("hello", "Say hello", function()
	cas.reply("Hello from Lua!")
end)
`)
	r := New(dir)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cmds := r.Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].Name != "hello" {
		t.Errorf("expected command 'hello', got %q", cmds[0].Name)
	}
	if cmds[0].Description != "Say hello" {
		t.Errorf("expected description 'Say hello', got %q", cmds[0].Description)
	}
	if cmds[0].PluginFile != "hello.lua" {
		t.Errorf("expected plugin file 'hello.lua', got %q", cmds[0].PluginFile)
	}
}

func TestLoadMultipleCommands(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "multi.lua", `
cas.command("greet", "Greet user", function()
	cas.reply("Hi!")
end)
cas.command("bye", "Say goodbye", function()
	cas.reply("Bye!")
end)
`)
	r := New(dir)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if len(r.Commands()) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(r.Commands()))
	}
}

func TestLoadMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "a.lua", `cas.command("alpha", "A", function() cas.reply("a") end)`)
	writePlugin(t, dir, "b.lua", `cas.command("beta", "B", function() cas.reply("b") end)`)

	r := New(dir)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if len(r.Commands()) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(r.Commands()))
	}
}

func TestLoadSkipsNonLuaFiles(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "notes.txt", "not a plugin")
	writePlugin(t, dir, "real.lua", `cas.command("real", "R", function() cas.reply("yes") end)`)

	r := New(dir)
	r.Load()
	defer r.Close()

	if len(r.Commands()) != 1 {
		t.Errorf("expected 1 command (skipping .txt), got %d", len(r.Commands()))
	}
}

func TestLoadBadLuaSyntax(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "bad.lua", `this is not valid lua }{}{`)
	writePlugin(t, dir, "good.lua", `cas.command("good", "G", function() cas.reply("ok") end)`)

	r := New(dir)
	r.Load()
	defer r.Close()

	if len(r.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(r.Errors()))
	}
	// Good plugin should still load
	if len(r.Commands()) != 1 {
		t.Errorf("expected 1 command despite bad plugin, got %d", len(r.Commands()))
	}
}

// ── Match ─────────────────────────────────────────────────────────

func TestMatchExact(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `cas.command("standup", "Daily standup", function() cas.reply("standup") end)`)

	r := New(dir)
	r.Load()
	defer r.Close()

	cmd, ok := r.Match("standup")
	if !ok {
		t.Fatal("expected match for 'standup'")
	}
	if cmd.Name != "standup" {
		t.Errorf("expected 'standup', got %q", cmd.Name)
	}
}

func TestMatchCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `cas.command("hello", "H", function() cas.reply("hi") end)`)

	r := New(dir)
	r.Load()
	defer r.Close()

	_, ok := r.Match("Hello")
	if !ok {
		t.Fatal("expected case-insensitive match")
	}
}

func TestMatchPrefix(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `cas.command("standup", "S", function() cas.reply("s") end)`)

	r := New(dir)
	r.Load()
	defer r.Close()

	_, ok := r.Match("standup please")
	if !ok {
		t.Fatal("expected prefix match for 'standup please'")
	}
}

func TestMatchNoMatch(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `cas.command("standup", "S", function() cas.reply("s") end)`)

	r := New(dir)
	r.Load()
	defer r.Close()

	_, ok := r.Match("hello world")
	if ok {
		t.Fatal("expected no match for 'hello world'")
	}
}

// ── Execute ───────────────────────────────────────────────────────

func TestExecuteSimpleReply(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `
cas.command("ping", "Pong", function()
	cas.reply("pong!")
end)
`)
	r := New(dir)
	r.Load()
	defer r.Close()

	cmd, _ := r.Match("ping")
	ctx := &Context{}
	reply, err := r.Execute(cmd, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "pong!" {
		t.Errorf("expected 'pong!', got %q", reply)
	}
}

func TestExecuteAccessWorkspaces(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `
cas.command("count", "Count workspaces", function()
	local ws = cas.workspaces()
	cas.reply("You have " .. #ws .. " workspaces")
end)
`)
	r := New(dir)
	r.Load()
	defer r.Close()

	cmd, _ := r.Match("count")
	ctx := &Context{
		Workspaces: []WorkspaceInfo{
			{ID: "1", Type: "document", Title: "Doc A", Content: "aaa"},
			{ID: "2", Type: "code", Title: "Script B", Content: "bbb"},
		},
	}
	reply, err := r.Execute(cmd, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "You have 2 workspaces" {
		t.Errorf("expected 'You have 2 workspaces', got %q", reply)
	}
}

func TestExecuteAccessActive(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `
cas.command("title", "Show active title", function()
	local ws = cas.active()
	if ws then
		cas.reply("Active: " .. ws.title)
	else
		cas.reply("No active workspace")
	end
end)
`)
	r := New(dir)
	r.Load()
	defer r.Close()

	cmd, _ := r.Match("title")

	// With workspace
	ctx := &Context{
		Workspaces: []WorkspaceInfo{
			{ID: "1", Type: "document", Title: "My Proposal", Content: "..."},
		},
	}
	reply, err := r.Execute(cmd, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Active: My Proposal" {
		t.Errorf("expected 'Active: My Proposal', got %q", reply)
	}

	// Without workspace
	ctx2 := &Context{}
	reply2, err := r.Execute(cmd, ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if reply2 != "No active workspace" {
		t.Errorf("expected 'No active workspace', got %q", reply2)
	}
}

func TestExecuteNoReply(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `
cas.command("silent", "Does nothing", function()
	-- intentionally empty
end)
`)
	r := New(dir)
	r.Load()
	defer r.Close()

	cmd, _ := r.Match("silent")
	reply, err := r.Execute(cmd, &Context{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "no output") {
		t.Errorf("expected 'no output' message, got %q", reply)
	}
}

func TestExecuteStringConcat(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `
cas.command("summary", "Summarize workspaces", function()
	local ws = cas.workspaces()
	local lines = {}
	for i, w in ipairs(ws) do
		lines[i] = "- " .. w.title .. " (" .. w.type .. ")"
	end
	cas.reply(table.concat(lines, "\n"))
end)
`)
	r := New(dir)
	r.Load()
	defer r.Close()

	cmd, _ := r.Match("summary")
	ctx := &Context{
		Workspaces: []WorkspaceInfo{
			{ID: "1", Type: "document", Title: "Proposal"},
			{ID: "2", Type: "code", Title: "Script"},
		},
	}
	reply, err := r.Execute(cmd, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "- Proposal (document)") {
		t.Errorf("missing 'Proposal' in output: %q", reply)
	}
	if !strings.Contains(reply, "- Script (code)") {
		t.Errorf("missing 'Script' in output: %q", reply)
	}
}

// ── Sandbox ───────────────────────────────────────────────────────

func TestSandboxNoOsExecute(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "evil.lua", `os.execute("echo pwned")`)

	r := New(dir)
	r.Load()
	defer r.Close()

	if len(r.Errors()) == 0 {
		t.Error("expected error for os.execute — os library should not be loaded")
	}
}

func TestSandboxNoDofile(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "evil.lua", `dofile("/etc/passwd")`)

	r := New(dir)
	r.Load()
	defer r.Close()

	if len(r.Errors()) == 0 {
		t.Error("expected error for dofile — should be removed")
	}
}

func TestSandboxNoIO(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "evil.lua", `local f = io.open("/etc/passwd", "r")`)

	r := New(dir)
	r.Load()
	defer r.Close()

	if len(r.Errors()) == 0 {
		t.Error("expected error for io.open — io library should not be loaded")
	}
}

// ── Close ─────────────────────────────────────────────────────────

func TestCloseResetsState(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "test.lua", `cas.command("hello", "H", function() cas.reply("hi") end)`)

	r := New(dir)
	r.Load()

	if len(r.Commands()) != 1 {
		t.Fatal("expected 1 command before close")
	}

	r.Close()

	if len(r.Commands()) != 0 {
		t.Error("expected 0 commands after close")
	}
}
