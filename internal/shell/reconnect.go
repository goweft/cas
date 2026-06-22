package shell

import (
	"context"
	"fmt"
	"strings"

	"github.com/goweft/cas/internal/intent"
	casmc "github.com/goweft/cas/internal/mcp"
	"github.com/goweft/cas/internal/webview"
	"github.com/goweft/cas/internal/workspace"
)

// handleReconnect re-establishes a live session for a disconnected mcp or web workspace.
func (sh *Shell) handleReconnect(ctx context.Context, sess *Session, in intent.Intent) (*Response, error) {
	active := sh.workspaces.Active()

	// Find the target: explicit title hint first, then any disconnected workspace.
	var target *workspace.Workspace
	if in.TitleHint != "" {
		target = resolveTarget(in.TitleHint, active)
	} else {
		// Auto-select the first disconnected workspace.
		for _, ws := range active {
			if !ws.Connected {
				target = ws
				break
			}
		}
	}

	if target == nil {
		return &Response{ChatReply: "No disconnected workspace found.", Intent: intent.KindReconnect}, nil
	}

	if target.Connected {
		return &Response{
			ChatReply: fmt.Sprintf("Workspace %q is already connected.", target.Title),
			Intent:    intent.KindReconnect,
		}, nil
	}

	switch target.Type {
	case "mcp":
		return sh.reconnectMCP(ctx, sess, target)
	case "web":
		return sh.reconnectWeb(ctx, sess, target)
	default:
		return &Response{
			ChatReply: fmt.Sprintf("Workspace %q (type %s) does not need reconnection.", target.Title, target.Type),
			Intent:    intent.KindReconnect,
		}, nil
	}
}

// reconnectMCP re-establishes an MCP server connection for a stale workspace.
func (sh *Shell) reconnectMCP(ctx context.Context, _ *Session, ws *workspace.Workspace) (*Response, error) {
	// Extract server URL from workspace content (first line after "**URL:**")
	serverURL := extractURLFromContent(ws.Content)
	if serverURL == "" {
		return &Response{
			ChatReply: fmt.Sprintf("Could not find server URL for workspace %q. Please ingest again.", ws.Title),
			Intent:    intent.KindReconnect,
		}, nil
	}

	conn, err := casmc.Connect(ctx, serverURL)
	if err != nil {
		return &Response{
			ChatReply: fmt.Sprintf("Reconnect failed: %v", err),
			Intent:    intent.KindReconnect,
		}, nil
	}

	// Refresh content and mark connected.
	content := formatMCPWorkspace(conn)
	ws.Content = content
	ws.Connected = true
	sh.mcpConns[ws.ID] = conn
	_ = sh.store.UpdateWorkspace(ws.ID, ws.Title, content)

	return &Response{
		ChatReply: fmt.Sprintf("Reconnected to %s — %d tool(s) available.", serverURL, len(conn.Tools)),
		Workspace: ws,
		Intent:    intent.KindReconnect,
	}, nil
}

// reconnectWeb re-fetches a web workspace that lost its session.
func (sh *Shell) reconnectWeb(ctx context.Context, _ *Session, ws *workspace.Workspace) (*Response, error) {
	pageURL := extractURLFromContent(ws.Content)
	if pageURL == "" {
		return &Response{
			ChatReply: fmt.Sprintf("Could not find URL for workspace %q. Please browse again.", ws.Title),
			Intent:    intent.KindReconnect,
		}, nil
	}

	webSess, err := webview.NewSession(ctx, pageURL)
	if err != nil {
		return &Response{
			ChatReply: fmt.Sprintf("Reconnect failed: %v", err),
			Intent:    intent.KindReconnect,
		}, nil
	}

	page, err := webSess.Navigate(ctx)
	if err != nil {
		webSess.Close()
		return &Response{
			ChatReply: fmt.Sprintf("Reconnect failed: %v", err),
			Intent:    intent.KindReconnect,
		}, nil
	}

	content := webview.FormatPageState(page)
	ws.Content = content
	ws.Connected = true
	sh.webSessions[ws.ID] = webSess
	sh.webPages[ws.ID] = page
	_ = sh.store.UpdateWorkspace(ws.ID, ws.Title, content)

	return &Response{
		ChatReply: fmt.Sprintf("Reconnected to %s.", page.Title),
		Workspace: ws,
		Intent:    intent.KindReconnect,
	}, nil
}

// extractURLFromContent pulls the first http/https URL from workspace content.
// Used by reconnect to find the server/page URL from the stored markdown.
func extractURLFromContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		// Match "**URL:** https://..." pattern used by formatMCPWorkspace and FormatPageState.
		if strings.Contains(line, "**URL:**") {
			parts := strings.SplitN(line, "**URL:**", 2)
			if len(parts) == 2 {
				u := strings.TrimSpace(parts[1])
				if strings.HasPrefix(u, "http") {
					return u
				}
			}
		}
	}
	return ""
}
