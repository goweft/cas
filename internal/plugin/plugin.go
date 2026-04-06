// Package plugin provides a Lua plugin runtime for CAS.
//
// Users place .lua files in ~/.cas/plugins/. Each plugin registers
// commands via cas.command(name, description, handler). When a user
// message matches a registered command, the handler runs instead of
// the LLM.
//
// The Lua VM is sandboxed: no file I/O, no os.execute, no network.
// Plugins interact with CAS through a controlled API table.
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"
)

// Command is a plugin-registered command.
type Command struct {
	Name        string
	Description string
	PluginFile  string
	handler     *lua.LFunction
}

// WorkspaceInfo is the read-only workspace data exposed to Lua.
type WorkspaceInfo struct {
	ID      string
	Type    string
	Title   string
	Content string
}

// Context is injected into plugin execution to provide CAS state.
type Context struct {
	Workspaces []WorkspaceInfo
	Reply      string // set by cas.reply() inside the handler
}

// Registry holds all loaded plugins and their commands.
type Registry struct {
	mu       sync.RWMutex
	commands map[string]*Command
	vms      map[string]*lua.LState // one VM per plugin file
	dir      string
	errors   []string
}

// New creates a Registry that loads plugins from dir.
func New(dir string) *Registry {
	return &Registry{
		commands: make(map[string]*Command),
		vms:      make(map[string]*lua.LState),
		dir:      dir,
	}
}

// DefaultDir returns ~/.cas/plugins.
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cas", "plugins")
}

// Load discovers and loads all .lua files in the plugin directory.
// Errors are collected but do not stop loading of other plugins.
func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.errors = nil

	if _, err := os.Stat(r.dir); os.IsNotExist(err) {
		return nil // no plugins dir — not an error
	}

	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return fmt.Errorf("read plugin dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lua") {
			continue
		}
		path := filepath.Join(r.dir, entry.Name())
		if err := r.loadFile(path); err != nil {
			r.errors = append(r.errors, fmt.Sprintf("%s: %v", entry.Name(), err))
		}
	}
	return nil
}

// loadFile creates a sandboxed VM, injects the cas API, and runs the file.
func (r *Registry) loadFile(path string) error {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})

	// Open only safe libraries
	for _, pair := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.LoadLibName, lua.OpenPackage}, // needed for require
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		if err := L.CallByParam(lua.P{
			Fn:      L.NewFunction(pair.fn),
			NRet:    0,
			Protect: true,
		}, lua.LString(pair.name)); err != nil {
			L.Close()
			return fmt.Errorf("open lib %s: %w", pair.name, err)
		}
	}

	// Remove dangerous globals that snuck in via base
	L.SetGlobal("dofile", lua.LNil)
	L.SetGlobal("loadfile", lua.LNil)

	// Inject cas API table
	casTable := L.NewTable()

	// cas.command(name, description, handler)
	L.SetField(casTable, "command", L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		desc := L.CheckString(2)
		handler := L.CheckFunction(3)

		lower := strings.ToLower(strings.TrimSpace(name))
		r.commands[lower] = &Command{
			Name:        lower,
			Description: desc,
			PluginFile:  filepath.Base(path),
			handler:     handler,
		}
		return 0
	}))

	L.SetGlobal("cas", casTable)

	// Execute the plugin file (registers commands)
	if err := L.DoFile(path); err != nil {
		L.Close()
		return err
	}

	r.vms[path] = L
	return nil
}

// Match checks if a message matches a registered command.
// Returns the command and true, or nil and false.
func (r *Registry) Match(message string) (*Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	msg := strings.ToLower(strings.TrimSpace(message))

	// Exact match first
	if cmd, ok := r.commands[msg]; ok {
		return cmd, true
	}

	// Prefix match: "standup please" matches "standup"
	for name, cmd := range r.commands {
		if strings.HasPrefix(msg, name+" ") || strings.HasPrefix(msg, name+".") {
			return cmd, true
		}
	}

	return nil, false
}

// Execute runs a matched command's handler with the given context.
func (r *Registry) Execute(cmd *Command, ctx *Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find the VM for this command's plugin
	path := filepath.Join(r.dir, cmd.PluginFile)
	L, ok := r.vms[path]
	if !ok {
		return "", fmt.Errorf("no VM for plugin %s", cmd.PluginFile)
	}

	// Inject runtime API functions that need context
	casTable := L.GetGlobal("cas").(*lua.LTable)

	// cas.reply(text)
	L.SetField(casTable, "reply", L.NewFunction(func(L *lua.LState) int {
		ctx.Reply = L.CheckString(1)
		return 0
	}))

	// cas.workspaces() → table of {id, type, title, content}
	L.SetField(casTable, "workspaces", L.NewFunction(func(L *lua.LState) int {
		tbl := L.NewTable()
		for i, ws := range ctx.Workspaces {
			entry := L.NewTable()
			L.SetField(entry, "id", lua.LString(ws.ID))
			L.SetField(entry, "type", lua.LString(ws.Type))
			L.SetField(entry, "title", lua.LString(ws.Title))
			L.SetField(entry, "content", lua.LString(ws.Content))
			L.RawSetInt(tbl, i+1, entry)
		}
		L.Push(tbl)
		return 1
	}))

	// cas.active() → {id, type, title, content} or nil
	L.SetField(casTable, "active", L.NewFunction(func(L *lua.LState) int {
		if len(ctx.Workspaces) == 0 {
			L.Push(lua.LNil)
			return 1
		}
		ws := ctx.Workspaces[len(ctx.Workspaces)-1]
		entry := L.NewTable()
		L.SetField(entry, "id", lua.LString(ws.ID))
		L.SetField(entry, "type", lua.LString(ws.Type))
		L.SetField(entry, "title", lua.LString(ws.Title))
		L.SetField(entry, "content", lua.LString(ws.Content))
		L.Push(entry)
		return 1
	}))

	// Call the handler
	if err := L.CallByParam(lua.P{
		Fn:      cmd.handler,
		NRet:    0,
		Protect: true,
	}); err != nil {
		return "", fmt.Errorf("plugin %s command %q: %w", cmd.PluginFile, cmd.Name, err)
	}

	if ctx.Reply == "" {
		return fmt.Sprintf("(plugin %q ran with no output)", cmd.Name), nil
	}
	return ctx.Reply, nil
}

// Commands returns all registered commands.
func (r *Registry) Commands() []*Command {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Command, 0, len(r.commands))
	for _, cmd := range r.commands {
		out = append(out, cmd)
	}
	return out
}

// Errors returns any errors from the last Load.
func (r *Registry) Errors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.errors
}

// Close shuts down all Lua VMs.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, L := range r.vms {
		L.Close()
	}
	r.vms = make(map[string]*lua.LState)
	r.commands = make(map[string]*Command)
}
