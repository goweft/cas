// Package shell is the CAS session manager.
// It wires intent detection, workspace lifecycle, LLM calls, persistence,
// and behavioral learning (Conductor).
package shell

import (
	"fmt"
	"strings"
	"time"

	"github.com/goweft/cas/internal/agent"
	"github.com/goweft/cas/internal/conductor"
	"github.com/goweft/cas/internal/intent"
	casmc "github.com/goweft/cas/internal/mcp"
	"github.com/goweft/cas/internal/plugin"
	"github.com/goweft/cas/internal/store"
	"github.com/goweft/cas/internal/webview"
	"github.com/goweft/cas/internal/workspace"
)

// Message is a single conversation turn.
type Message struct {
	ID        string
	SessionID string
	Role      string // "user" | "shell"
	Text      string
	Timestamp time.Time
}

// Session is a single CAS conversation session.
type Session struct {
	ID        string
	CreatedAt time.Time
	History   []Message
}

func (s *Session) addMessage(role, text string) Message {
	msg := Message{
		ID: newID(), SessionID: s.ID,
		Role: role, Text: text, Timestamp: time.Now().UTC(),
	}
	s.History = append(s.History, msg)
	return msg
}

// Response is returned from ProcessMessage.
type Response struct {
	ChatReply string
	Workspace *workspace.Workspace
	Intent    intent.Kind
}

// StreamResponse is returned from StreamMessage.
type StreamResponse struct {
	ChatReply string
	Workspace *workspace.Workspace
	Intent    intent.Kind
}

// Shell is the central CAS coordinator.
type Shell struct {
	store        store.Store
	workspaces   *workspace.Manager
	sessions     map[string]*Session
	conductor    *conductor.Conductor
	plugins      *plugin.Registry
	genAgent     *agent.GenerationAgent
	editAgent    *agent.EditAgent
	combAgent    *agent.CombineAgent
	chatAgent    *agent.ChatAgent
	mcpAgent     *agent.MCPAgent
	mcpConns     map[string]*casmc.Connection // wsID → connection
	webAgent     *agent.WebAgent
	webSessions  map[string]*webview.Session   // wsID → session
	webPages     map[string]*webview.PageState // wsID → current page
	orchestAgent *agent.OrchestratorAgent
}

// NewShell creates a Shell backed by the given store.
// conductorPath is the profile JSON path; pass "" for the default ~/.cas/profile.json.
func NewShell(s store.Store, conductorPath ...string) *Shell {
	path := ""
	if len(conductorPath) > 0 {
		path = conductorPath[0]
	}
	sh := &Shell{
		store:        s,
		workspaces:   workspace.NewManager(s),
		sessions:     make(map[string]*Session),
		conductor:    conductor.New(path),
		plugins:      plugin.New(plugin.DefaultDir()),
		genAgent:     agent.NewGenerationAgent(),
		editAgent:    agent.NewEditAgent(),
		combAgent:    agent.NewCombineAgent(),
		chatAgent:    agent.NewChatAgent(),
		mcpAgent:     agent.NewMCPAgent(),
		mcpConns:     make(map[string]*casmc.Connection),
		webAgent:     agent.NewWebAgent(),
		webSessions:  make(map[string]*webview.Session),
		webPages:     make(map[string]*webview.PageState),
		orchestAgent: agent.NewOrchestratorAgent(),
	}
	sh.plugins.Load()
	return sh
}

// NewShellWithPlugins creates a Shell with a custom plugin directory.
func NewShellWithPlugins(s store.Store, conductorPath, pluginDir string) *Shell {
	sh := &Shell{
		store:        s,
		workspaces:   workspace.NewManager(s),
		sessions:     make(map[string]*Session),
		conductor:    conductor.New(conductorPath),
		plugins:      plugin.New(pluginDir),
		genAgent:     agent.NewGenerationAgent(),
		editAgent:    agent.NewEditAgent(),
		combAgent:    agent.NewCombineAgent(),
		chatAgent:    agent.NewChatAgent(),
		mcpAgent:     agent.NewMCPAgent(),
		mcpConns:     make(map[string]*casmc.Connection),
		webAgent:     agent.NewWebAgent(),
		webSessions:  make(map[string]*webview.Session),
		webPages:     make(map[string]*webview.PageState),
		orchestAgent: agent.NewOrchestratorAgent(),
	}
	sh.plugins.Load()
	return sh
}

// Restore loads persisted sessions and workspaces from the store.
func (sh *Shell) Restore() error {
	if err := sh.workspaces.Restore(); err != nil {
		return fmt.Errorf("restore workspaces: %w", err)
	}

	// For mcp and web workspaces restored without live sessions, prepend a
	// stale notice so the user knows reconnection is needed.
	for _, ws := range sh.workspaces.Active() {
		if !ws.Connected {
			staleNote := "> **Disconnected** — session ended. Type `reconnect` to restore.\n\n"
			if !strings.HasPrefix(ws.Content, "> **Disconnected**") {
				// Update in-memory only; store content is the last good snapshot.
				ws.Content = staleNote + ws.Content
			}
		}
	}

	rows, err := sh.store.LoadSessions()
	if err != nil {
		return fmt.Errorf("restore sessions: %w", err)
	}
	for _, row := range rows {
		sess := &Session{ID: row.ID, CreatedAt: row.CreatedAt}
		msgs, err := sh.store.LoadMessages(row.ID)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			sess.History = append(sess.History, Message{
				ID: m.ID, SessionID: m.SessionID,
				Role: m.Role, Text: m.Text, Timestamp: m.Timestamp,
			})
		}
		sh.sessions[sess.ID] = sess
	}
	return nil
}

// CreateSession starts a new conversation session and records it with the conductor.
func (sh *Shell) CreateSession() (*Session, error) {
	sess := &Session{ID: newID(), CreatedAt: time.Now().UTC()}
	sh.sessions[sess.ID] = sess
	sh.conductor.ObserveSessionStart()
	return sess, sh.store.SaveSession(sess.ID, sess.CreatedAt)
}

// GetSession returns the session with the given ID.
func (sh *Shell) GetSession(id string) (*Session, error) {
	sess, ok := sh.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return sess, nil
}

// LatestSession returns the most recently created session, or nil.
func (sh *Shell) LatestSession() *Session {
	var latest *Session
	for _, s := range sh.sessions {
		if latest == nil || s.CreatedAt.After(latest.CreatedAt) {
			latest = s
		}
	}
	return latest
}

// Workspaces returns the workspace manager.
func (sh *Shell) Workspaces() *workspace.Manager { return sh.workspaces }

// ProfileSummary returns the conductor's behavioral profile for display.
func (sh *Shell) ProfileSummary() map[string]interface{} {
	return sh.conductor.ProfileSummary()
}

// UserContext returns the conductor's current context string.
func (sh *Shell) UserContext() string { return sh.conductor.UserContext() }

// Plugins returns the plugin registry for inspection.
func (sh *Shell) Plugins() *plugin.Registry { return sh.plugins }
