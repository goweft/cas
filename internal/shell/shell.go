// Package shell is the CAS session manager.
// It wires intent detection, workspace lifecycle, LLM calls, persistence,
// and behavioral learning (Conductor).
package shell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/goweft/cas/internal/conductor"
	"github.com/goweft/cas/internal/intent"
	"github.com/goweft/cas/internal/llm"
	"github.com/goweft/cas/internal/store"
	"github.com/goweft/cas/internal/plugin"
	"github.com/goweft/cas/internal/runner"
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
	store      store.Store
	workspaces *workspace.Manager
	sessions   map[string]*Session
	conductor  *conductor.Conductor
	plugins    *plugin.Registry
}

// NewShell creates a Shell backed by the given store.
// conductorPath is the profile JSON path; pass "" for the default ~/.cas/profile.json.
func NewShell(s store.Store, conductorPath ...string) *Shell {
	path := ""
	if len(conductorPath) > 0 {
		path = conductorPath[0]
	}
	sh := &Shell{
		store:      s,
		workspaces: workspace.NewManager(s),
		sessions:   make(map[string]*Session),
		conductor:  conductor.New(path),
		plugins:    plugin.New(plugin.DefaultDir()),
	}
	sh.plugins.Load()
	return sh
}


// NewShellWithPlugins creates a Shell with a custom plugin directory.
func NewShellWithPlugins(s store.Store, conductorPath, pluginDir string) *Shell {
	sh := &Shell{
		store:      s,
		workspaces: workspace.NewManager(s),
		sessions:   make(map[string]*Session),
		conductor:  conductor.New(conductorPath),
		plugins:    plugin.New(pluginDir),
	}
	sh.plugins.Load()
	return sh
}
// Restore loads persisted sessions and workspaces from the store.
func (sh *Shell) Restore() error {
	if err := sh.workspaces.Restore(); err != nil {
		return fmt.Errorf("restore workspaces: %w", err)
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

// ProcessMessage classifies the message, calls the LLM, and returns a Response.
func (sh *Shell) ProcessMessage(ctx context.Context, sessionID, message string) (*Response, error) {
	sess, err := sh.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Check plugin commands first — user-defined overrides
	if cmd, ok := sh.plugins.Match(message); ok {
		userMsg := sess.addMessage("user", message)
		if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
			return nil, err
		}
		return sh.handlePlugin(sess, cmd)
	}

	in := intent.Detect(message)
	userMsg := sess.addMessage("user", message)
	if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
		return nil, err
	}

	var resp *Response
	switch in.Kind {
	case intent.KindCreate:
		resp, err = sh.handleCreate(ctx, sess, in, message)
	case intent.KindEdit:
		resp, err = sh.handleEdit(ctx, sess, message)
	case intent.KindClose:
		resp, err = sh.handleClose(sess)
	case intent.KindRun:
		resp, err = sh.handleRun(ctx, sess)
	default:
		resp, err = sh.handleChat(ctx, sess, message)
	}
	if err != nil {
		return nil, err
	}

	shellMsg := sess.addMessage("shell", resp.ChatReply)
	if err := sh.store.SaveMessage(toStoreMsg(shellMsg)); err != nil {
		return nil, err
	}

	wsTitle, wsType := "", ""
	if resp.Workspace != nil {
		wsTitle, wsType = resp.Workspace.Title, resp.Workspace.Type
	}
	sh.conductor.Observe(string(in.Kind), message, wsTitle, wsType)

	return resp, nil
}

// StreamMessage classifies the message, streams tokens via onToken,
// and returns a StreamResponse when generation finishes.
func (sh *Shell) StreamMessage(ctx context.Context, sessionID, message string, onToken func(string)) (*StreamResponse, error) {
	sess, err := sh.GetSession(sessionID)
	if err != nil {
		return nil, err
	}

	// Check plugin commands first
	if cmd, ok := sh.plugins.Match(message); ok {
		userMsg := sess.addMessage("user", message)
		if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
			return nil, err
		}
		r, err := sh.handlePlugin(sess, cmd)
		if err != nil {
			return nil, err
		}
		return &StreamResponse{ChatReply: r.ChatReply, Intent: r.Intent}, nil
	}

	in := intent.Detect(message)
	userMsg := sess.addMessage("user", message)
	if err := sh.store.SaveMessage(toStoreMsg(userMsg)); err != nil {
		return nil, err
	}

	var resp *StreamResponse
	switch in.Kind {
	case intent.KindCreate:
		resp, err = sh.streamCreate(ctx, sess, in, message, onToken)
	case intent.KindEdit:
		resp, err = sh.streamEdit(ctx, sess, message, onToken)
	case intent.KindClose:
		r, e := sh.handleClose(sess)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindRun:
		r, e := sh.handleRun(ctx, sess)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	default:
		resp, err = sh.streamChat(ctx, sess, message, onToken)
	}
	if err != nil {
		return nil, err
	}

	shellMsg := sess.addMessage("shell", resp.ChatReply)
	if err := sh.store.SaveMessage(toStoreMsg(shellMsg)); err != nil {
		return nil, err
	}

	wsTitle, wsType := "", ""
	if resp.Workspace != nil {
		wsTitle, wsType = resp.Workspace.Title, resp.Workspace.Type
	}
	sh.conductor.Observe(string(in.Kind), message, wsTitle, wsType)

	return resp, nil
}

