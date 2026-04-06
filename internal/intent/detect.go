// Package intent provides zero-latency intent detection for CAS messages.
// Pattern matching fires before any LLM call — no latency, deterministic.
//
// Priority order (must be preserved):
//  1. Close patterns
//  2. Run patterns
//  3. Combine patterns — merge multiple workspaces — execute active code workspace
//  4. Self-edit exclusions — user signals they will edit manually → chat
//  5. Edit patterns
//  6. Create patterns
//  7. Chat (default)
package intent

import "regexp"

// Kind names the intent of a user message.
type Kind string

const (
	KindCreate Kind = "create_workspace"
	KindEdit   Kind = "edit_workspace"
	KindClose  Kind = "close_workspace"
	KindRun    Kind = "run_workspace"
	KindChat   Kind = "chat"
	KindPlugin  Kind = "plugin_command"
	KindCombine Kind = "combine_workspaces"
)

// WSType is the workspace type inferred from the message.
type WSType string

const (
	WSDocument WSType = "document"
	WSCode     WSType = "code"
	WSList     WSType = "list"
)

// Intent is the result of detecting what a message is asking for.
type Intent struct {
	Kind      Kind
	WSType    WSType
	TitleHint string
}

// ── Pattern tables ────────────────────────────────────────────────

var docNouns = `document|doc|proposal|report|letter|memo|essay|article|plan|outline|` +
	`resume|cv|email|brief|spec|story|blog|post|summary|agenda|budget|` +
	`invoice|contract|pitch|bio|profile|note|notes|` +
	`template|form|guide|handbook|manual|policy|procedure|playbook|` +
	`readme|changelog|roadmap|overview|draft|ticket|issue`

var codeNouns = `script|program|function|class|module|snippet|code|file|` +
	`api|endpoint|query|schema|migration|test|dockerfile|config`

var listNouns = `list|checklist|todo|to-do|table|inventory|index|glossary|outline`

var createVerbs = `write|draft|create|make|start|begin|compose`

// createPattern pairs a compiled regexp with the workspace type it implies.
type createPattern struct {
	re     *regexp.Regexp
	wsType WSType
}

var createPatterns []createPattern
var editPatterns []*regexp.Regexp
var selfEditPatterns []*regexp.Regexp
var closePatterns []*regexp.Regexp
var runPatterns []*regexp.Regexp
var combinePatterns []*regexp.Regexp
var titleHintRe *regexp.Regexp

