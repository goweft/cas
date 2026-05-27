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

	"github.com/goweft/cas/internal/agent"
	"github.com/goweft/cas/internal/conductor"
	casmc "github.com/goweft/cas/internal/mcp"
	"github.com/goweft/cas/internal/webview"
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
	genAgent   *agent.GenerationAgent
	editAgent  *agent.EditAgent
	combAgent  *agent.CombineAgent
	chatAgent  *agent.ChatAgent
	mcpAgent    *agent.MCPAgent
	mcpConns    map[string]*casmc.Connection  // wsID → connection
	webAgent        *agent.WebAgent
	webSessions     map[string]*webview.Session    // wsID → session
	webPages        map[string]*webview.PageState  // wsID → current page
	orchestAgent    *agent.OrchestratorAgent
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
		genAgent:   agent.NewGenerationAgent(),
		editAgent:  agent.NewEditAgent(),
		combAgent:  agent.NewCombineAgent(),
		chatAgent:  agent.NewChatAgent(),
		mcpAgent:    agent.NewMCPAgent(),
		mcpConns:    make(map[string]*casmc.Connection),
		webAgent:        agent.NewWebAgent(),
		webSessions:     make(map[string]*webview.Session),
		webPages:        make(map[string]*webview.PageState),
		orchestAgent:   agent.NewOrchestratorAgent(),
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
		genAgent:   agent.NewGenerationAgent(),
		editAgent:  agent.NewEditAgent(),
		combAgent:  agent.NewCombineAgent(),
		chatAgent:  agent.NewChatAgent(),
		mcpAgent:    agent.NewMCPAgent(),
		mcpConns:    make(map[string]*casmc.Connection),
		webAgent:        agent.NewWebAgent(),
		webSessions:     make(map[string]*webview.Session),
		webPages:        make(map[string]*webview.PageState),
		orchestAgent:   agent.NewOrchestratorAgent(),
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
	case intent.KindCombine:
		resp, err = sh.handleCombine(ctx, sess, message)
	case intent.KindIngest:
		resp, err = sh.handleIngest(ctx, sess, in)
	case intent.KindOrchestrate:
		resp, err = sh.handleOrchestrate(ctx, sess, message)
	case intent.KindBrowse:
		resp, err = sh.handleBrowse(ctx, sess, in)
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
	case intent.KindCombine:
		resp, err = sh.streamCombine(ctx, sess, message, onToken)
	case intent.KindIngest:
		r, e := sh.handleIngest(ctx, sess, in)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindOrchestrate:
		r, e := sh.handleOrchestrate(ctx, sess, message)
		if e != nil {
			return nil, e
		}
		resp = &StreamResponse{ChatReply: r.ChatReply, Workspace: r.Workspace, Intent: r.Intent}
	case intent.KindBrowse:
		r, e := sh.handleBrowse(ctx, sess, in)
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
	result, err := sh.genAgent.Generate(ctx, agent.GenerationRequest{
		WSType:      string(in.WSType),
		Title:       title,
		Prompt:      message,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.6,
	})
	if err != nil {
		return nil, err
	}
	content := normaliseContent(result.Content, string(in.WSType), title)
	ws, err := sh.workspaces.Create(newID(), string(in.WSType), title, content, sess.ID)
	if err != nil {
		return nil, err
	}
	reply := fmt.Sprintf("Created %s workspace %q. Edit directly or ask me to make changes.", in.WSType, ws.Title)
	return &Response{ChatReply: reply, Workspace: ws, Intent: in.Kind}, nil
}

func (sh *Shell) streamCreate(ctx context.Context, sess *Session, in intent.Intent, message string, onToken func(string)) (*StreamResponse, error) {
	title := titleOrDefault(in.TitleHint)
	result, err := sh.genAgent.Stream(ctx, agent.GenerationRequest{
		WSType:      string(in.WSType),
		Title:       title,
		Prompt:      message,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.6,
	}, onToken)
	if err != nil {
		return nil, err
	}
	content := normaliseContent(result.Content, string(in.WSType), title)
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
	ws := resolveTarget(message, active)
	refs := crossWorkspaceRefs(message, active, ws)
	refData := make([]struct{ Title, Content string }, len(refs))
	for i, r := range refs {
		refData[i] = struct{ Title, Content string }{r.Title, r.Content}
	}
	result, err := sh.editAgent.Edit(ctx, agent.EditRequest{
		WSType:         ws.Type,
		Title:          ws.Title,
		CurrentContent: ws.Content,
		EditRequest:    message,
		UserContext:    sh.conductor.UserContext(),
		Refs:           refData,
		Temperature:    0.3,
	})
	if err != nil {
		return nil, err
	}
	ws, err = sh.workspaces.Update(ws.ID, ws.Title, result.Content)
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
	ws := resolveTarget(message, active)
	refs := crossWorkspaceRefs(message, active, ws)
	refData := make([]struct{ Title, Content string }, len(refs))
	for i, r := range refs {
		refData[i] = struct{ Title, Content string }{r.Title, r.Content}
	}
	result, err := sh.editAgent.Stream(ctx, agent.EditRequest{
		WSType:         ws.Type,
		Title:          ws.Title,
		CurrentContent: ws.Content,
		EditRequest:    message,
		UserContext:    sh.conductor.UserContext(),
		Refs:           refData,
		Temperature:    0.3,
	}, onToken)
	if err != nil {
		return nil, err
	}
	ws, err = sh.workspaces.Update(ws.ID, ws.Title, result.Content)
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
	result, err := sh.chatAgent.Chat(ctx, agent.ChatRequest{
		Message:     message,
		History:     sessionHistory(sess),
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.7,
	})
	if err != nil {
		return nil, err
	}
	return &Response{ChatReply: result.Reply, Intent: intent.KindChat}, nil
}

