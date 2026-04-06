package shell

import (
	"strings"

	"github.com/goweft/cas/internal/workspace"
)

// resolveTarget finds which workspace the user is addressing.
// It fuzzy-matches title fragments from the message against active workspaces.
// Returns the best match, or the most recent workspace if no title match is found.
func resolveTarget(message string, active []*workspace.Workspace) *workspace.Workspace {
	if len(active) == 0 {
		return nil
	}
	if len(active) == 1 {
		return active[0]
	}

	msg := strings.ToLower(message)
	var best *workspace.Workspace
	bestScore := 0

	for _, ws := range active {
		score := titleMatchScore(msg, ws.Title)
		if score > bestScore {
			bestScore = score
			best = ws
		}
	}

	if best != nil {
		return best
	}
	// Default: most recent
	return active[len(active)-1]
}

// resolveAll finds all workspaces referenced in the message.
// Returns at least the target workspace. Used for combine/cross-workspace edits.
func resolveAll(message string, active []*workspace.Workspace) []*workspace.Workspace {
	if len(active) == 0 {
		return nil
	}

	msg := strings.ToLower(message)
	var matched []*workspace.Workspace

	for _, ws := range active {
		if titleMatchScore(msg, ws.Title) > 0 {
			matched = append(matched, ws)
		}
	}

	// "all workspaces" / "everything" / "all of them" → return all
	if strings.Contains(msg, "all workspace") ||
		strings.Contains(msg, "all of them") ||
		strings.Contains(msg, "everything") ||
		strings.Contains(msg, "every workspace") {
		return active
	}

	if len(matched) == 0 {
		// No title matches — return all active (assume user means everything)
		return active
	}
	return matched
}

// titleMatchScore returns how well a workspace title matches the message.
// 0 = no match. Higher = better match.
func titleMatchScore(messageLower string, title string) int {
	titleLower := strings.ToLower(title)

	// Exact title in message: "update the Project Proposal"
	if strings.Contains(messageLower, titleLower) {
		return 100 + len(title) // longer exact matches score higher
	}

	// Match individual title words (skip short/common words)
	words := strings.Fields(titleLower)
	matched := 0
	for _, w := range words {
		if len(w) < 3 {
			continue // skip "a", "my", "the", etc.
		}
		if strings.Contains(messageLower, w) {
			matched++
		}
	}

	if matched == 0 {
		return 0
	}

	// Score: fraction of title words found in message
	significant := 0
	for _, w := range words {
		if len(w) >= 3 {
			significant++
		}
	}
	if significant == 0 {
		return 0
	}

	return (matched * 50) / significant
}