// ── Handlers ──────────────────────────────────────────────────────

func (sh *Shell) handleCreate(ctx context.Context, sess *Session, in intent.Intent, message string) (*Response, error) {
	title := titleOrDefault(in.TitleHint)
	sys := llm.SystemFor(llm.WorkspaceSystem, string(in.WSType), sh.conductor.UserContext())
	msgs := llm.BuildWorkspaceMessages(sys, title, message)
	content, err := llm.Complete(ctx, msgs, llm.ModelFor(string(in.WSType)), 0.6)
	if err != nil {
		return nil, err
	}
	content = normaliseContent(content, string(in.WSType), title)
	ws, err := sh.workspaces.Create(newID(), string(in.WSType), title, content, sess.ID)
	if err != nil {
		return nil, err
	}
	reply := fmt.Sprintf("Created %s workspace %q. Edit directly or ask me to make changes.", in.WSType, ws.Title)
	return &Response{ChatReply: reply, Workspace: ws, Intent: in.Kind}, nil
}

func (sh *Shell) streamCreate(ctx context.Context, sess *Session, in intent.Intent, message string, onToken func(string)) (*StreamResponse, error) {
	title := titleOrDefault(in.TitleHint)
	sys := llm.SystemFor(llm.WorkspaceSystem, string(in.WSType), sh.conductor.UserContext())
	msgs := llm.BuildWorkspaceMessages(sys, title, message)
	content, err := llm.Stream(ctx, msgs, llm.ModelFor(string(in.WSType)), 0.6, onToken)
	if err != nil {
		return nil, err
	}
	content = normaliseContent(content, string(in.WSType), title)
	ws, err := sh.workspaces.Create(newID(), string(in.WSType), title, content, sess.ID)
	if err != nil {
		return nil, err
	}
	reply := fmt.Sprintf("Created %s workspace %q. Edit directly or ask me to make changes.", in.WSType, ws.Title)
	return &StreamResponse{ChatReply: reply, Workspace: ws, Intent: in.Kind}, nil
}

func (sh *Shell) handleEdit(ctx context.Context, sess *Session, message string) (*Response, error) {
	active := sh.workspaces.Active()
	if len(active) == 0 {
		return &Response{ChatReply: "No active workspace to edit. Ask me to create one first.", Intent: intent.KindEdit}, nil
	}
	ws := active[len(active)-1]
	sys := llm.SystemFor(llm.EditSystem, ws.Type, sh.conductor.UserContext())
	msgs := llm.BuildEditMessages(sys, ws.Title, ws.Content, message)
	content, err := llm.Complete(ctx, msgs, llm.ModelFor(ws.Type), 0.3)
	if err != nil {
		return nil, err
	}
	ws, err = sh.workspaces.Update(ws.ID, ws.Title, content)
	if err != nil {
		return nil, err
	}
	reply := fmt.Sprintf("Updated workspace %q.", ws.Title)
	return &Response{ChatReply: reply, Workspace: ws, Intent: intent.KindEdit}, nil
}

func (sh *Shell) streamEdit(ctx context.Context, sess *Session, message string, onToken func(string)) (*StreamResponse, error) {
	active := sh.workspaces.Active()
	if len(active) == 0 {
		return &StreamResponse{ChatReply: "No active workspace to edit. Ask me to create one first.", Intent: intent.KindEdit}, nil
	}
	ws := active[len(active)-1]
	sys := llm.SystemFor(llm.EditSystem, ws.Type, sh.conductor.UserContext())
	msgs := llm.BuildEditMessages(sys, ws.Title, ws.Content, message)
	content, err := llm.Stream(ctx, msgs, llm.ModelFor(ws.Type), 0.3, onToken)
	if err != nil {
		return nil, err
	}
	ws, err = sh.workspaces.Update(ws.ID, ws.Title, content)
	if err != nil {
		return nil, err
	}
	reply := fmt.Sprintf("Updated workspace %q.", ws.Title)
	return &StreamResponse{ChatReply: reply, Workspace: ws, Intent: intent.KindEdit}, nil
}

func (sh *Shell) handleClose(sess *Session) (*Response, error) {
	active := sh.workspaces.Active()
	if len(active) == 0 {
		return &Response{ChatReply: "No active workspace to close.", Intent: intent.KindClose}, nil
	}
	ws := active[len(active)-1]
	ws, err := sh.workspaces.Close(ws.ID)
	if err != nil {
		return nil, err
	}
	return &Response{ChatReply: fmt.Sprintf("Closed workspace %q.", ws.Title), Workspace: ws, Intent: intent.KindClose}, nil
}