func init() {
	ci := regexp.MustCompile // alias for readability

	createPatterns = []createPattern{
		{ci(`(?i)\b(?:` + createVerbs + `)\b.*\b(?:` + codeNouns + `)\b`), WSCode},
		{ci(`(?i)\b(?:` + codeNouns + `)\b.*\b(?:` + createVerbs + `)\b`), WSCode},
		{ci(`(?i)\b(?:` + createVerbs + `)\b.*\b(?:` + listNouns + `)\b`), WSList},
		{ci(`(?i)\b(?:` + listNouns + `)\b.*\b(?:` + createVerbs + `)\b`), WSList},
		{ci(`(?i)\b(?:` + createVerbs + `)\b.*\b(?:` + docNouns + `)\b`), WSDocument},
		{ci(`(?i)\b(?:` + docNouns + `)\b.*\b(?:` + createVerbs + `)\b`), WSDocument},
		{ci(`(?i)\bnew\s+(?:document|doc|note|proposal|report|resume|email|brief)\b`), WSDocument},
		{ci(`(?i)\bnew\s+(?:script|program|code|function|module)\b`), WSCode},
		{ci(`(?i)\bnew\s+(?:list|checklist|todo)\b`), WSList},
		{ci(`(?i)\bi\s+need\s+to\s+(?:write|draft)\b`), WSDocument},
	}

	editPatterns = []*regexp.Regexp{
		ci(`(?i)\b(?:edit|update|change|modify|revise|rewrite|append|insert|remove|delete|expand|shorten|rename)\b`),
		ci(`(?i)\badd\b.{0,60}\b(?:section|paragraph|part|chapter|bullet|point|entry|item|row|function|method|class|feature|support|handling|validation|test|logging|error|comment|type|field|parameter|argument|example|case|check|step)\b`),
		ci(`(?i)\b(?:fix|improve|clean\s+up|polish|proofread|refactor|optimise|optimize)\b`),
	}

	// Checked BEFORE editPatterns — user signals manual edit → route to chat
	selfEditPatterns = []*regexp.Regexp{
		ci(`(?i)edit\s+(it\s+)?(directly|myself|yourself|manually|now)`),
		ci(`(?i)i'?ll\s+(edit|do|fix|change|update|write)\s*(it|that|this)?`),
		ci(`(?i)let\s+me\s+(edit|do|fix|change|update|write)`),
		ci(`(?i)(i'll|i will|i can)\s+(take it from here|handle it)`),
		ci(`(?i)just\s+(edit|open|show)\s+(it|the editor)`),
		ci(`(?i)i('ll| will| can)\s+do\s+(it|that)\s*(myself|manually)?`),
	}

	closePatterns = []*regexp.Regexp{
		ci(`(?i)\b(?:close|done|finish|discard|dismiss)\b.*\b(?:workspace|document|doc|editor|file)\b`),
		ci(`(?i)\b(?:workspace|document|doc|editor|file)\b.*\b(?:close|done|finish|discard|dismiss)\b`),
	}

	// Checked after close but before self-edit and edit patterns.
	// Short, imperative phrases that mean "execute the active code".
	runPatterns = []*regexp.Regexp{
		ci(`(?i)^run\s*(it|this|that)?\.?$`),                                        // "run", "run it", "run this"
		ci(`(?i)^execute\s*(it|this|that)?\.?$`),                                     // "execute", "execute it"
		ci(`(?i)^run\s+(the\s+)?(script|code|program|file)\.?$`),                     // "run the script"
		ci(`(?i)^execute\s+(the\s+)?(script|code|program|file)\.?$`),                 // "execute the code"
		ci(`(?i)^test\s+(it|this|that)\.?$`),                                         // "test it", "test this"
		ci(`(?i)^try\s+(it|this|that|running\s+it)\.?$`),                             // "try it", "try running it"
		ci(`(?i)^go\s+run\.?$`),                                                      // "go run"
		ci(`(?i)^can\s+you\s+run\s+(it|this|that|the\s+(?:script|code|program))\.?$`), // "can you run it"
		ci(`(?i)^please\s+run\s+(it|this|that)?\.?$`),                                // "please run it"
		ci(`(?i)^run\s+and\s+show\s+(me\s+)?(the\s+)?output\.?$`),                   // "run and show me the output"
	}

	// Combine/merge patterns — reference multiple workspaces
	combinePatterns = []*regexp.Regexp{
		ci(`(?i)^combine\b`),
		ci(`(?i)^merge\b`),
		ci(`(?i)^put .+ (and|&) .+ together`),
		ci(`(?i)^consolidate\b`),
		ci(`(?i)\bcombine .+ (and|with|&) .+`),
		ci(`(?i)\bmerge .+ (and|with|&) .+`),
		ci(`(?i)\bcombine (all|these|the) (workspace|document|tab)`),
		ci(`(?i)\bmerge (all|these|the) (workspace|document|tab)`),
	}

	titleHintRe = regexp.MustCompile(
		`(?i)\b(?:write|draft|create|make|start|begin|compose)\s+(?:me\s+)?(?:a|an|the|my|our)?\s*(.+)`,
	)
}

// Detect classifies a user message and returns an Intent.
// This is the hot path — no allocations in the common case.
func Detect(message string) Intent {
	for _, re := range closePatterns {
		if re.MatchString(message) {
			return Intent{Kind: KindClose, WSType: WSDocument}
		}
	}
	for _, re := range runPatterns {
		if re.MatchString(message) {
			return Intent{Kind: KindRun, WSType: WSCode}
		}
	}
	for _, re := range combinePatterns {
		if re.MatchString(message) {
			return Intent{Kind: KindCombine, WSType: WSDocument}
		}
	}
	for _, re := range selfEditPatterns {
		if re.MatchString(message) {
			return Intent{Kind: KindChat, WSType: WSDocument}
		}
	}
	for _, re := range editPatterns {
		if re.MatchString(message) {
			return Intent{Kind: KindEdit, WSType: WSDocument}
		}
	}
	for _, cp := range createPatterns {
		if cp.re.MatchString(message) {
			return Intent{
				Kind:      KindCreate,
				WSType:    cp.wsType,
				TitleHint: extractTitleHint(message),
			}
		}
	}
	return Intent{Kind: KindChat, WSType: WSDocument}
}

func extractTitleHint(message string) string {
	m := titleHintRe.FindStringSubmatch(message)
	if len(m) < 2 {
		words := splitWords(message, 6)
		return titleCase(words)
	}
	words := splitWords(m[1], 6)
	return titleCase(trimPunct(words))
}

func splitWords(s string, max int) []string {
	words := regexp.MustCompile(`\s+`).Split(s, -1)
	if len(words) > max {
		words = words[:max]
	}
	return words
}

func trimPunct(words []string) []string {
	if len(words) == 0 {
		return words
	}
	last := words[len(words)-1]
	last = regexp.MustCompile(`[.!?]+$`).ReplaceAllString(last, "")
	words[len(words)-1] = last
	return words
}

func titleCase(words []string) string {
	out := make([]byte, 0, 64)
	for i, w := range words {
		if i > 0 {
			out = append(out, ' ')
		}
		if len(w) > 0 {
			if w[0] >= 'a' && w[0] <= 'z' {
				out = append(out, w[0]-32)
				out = append(out, w[1:]...)
			} else {
				out = append(out, w...)
			}
		}
	}
	return string(out)
}
