package shell

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/goweft/cas/internal/llm"
	"github.com/goweft/cas/internal/store"
	"github.com/goweft/cas/internal/workspace"
)

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
