package intent_test

import (
	"testing"

	"github.com/goweft/cas/internal/intent"
)

func TestDetectCreate(t *testing.T) {
	cases := []struct {
		msg    string
		wsType intent.WSType
	}{
		{"write a project proposal", intent.WSDocument},
		{"draft a resume for a software engineer", intent.WSDocument},
		{"create a python script", intent.WSCode},
		{"write a function to parse JSON", intent.WSCode},
		{"make a todo list", intent.WSList},
		{"create a checklist for deployment", intent.WSList},
	}
	for _, tc := range cases {
		t.Run(tc.msg, func(t *testing.T) {
			got := intent.Detect(tc.msg)
			if got.Kind != intent.KindCreate {
				t.Errorf("expected KindCreate, got %q", got.Kind)
			}
			if got.WSType != tc.wsType {
				t.Errorf("expected wsType %q, got %q", tc.wsType, got.WSType)
			}
		})
	}
}

func TestDetectEdit(t *testing.T) {
	cases := []string{
		"add a section about budget",
		"fix the introduction",
		"rewrite the summary",
		"improve the conclusion",
		// These were broken before the noun-list expansion:
		"add error handling",
		"add file input support",
		"add logging to the function",
		"add a validation step",
		"add type annotations",
		"add an example",
		"add test coverage",
		"improve error messages",
		"fix the error handling",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind != intent.KindEdit {
				t.Errorf("expected KindEdit, got %q for message %q", got.Kind, msg)
			}
		})
	}
}

func TestDetectSelfEdit(t *testing.T) {
	// Self-edit phrases must route to chat, not edit
	cases := []string{
		"edit it directly",
		"I'll edit it myself",
		"let me fix it",
		"I'll do that",
		"just open the editor",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind != intent.KindChat {
				t.Errorf("expected KindChat (self-edit), got %q for %q", got.Kind, msg)
			}
		})
	}
}

func TestDetectClose(t *testing.T) {
	cases := []string{
		"close the workspace",
		"done with this document",
		"discard the editor",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind != intent.KindClose {
				t.Errorf("expected KindClose, got %q", got.Kind)
			}
		})
	}
}

func TestDetectChat(t *testing.T) {
	cases := []string{
		"hello",
		"how are you",
		"what can you do",
		"add me a coffee",         // "add" without edit-target noun → chat
		"add it to my grocery list", // ambiguous but no workspace context
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind != intent.KindChat {
				t.Errorf("expected KindChat, got %q for %q", got.Kind, msg)
			}
		})
	}
}

func TestSelfEditBeforeEditPattern(t *testing.T) {
	// "edit it directly" contains the word "edit" which matches _EDIT_PATTERNS[0]
	// but _SELF_EDIT_PATTERNS must be checked first — result must be KindChat
	got := intent.Detect("edit it directly")
	if got.Kind != intent.KindChat {
		t.Errorf("self-edit exclusion must take priority over edit patterns, got %q", got.Kind)
	}
}

func TestTitleHint(t *testing.T) {
	got := intent.Detect("write a project proposal for Q3")
	if got.TitleHint == "" {
		t.Error("expected non-empty TitleHint for create intent")
	}
}

func TestTitleHintAbsentForNonCreate(t *testing.T) {
	cases := []string{"hello", "add a section", "close the workspace"}
	for _, msg := range cases {
		got := intent.Detect(msg)
		if got.Kind == intent.KindCreate {
			continue // only check non-create
		}
		if got.TitleHint != "" {
			t.Errorf("expected empty TitleHint for %q (%s), got %q", msg, got.Kind, got.TitleHint)
		}
	}
}


// ── Run intent tests ──────────────────────────────────────────────

func TestRunIntent(t *testing.T) {
	cases := []struct {
		message string
		want    intent.Kind
	}{
		{"run", intent.KindRun},
		{"run it", intent.KindRun},
		{"run this", intent.KindRun},
		{"run that", intent.KindRun},
		{"execute", intent.KindRun},
		{"execute it", intent.KindRun},
		{"run the script", intent.KindRun},
		{"run the code", intent.KindRun},
		{"execute the program", intent.KindRun},
		{"test it", intent.KindRun},
		{"test this", intent.KindRun},
		{"try it", intent.KindRun},
		{"try running it", intent.KindRun},
		{"go run", intent.KindRun},
		{"can you run it", intent.KindRun},
		{"can you run the script", intent.KindRun},
		{"please run it", intent.KindRun},
		{"run and show me the output", intent.KindRun},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind != tc.want {
				t.Errorf("Detect(%q) = %q, want %q", tc.message, got.Kind, tc.want)
			}
		})
	}
}

func TestRunIntentDoesNotMatchConversational(t *testing.T) {
	// These should NOT be detected as run intent
	cases := []struct {
		message string
		want    intent.Kind
	}{
		{"how do I run this on my server?", intent.KindChat},
		{"can you write a script to run the tests?", intent.KindCreate},
		{"I need to run some errands", intent.KindChat},
		{"the program won't run on Windows", intent.KindChat},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind != tc.want {
				t.Errorf("Detect(%q) = %q, want %q", tc.message, got.Kind, tc.want)
			}
		})
	}
}

func TestRunBeforeSelfEdit(t *testing.T) {
	// "run it" should match run, not fall through to anything else
	got := intent.Detect("run it")
	if got.Kind != intent.KindRun {
		t.Errorf("expected KindRun for 'run it', got %q", got.Kind)
	}
}


// ── Combine intent tests ──────────────────────────────────────────