func (sh *Shell) streamChat(ctx context.Context, sess *Session, message string, onToken func(string)) (*StreamResponse, error) {
	result, err := sh.chatAgent.Stream(ctx, agent.ChatRequest{
		Message:     message,
		History:     sessionHistory(sess),
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.7,
	}, onToken)
	if err != nil {
		return nil, err
	}
	return &StreamResponse{ChatReply: result.Reply, Intent: intent.KindChat}, nil
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

func (sh *Shell) handleCombine(ctx context.Context, sess *Session, message string) (*Response, error) {
	active := sh.workspaces.Active()
	if len(active) < 2 {
		return &Response{
			ChatReply: "Need at least 2 active workspaces to combine.",
			Intent:    intent.KindCombine,
		}, nil
	}

	sources := resolveAll(message, active)
	if len(sources) < 2 {
		return &Response{
			ChatReply: "Could not identify 2 or more workspaces to combine. Try naming them explicitly.",
			Intent:    intent.KindCombine,
		}, nil
	}

	wsData := make([]struct{ Title, Type, Content string }, len(sources))
	for i, s := range sources {
		wsData[i] = struct{ Title, Type, Content string }{s.Title, s.Type, s.Content}
	}

	result, err := sh.combAgent.Combine(ctx, agent.CombineRequest{
		Sources:     wsData,
		Instruction: message,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.5,
	})
	if err != nil {
		return nil, err
	}

	titles := make([]string, len(sources))
	for i, s := range sources {
		titles[i] = s.Title
	}
	title := "Combined: " + strings.Join(titles, " + ")
	if len(title) > 64 {
		title = title[:61] + "..."
	}

	ws, err := sh.workspaces.Create(newID(), "document", title, result.Content, sess.ID)
	if err != nil {
		return nil, err
	}

	reply := fmt.Sprintf("Combined %d workspaces into %q.", len(sources), ws.Title)
	return &Response{ChatReply: reply, Workspace: ws, Intent: intent.KindCombine}, nil
}

func (sh *Shell) streamCombine(ctx context.Context, sess *Session, message string, onToken func(string)) (*StreamResponse, error) {
	active := sh.workspaces.Active()
	if len(active) < 2 {
		return &StreamResponse{
			ChatReply: "Need at least 2 active workspaces to combine.",
			Intent:    intent.KindCombine,
		}, nil
	}

	sources := resolveAll(message, active)
	if len(sources) < 2 {
		return &StreamResponse{
			ChatReply: "Could not identify 2 or more workspaces to combine. Try naming them explicitly.",
			Intent:    intent.KindCombine,
		}, nil
	}

	wsData := make([]struct{ Title, Type, Content string }, len(sources))
	for i, s := range sources {
		wsData[i] = struct{ Title, Type, Content string }{s.Title, s.Type, s.Content}
	}

	result, err := sh.combAgent.Stream(ctx, agent.CombineRequest{
		Sources:     wsData,
		Instruction: message,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.5,
	}, onToken)
	if err != nil {
		return nil, err
	}

	titles := make([]string, len(sources))
	for i, s := range sources {
		titles[i] = s.Title
	}
	title := "Combined: " + strings.Join(titles, " + ")
	if len(title) > 64 {
		title = title[:61] + "..."
	}

	ws, err := sh.workspaces.Create(newID(), "document", title, result.Content, sess.ID)
	if err != nil {
		return nil, err
	}

	reply := fmt.Sprintf("Combined %d workspaces into %q.", len(sources), ws.Title)
	return &StreamResponse{ChatReply: reply, Workspace: ws, Intent: intent.KindCombine}, nil
}

// handleIngest connects to an MCP server and materializes it as a workspace.
func (sh *Shell) handleIngest(ctx context.Context, sess *Session, in intent.Intent) (*Response, error) {
	serverURL := in.TitleHint
	if serverURL == "" {
		return &Response{ChatReply: "No server URL found. Usage: ingest <url>", Intent: intent.KindIngest}, nil
	}

	conn, err := casmc.Connect(ctx, serverURL)
	if err != nil {
		return &Response{
			ChatReply: fmt.Sprintf("Failed to connect to %s: %v", serverURL, err),
			Intent:    intent.KindIngest,
		}, nil
	}

	title := "MCP: " + serverURL
	content := formatMCPWorkspace(conn)
	ws, err := sh.workspaces.Create(newID(), "mcp", title, content, sess.ID)
	if err != nil {
		conn.Close()
		return nil, err
	}

	sh.mcpConns[ws.ID] = conn

	reply := fmt.Sprintf("Connected to %s — %d tool(s) available. Ask me to use any of them.", serverURL, len(conn.Tools))
	return &Response{ChatReply: reply, Workspace: ws, Intent: intent.KindIngest}, nil
}

// HandleMCPAction executes a user instruction against the MCP workspace.
// Called by the shell when the active workspace is type "mcp".
func (sh *Shell) HandleMCPAction(ctx context.Context, wsID, instruction string, autonomy agent.Autonomy) (*agent.MCPResult, error) {
	conn, ok := sh.mcpConns[wsID]
	if !ok {
		return nil, fmt.Errorf("no MCP connection for workspace %q", wsID)
	}
	return sh.mcpAgent.Act(ctx, agent.MCPRequest{
		Instruction: instruction,
		Connection:  conn,
		Autonomy:    autonomy,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.3,
	})
}

// CloseMCPWorkspace closes the MCP connection when its workspace is closed.
func (sh *Shell) CloseMCPWorkspace(wsID string) {
	if conn, ok := sh.mcpConns[wsID]; ok {
		conn.Close()
		delete(sh.mcpConns, wsID)
	}
}

// formatMCPWorkspace produces the initial content for an MCP workspace panel.
func formatMCPWorkspace(conn *casmc.Connection) string {
	var sb strings.Builder
	sb.WriteString("# MCP Server\n\n")
	sb.WriteString("**URL:** " + conn.ServerURL + "\n\n")
	sb.WriteString("## Available Tools\n\n")
	for _, t := range conn.Tools {
		sb.WriteString("### " + t.Name + "\n")
		if t.Description != "" {
			sb.WriteString(t.Description + "\n")
		}
		sb.WriteString("\n")
	}
	if len(conn.Tools) == 0 {
		sb.WriteString("_(no tools discovered)_\n")
	}
	return sb.String()
}

// handleBrowse fetches a URL and materializes it as a web workspace.
func (sh *Shell) handleBrowse(ctx context.Context, sess *Session, in intent.Intent) (*Response, error) {
	pageURL := in.TitleHint
	if pageURL == "" {
		return &Response{ChatReply: "No URL found. Usage: browse <url>", Intent: intent.KindBrowse}, nil
	}

	webSess, err := webview.NewSession(ctx, pageURL)
	if err != nil {
		return &Response{
			ChatReply: fmt.Sprintf("Failed to create session for %s: %v", pageURL, err),
			Intent:    intent.KindBrowse,
		}, nil
	}

	page, err := webSess.Navigate(ctx)
	if err != nil {
		webSess.Close()
		return &Response{
			ChatReply: fmt.Sprintf("Failed to fetch %s: %v", pageURL, err),
			Intent:    intent.KindBrowse,
		}, nil
	}

	title := page.Title
	if title == "" {
		title = pageURL
	}
	wsTitle := "Web: " + title
	if len(wsTitle) > 64 {
		wsTitle = wsTitle[:61] + "..."
	}

	content := webview.FormatPageState(page)
	ws, err := sh.workspaces.Create(newID(), "web", wsTitle, content, sess.ID)
	if err != nil {
		webSess.Close()
		return nil, err
	}

	sh.webSessions[ws.ID] = webSess
	sh.webPages[ws.ID] = page

	reply := fmt.Sprintf("Loaded %s — %d headings, %d links found.", title, len(page.Headings), len(page.Links))
	return &Response{ChatReply: reply, Workspace: ws, Intent: intent.KindBrowse}, nil
}

// HandleWebAction executes a user instruction against a web workspace.
func (sh *Shell) HandleWebAction(ctx context.Context, wsID, instruction string, autonomy agent.Autonomy) (*agent.WebResult, error) {
	sess, ok := sh.webSessions[wsID]
	if !ok {
		return nil, fmt.Errorf("no web session for workspace %q", wsID)
	}
	page, ok := sh.webPages[wsID]
	if !ok {
		return nil, fmt.Errorf("no page state for workspace %q", wsID)
	}
	result, err := sh.webAgent.Act(ctx, agent.WebRequest{
		Instruction: instruction,
		Session:     sess,
		PageState:   page,
		Autonomy:    autonomy,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.3,
	})
	if err != nil {
		return nil, err
	}
	// If the agent navigated, update the stored page state
	if result.NewPage != nil {
		sh.webPages[wsID] = result.NewPage
	}
	return result, nil
}

// handleOrchestrate coordinates a multi-workspace task.
func (sh *Shell) handleOrchestrate(ctx context.Context, sess *Session, message string) (*Response, error) {
	active := sh.workspaces.Active()
	if len(active) < 2 {
		// Fall through to chat if fewer than 2 workspaces are open
		return sh.handleChat(ctx, sess, message)
	}

	// Build WorkspaceInfo for each active workspace
	wsInfos := make([]agent.WorkspaceInfo, len(active))
	for i, ws := range active {
		info := agent.WorkspaceInfo{
			ID:    ws.ID,
			Title: ws.Title,
			Type:  ws.Type,
		}
		if conn, ok := sh.mcpConns[ws.ID]; ok {
			info.ToolSummary = conn.ToolSummary()
		}
		if len(ws.Content) > 200 {
			info.ContentSnip = ws.Content[:200]
		} else {
			info.ContentSnip = ws.Content
		}
		wsInfos[i] = info
	}

	result, err := sh.orchestAgent.Orchestrate(ctx, agent.OrchestratorRequest{
		Instruction: message,
		Workspaces:  wsInfos,
		Executor:    sh,
		Autonomy:    agent.AutonomyRun,
		UserContext: sh.conductor.UserContext(),
		Temperature: 0.3,
	})
	if err != nil {
		return nil, err
	}

	return &Response{ChatReply: result.Summary, Intent: intent.KindOrchestrate}, nil
}

// ExecuteStep implements agent.StepExecutor.
// Routes a single orchestration step to the appropriate agent based on workspace type.
func (sh *Shell) ExecuteStep(ctx context.Context, wsID, instruction, priorContext string) (string, error) {
	ws, err := sh.workspaces.Get(wsID)
	if err != nil || ws == nil {
		return "", fmt.Errorf("workspace %q not found", wsID)
	}

	// Prepend prior context to instruction if present
	fullInstruction := instruction
	if priorContext != "" {
		fullInstruction = priorContext + "\n\n" + instruction
	}

	switch ws.Type {
	case "mcp":
		result, err := sh.HandleMCPAction(ctx, wsID, fullInstruction, agent.AutonomyRun)
		if err != nil {
			return "", err
		}
		if result.Output != "" {
			return result.Output, nil
		}
		return result.Suggestion, nil
	case "web":
		result, err := sh.HandleWebAction(ctx, wsID, fullInstruction, agent.AutonomyRun)
		if err != nil {
			return "", err
		}
		if result.Answer != "" {
			return result.Answer, nil
		}
		if result.NewPage != nil {
			return result.NewPage.Text, nil
		}
		return "", nil
	default:
		// For document/code/list workspaces: use EditAgent to apply the instruction
		result, err := sh.editAgent.Edit(ctx, agent.EditRequest{
			WSType:         ws.Type,
			Title:          ws.Title,
			CurrentContent: ws.Content,
			EditRequest:    fullInstruction,
			UserContext:    sh.conductor.UserContext(),
			Temperature:    0.3,
		})
		if err != nil {
			return "", err
		}
		// Persist the edit
		_, err = sh.workspaces.Update(wsID, ws.Title, result.Content)
		return result.Content, err
	}
}

// CloseWebWorkspace cleans up a web session when its workspace is closed.
func (sh *Shell) CloseWebWorkspace(wsID string) {
	if sess, ok := sh.webSessions[wsID]; ok {
		sess.Close()
		delete(sh.webSessions, wsID)
	}
	delete(sh.webPages, wsID)
}

// crossWorkspaceRefs finds workspaces referenced in the message other than the target.
// Used to include additional context in edit prompts.
func crossWorkspaceRefs(message string, active []*workspace.Workspace, target *workspace.Workspace) []*workspace.Workspace {
	if len(active) < 2 {
		return nil
	}
	msg := strings.ToLower(message)
	var refs []*workspace.Workspace
	for _, ws := range active {
		if ws.ID == target.ID {
			continue
		}
		if titleMatchScore(msg, ws.Title) > 0 {
			refs = append(refs, ws)
		}
	}
	return refs
}

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


