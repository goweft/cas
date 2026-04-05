// Package conductor implements behavioral learning for CAS.
//
// The Conductor observes shell interactions over time and builds a persistent
// user profile stored at ~/.cas/profile.json. On each interaction it updates
// the profile and derives a natural-language context string that is appended
// to LLM system prompts, progressively calibrating CAS to the specific user
// without requiring explicit configuration.
//
// All methods are fail-safe: a corrupt or missing profile never breaks the
// shell; observe errors are logged and silently discarded.
package conductor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Thresholds before context generation starts.
const (
	minWorkspaces = 1
	minMessages   = 2
	maxPhrases    = 30
	maxCtxTypes   = 4
	maxCtxVerbs   = 3
)

// Profile is the persisted behavioral learning state.
type Profile struct {
	DocTypes       map[string]int `json:"doc_types"`       // content-type nouns
	WSTypes        map[string]int `json:"ws_types"`        // document/code/list counts
	EditVerbs      map[string]int `json:"edit_verbs"`      // edit verb counts
	Phrases        []string       `json:"phrases"`         // recent create-intent messages
	SessionCount   int            `json:"session_count"`
	MessageCount   int            `json:"message_count"`
	WorkspaceCount int            `json:"workspace_count"` // workspaces created
	EditCount      int            `json:"edit_count"`
	LastSeen       string         `json:"last_seen"` // RFC3339
}

func defaultProfile() Profile {
	return Profile{
		DocTypes:  make(map[string]int),
		WSTypes:   make(map[string]int),
		EditVerbs: make(map[string]int),
		Phrases:   []string{},
	}
}

// Conductor observes CAS interactions and builds a persistent user profile.
type Conductor struct {
	path    string
	profile Profile
}

// New returns a Conductor backed by the given profile path.
// Pass an empty string to use the default ~/.cas/profile.json.
func New(profilePath string) *Conductor {
	if profilePath == "" {
		home, _ := os.UserHomeDir()
		profilePath = filepath.Join(home, ".cas", "profile.json")
	}
	c := &Conductor{path: profilePath}
	c.profile = c.load()
	return c
}

// ── Persistence ───────────────────────────────────────────────────

func (c *Conductor) load() Profile {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return defaultProfile()
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		log.Printf("conductor: corrupt profile, resetting: %v", err)
		return defaultProfile()
	}
	// Ensure maps are non-nil (old profiles may predate a field)
	if p.DocTypes == nil {
		p.DocTypes = make(map[string]int)
	}
	if p.WSTypes == nil {
		p.WSTypes = make(map[string]int)
	}
	if p.EditVerbs == nil {
		p.EditVerbs = make(map[string]int)
	}
	if p.Phrases == nil {
		p.Phrases = []string{}
	}
	return p
}

func (c *Conductor) save() {
	if err := os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		log.Printf("conductor: mkdir: %v", err)
		return
	}
	data, err := json.MarshalIndent(c.profile, "", "  ")
	if err != nil {
		log.Printf("conductor: marshal: %v", err)
		return
	}
	if err := os.WriteFile(c.path, data, 0600); err != nil {
		log.Printf("conductor: write: %v", err)
	}
}

// ── Patterns ──────────────────────────────────────────────────────

var editVerbRe = regexp.MustCompile(
	`(?i)\b(add|append|insert|revise|rewrite|remove|delete|update|change|modify|` +
		`expand|shorten|summarise|summarize|fix|improve|rename|refactor|` +
		`clean|polish|proofread|reorganize|restructure)\b`,
)

var docTypeRe = regexp.MustCompile(
	`(?i)\b(resume|cv|letter|proposal|report|memo|essay|article|plan|outline|` +
		`note|brief|spec|story|blog|post|summary|agenda|budget|invoice|` +
		`contract|script|pitch|bio|profile|email|list|document|doc|` +
		`function|program|class|module|test|query|schema)\b`,
)

// ── Observation ───────────────────────────────────────────────────

// Observe records one shell interaction.
// intentKind is one of "create_workspace", "edit_workspace", "close_workspace", "chat".
func (c *Conductor) Observe(intentKind, message, workspaceTitle, wsType string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("conductor: observe panic: %v", r)
		}
		c.save()
	}()

	c.profile.MessageCount++
	c.profile.LastSeen = time.Now().UTC().Format(time.RFC3339)

	switch intentKind {
	case "create_workspace":
		c.observeCreate(message, workspaceTitle, wsType)
	case "edit_workspace":
		c.observeEdit(message)
	}
}

// ObserveSessionStart increments the session counter.
func (c *Conductor) ObserveSessionStart() {
	defer c.save()
	c.profile.SessionCount++
}