func TestCombineIntent(t *testing.T) {
	cases := []struct {
		message string
		want    intent.Kind
	}{
		{"combine the proposal and the checklist", intent.KindCombine},
		{"merge the proposal and the script", intent.KindCombine},
		{"combine all workspaces", intent.KindCombine},
		{"merge all documents", intent.KindCombine},
		{"merge these workspaces", intent.KindCombine},
		{"put the proposal and checklist together", intent.KindCombine},
		{"consolidate everything", intent.KindCombine},
		{"combine report with the notes", intent.KindCombine},
		{"merge notes and summary", intent.KindCombine},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind != tc.want {
				t.Errorf("Detect(%q) = %q, want %q", tc.message, got.Kind, tc.want)
			}
		})
	}
}

func TestCombineDoesNotMatchNormal(t *testing.T) {
	cases := []struct {
		message string
	}{
		{"how do I combine these colors?"},
		{"write a proposal"},
		{"add a section"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind == intent.KindCombine {
				t.Errorf("Detect(%q) should not be KindCombine", tc.message)
			}
		})
	}
}

func TestIngestDetection(t *testing.T) {
	cases := []struct {
		message string
		wantURL string
	}{
		{"ingest http://localhost:3000/sse", "http://localhost:3000/sse"},
		{"ingest https://mcp.example.com/api", "https://mcp.example.com/api"},
		{"connect to https://mcp.linear.app/sse", "https://mcp.linear.app/sse"},
		{"connect https://api.example.com/mcp", "https://api.example.com/mcp"},
		{"add mcp server https://tools.internal/sse", "https://tools.internal/sse"},
		{"add server https://tools.internal/sse", "https://tools.internal/sse"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind != intent.KindIngest {
				t.Errorf("Detect(%q) = %q, want KindIngest", tc.message, got.Kind)
			}
			if got.TitleHint != tc.wantURL {
				t.Errorf("Detect(%q).TitleHint = %q, want %q", tc.message, got.TitleHint, tc.wantURL)
			}
		})
	}
}

func TestIngestDoesNotMatchNormal(t *testing.T) {
	cases := []string{
		"write a document",
		"ingest some data",         // no URL
		"connect the dots",         // no URL
		"add a section",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind == intent.KindIngest {
				t.Errorf("Detect(%q) should not be KindIngest", msg)
			}
		})
	}
}

func TestBrowseDetection(t *testing.T) {
	cases := []struct {
		message string
		wantURL string
	}{
		{"browse https://example.com", "https://example.com"},
		{"open https://golang.org", "https://golang.org"},
		{"scrape https://news.ycombinator.com", "https://news.ycombinator.com"},
		{"fetch https://api.example.com/data", "https://api.example.com/data"},
		{"read https://docs.example.com/guide", "https://docs.example.com/guide"},
		{"summarise https://example.com/article", "https://example.com/article"},
		{"summarize https://example.com/article", "https://example.com/article"},
		{"go to https://example.com/page", "https://example.com/page"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind != intent.KindBrowse {
				t.Errorf("Detect(%q) = %q, want KindBrowse", tc.message, got.Kind)
			}
			if got.TitleHint != tc.wantURL {
				t.Errorf("Detect(%q).TitleHint = %q, want %q", tc.message, got.TitleHint, tc.wantURL)
			}
		})
	}
}

func TestBrowseDoesNotMatchNormal(t *testing.T) {
	cases := []string{
		"write a document",
		"open a new file",     // no URL
		"fetch some data",     // no URL
		"read the report",     // no URL
		"go to sleep",         // no URL
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind == intent.KindBrowse {
				t.Errorf("Detect(%q) should not be KindBrowse", msg)
			}
		})
	}
}

func TestOrchestrateDetection(t *testing.T) {
	cases := []string{
		"using the linear workspace, create a github PR",
		"use the issues list to draft a PR description",
		"from the linear issue and create a PR",
		"take the issue and file a pull request",
		"read the linear issue and update the PR draft",
		"get the issue then create a PR",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind != intent.KindOrchestrate {
				t.Errorf("Detect(%q) = %q, want KindOrchestrate", msg, got.Kind)
			}
		})
	}
}

func TestOrchestrateDoesNotMatchNormal(t *testing.T) {
	cases := []string{
		"write a document",
		"edit the proposal",
		"combine the notes",
		"what is the capital of France",
		"use good grammar in this document",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind == intent.KindOrchestrate {
				t.Errorf("Detect(%q) should not be KindOrchestrate", msg)
			}
		})
	}
}

func TestReconnectDetection(t *testing.T) {
	cases := []struct {
		message string
	}{
		{"reconnect"},
		{"reconnect to the linear workspace"},
		{"reconnect workspace"},
		{"reconnect linear"},
		{"re-connect"},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.Kind != intent.KindReconnect {
				t.Errorf("Detect(%q) = %q, want KindReconnect", tc.message, got.Kind)
			}
		})
	}
}

func TestReconnectTitleHintExtraction(t *testing.T) {
	cases := []struct {
		message   string
		wantHint  string
	}{
		{"reconnect to the linear workspace", "the linear workspace"},
		{"reconnect linear", "linear"},
		{"reconnect", ""},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			got := intent.Detect(tc.message)
			if got.TitleHint != tc.wantHint {
				t.Errorf("Detect(%q).TitleHint = %q, want %q", tc.message, got.TitleHint, tc.wantHint)
			}
		})
	}
}

func TestReconnectDoesNotMatchNormal(t *testing.T) {
	cases := []string{
		"connect to a server",     // no URL but not reconnect
		"write a document",
		"edit the notes",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			got := intent.Detect(msg)
			if got.Kind == intent.KindReconnect {
				t.Errorf("Detect(%q) should not be KindReconnect", msg)
			}
		})
	}
}