func (sh *Shell) handleChat(ctx context.Context, sess *Session, message string) (*Response, error) {
	msgs := llm.BuildChatMessages(llm.ChatSystem+contextSuffix(sh.conductor.UserContext()), sessionHistory(sess), message)
	reply, err := llm.Complete(ctx, msgs, llm.ModelFor("chat"), 0.7)
	if err != nil {
		return nil, err
	}
	if reply == "" {
		reply = `To create a workspace, say: "write a [document type]".`
	}
	return &Response{ChatReply: reply, Intent: intent.KindChat}, nil
}

func (sh *Shell) streamChat(ctx context.Context, sess *Session, message string, onToken func(string)) (*StreamResponse, error) {
	msgs := llm.BuildChatMessages(llm.ChatSystem+contextSuffix(sh.conductor.UserContext()), sessionHistory(sess), message)
	reply, err := llm.Stream(ctx, msgs, llm.ModelFor("chat"), 0.7, onToken)
	if err != nil {
		return nil, err
	}
	if reply == "" {
		reply = `To create a workspace, say: "write a [document type]".`
	}
	return &StreamResponse{ChatReply: reply, Intent: intent.KindChat}, nil
}

func (sh *Shell) handleRun(ctx context.Context, sess *Session) (*Response, error) {
	active := sh.workspaces.Active()
	if len(active) == 0 {
		return &Response{ChatReply: "No active workspace to run. Create a code workspace first.", Intent: intent.KindRun}, nil
	}
	ws := active[len(active)-1]
	if ws.Type != "code" {
		return &Response{
			ChatReply: fmt.Sprintf("Cannot run a %s workspace. Only code workspaces can be executed.", ws.Type),
			Workspace: ws,
			Intent:    intent.KindRun,
		}, nil
	}
	if strings.TrimSpace(ws.Content) == "" {
		return &Response{
			ChatReply: "Workspace is empty — nothing to run.",
			Workspace: ws,
			Intent:    intent.KindRun,
		}, nil
	}

	result, err := runner.Run(ctx, ws.Content, runner.DefaultTimeout)
	if err != nil {
		return &Response{
			ChatReply: fmt.Sprintf("Run failed: %v", err),
			Workspace: ws,
			Intent:    intent.KindRun,
		}, nil
	}

	return &Response{
		ChatReply: runner.FormatResult(result),
		Workspace: ws,
		Intent:    intent.KindRun,
	}, nil
}

func (sh *Shell) handlePlugin(sess *Session, cmd *plugin.Command) (*Response, error) {
	// Build plugin context from active workspaces
	active := sh.workspaces.Active()
	wsInfos := make([]plugin.WorkspaceInfo, len(active))
	for i, ws := range active {
		wsInfos[i] = plugin.WorkspaceInfo{
			ID:      ws.ID,
			Type:    ws.Type,
			Title:   ws.Title,
			Content: ws.Content,
		}
	}

	ctx := &plugin.Context{Workspaces: wsInfos}
	reply, err := sh.plugins.Execute(cmd, ctx)
	if err != nil {
		reply = fmt.Sprintf("Plugin error: %v", err)
	}

	shellMsg := sess.addMessage("shell", reply)
	if err := sh.store.SaveMessage(toStoreMsg(shellMsg)); err != nil {
		return nil, err
	}

	sh.conductor.Observe(string(intent.KindPlugin), cmd.Name, "", "")
	return &Response{ChatReply: reply, Intent: intent.KindPlugin}, nil
}

// Plugins returns the plugin registry for inspection.
func (sh *Shell) Plugins() *plugin.Registry { return sh.plugins }

// ── Helpers ───────────────────────────────────────────────────────

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func toStoreMsg(m Message) store.MessageRow {
	return store.MessageRow{
		ID: m.ID, SessionID: m.SessionID,
		Role: m.Role, Text: m.Text, Timestamp: m.Timestamp,
	}
}

func sessionHistory(sess *Session) []llm.Message {
	out := make([]llm.Message, 0, len(sess.History))
	for _, m := range sess.History {
		role := "assistant"
		if m.Role == "user" {
			role = "user"
		}
		out = append(out, llm.Message{Role: role, Content: m.Text})
	}
	return out
}

func normaliseContent(content, wsType, title string) string {
	content = strings.TrimSpace(content)
	if wsType == "code" {
		return content
	}
	if !strings.HasPrefix(content, "#") {
		return "# " + title + "\n\n" + content
	}
	return content
}

func titleOrDefault(hint string) string {
	if hint == "" {
		return "Untitled"
	}
	return hint
}

func contextSuffix(ctx string) string {
	if ctx == "" {
		return ""
	}
	return "\n\nUser context: " + ctx
}