func (c *Conductor) observeCreate(message, title, wsType string) {
	c.profile.WorkspaceCount++

	if wsType != "" {
		c.profile.WSTypes[wsType]++
	}

	// Extract topic nouns — deduplicated per message, prefer last (most specific)
	found := dedupeLower(docTypeRe.FindAllString(message, -1))
	if len(found) > 0 {
		primary := found[len(found)-1]
		c.profile.DocTypes[primary]++
	}

	// Also observe from title if it adds a different noun
	if title != "" {
		titleFound := dedupeLower(docTypeRe.FindAllString(title, -1))
		if len(titleFound) > 0 {
			t := titleFound[len(titleFound)-1]
			if len(found) == 0 || t != found[len(found)-1] {
				c.profile.DocTypes[t]++
			}
		}
	}

	// Phrase ring buffer
	c.profile.Phrases = append(c.profile.Phrases, strings.TrimSpace(message))
	if len(c.profile.Phrases) > maxPhrases {
		c.profile.Phrases = c.profile.Phrases[len(c.profile.Phrases)-maxPhrases:]
	}
}

func (c *Conductor) observeEdit(message string) {
	c.profile.EditCount++
	verbs := dedupeLower(editVerbRe.FindAllString(message, -1))
	for _, v := range verbs {
		c.profile.EditVerbs[v]++
	}
}

// ── Context generation ────────────────────────────────────────────

// UserContext returns a natural-language string for LLM system prompts.
// Returns empty string when there is insufficient signal.
func (c *Conductor) UserContext() string {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("conductor: user_context panic: %v", r)
		}
	}()
	return c.buildContext()
}

func (c *Conductor) buildContext() string {
	p := c.profile
	if p.WorkspaceCount < minWorkspaces && p.MessageCount < minMessages {
		return ""
	}

	var parts []string

	// Workspace type preferences
	wsTypes := topN(p.WSTypes, 3)
	docTypes := topN(p.DocTypes, maxCtxTypes)

	if len(wsTypes) > 0 {
		dominant := wsTypes[0].key
		if len(wsTypes) == 1 {
			parts = append(parts, "This user primarily creates "+dominant+" workspaces.")
		} else {
			var items []string
			for _, kv := range wsTypes {
				items = append(items, kv.key)
			}
			parts = append(parts, "This user creates: "+strings.Join(items, ", ")+" workspaces.")
		}
	}

	// Content topic preferences
	if len(docTypes) > 0 {
		tops := make([]string, len(docTypes))
		for i, kv := range docTypes {
			tops[i] = kv.key
		}
		if len(tops) == 1 {
			parts = append(parts, "Their documents are primarily "+tops[0]+"s.")
		} else {
			last := tops[len(tops)-1]
			rest := tops[:len(tops)-1]
			parts = append(parts, "Their documents are typically "+strings.Join(rest, ", ")+" and "+last+"s.")
		}
	}

	// Edit style
	editVerbs := topN(p.EditVerbs, maxCtxVerbs)
	if len(editVerbs) > 0 && p.EditCount > 0 {
		verbSet := make(map[string]bool)
		for _, kv := range editVerbs {
			verbSet[kv.key] = true
		}
		switch {
		case verbSet["rewrite"] || verbSet["revise"]:
			parts = append(parts, "They prefer full rewrites over incremental edits.")
		case verbSet["shorten"] || verbSet["summarize"] || verbSet["summarise"]:
			parts = append(parts, "They frequently ask to shorten or condense content.")
		case verbSet["add"] || verbSet["append"] || verbSet["insert"]:
			parts = append(parts, "They prefer adding sections over rewriting existing content.")
		case verbSet["fix"] || verbSet["improve"] || verbSet["proofread"]:
			parts = append(parts, "They frequently ask for quality improvements and fixes.")
		}
	}

	// Return user signal
	if p.SessionCount > 1 {
		ws := "workspace"
		if p.WorkspaceCount != 1 {
			ws = "workspaces"
		}
		parts = append(parts, "Returning user: "+itoa(p.SessionCount)+" sessions, "+itoa(p.WorkspaceCount)+" "+ws+" created.")
	}

	return strings.Join(parts, " ")
}

// ── Introspection ─────────────────────────────────────────────────

// ProfileSummary returns the profile plus the current context string.
func (c *Conductor) ProfileSummary() map[string]interface{} {
	ctx := c.UserContext()
	return map[string]interface{}{
		"doc_types":       c.profile.DocTypes,
		"ws_types":        c.profile.WSTypes,
		"edit_verbs":      c.profile.EditVerbs,
		"session_count":   c.profile.SessionCount,
		"message_count":   c.profile.MessageCount,
		"workspace_count": c.profile.WorkspaceCount,
		"edit_count":      c.profile.EditCount,
		"last_seen":       c.profile.LastSeen,
		"context":         ctx,
		"has_context":     ctx != "",
	}
}

// Reset wipes the profile and saves.
func (c *Conductor) Reset() {
	c.profile = defaultProfile()
	c.save()
}

// ── Helpers ───────────────────────────────────────────────────────

type kv struct {
	key   string
	count int
}

// topN returns the top n entries from a frequency map, sorted by count desc.
func topN(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].key < out[j].key // stable alphabetical tiebreak
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// dedupeLower lowercases and deduplicates a slice preserving order.
func dedupeLower(words []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, w := range words {
		l := strings.ToLower(w)
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte(n%10) + '0'
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
