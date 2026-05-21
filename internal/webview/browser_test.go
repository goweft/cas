package webview_test

import (
	"strings"
	"testing"

	"github.com/goweft/cas/internal/webview"
)

var sampleHTML = `<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
<h1>Main Heading</h1>
<h2>Sub Heading</h2>
<p>Some body text here.</p>
<a href="https://example.com/page1">Link One</a>
<a href="https://example.com/page2">Link Two</a>
<script>var x = 1;</script>
</body>
</html>`

func TestParseHTMLTitle(t *testing.T) {
	ps := parseTestPage("https://example.com", sampleHTML)
	if ps.Title != "Test Page" {
		t.Errorf("Title = %q, want %q", ps.Title, "Test Page")
	}
}

func TestParseHTMLHeadings(t *testing.T) {
	ps := parseTestPage("https://example.com", sampleHTML)
	if len(ps.Headings) < 2 {
		t.Errorf("expected ≥2 headings, got %d", len(ps.Headings))
	}
	found := false
	for _, h := range ps.Headings {
		if strings.Contains(h, "Main Heading") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Main Heading' in headings, got %v", ps.Headings)
	}
}

func TestParseHTMLLinks(t *testing.T) {
	ps := parseTestPage("https://example.com", sampleHTML)
	if len(ps.Links) < 2 {
		t.Errorf("expected ≥2 links, got %d", len(ps.Links))
	}
	found := false
	for _, l := range ps.Links {
		if l.Text == "Link One" && strings.Contains(l.Href, "page1") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Link One' link, got %+v", ps.Links)
	}
}

func TestParseHTMLExcludesScripts(t *testing.T) {
	ps := parseTestPage("https://example.com", sampleHTML)
	if strings.Contains(ps.Text, "var x = 1") {
		t.Error("page text should not include script content")
	}
}

func TestParseHTMLBodyText(t *testing.T) {
	ps := parseTestPage("https://example.com", sampleHTML)
	if !strings.Contains(ps.Text, "Some body text here") {
		t.Errorf("expected body text in content, got %q", ps.Text)
	}
}

func TestFormatPageState(t *testing.T) {
	ps := &webview.PageState{
		URL:      "https://example.com",
		Title:    "Test",
		Headings: []string{"H1"},
		Links:    []webview.Link{{Text: "Go", Href: "https://go.dev"}},
		Text:     "some text",
	}
	out := webview.FormatPageState(ps)
	if !strings.Contains(out, "# Test") {
		t.Error("expected title heading in output")
	}
	if !strings.Contains(out, "https://example.com") {
		t.Error("expected URL in output")
	}
	if !strings.Contains(out, "H1") {
		t.Error("expected heading in output")
	}
	if !strings.Contains(out, "Go") {
		t.Error("expected link text in output")
	}
}

func TestNewSessionInvalidURL(t *testing.T) {
	_, err := webview.NewSession(nil, "not a url %%")
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
}

func TestNewSessionValidURL(t *testing.T) {
	sess, err := webview.NewSession(nil, "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess.StartURL != "https://example.com" {
		t.Errorf("StartURL = %q, want %q", sess.StartURL, "https://example.com")
	}
	sess.Close() // no-op, should not panic
}

// parseTestPage is a test helper that uses the exported ParseHTML via NewSession+internal.
// Since parseHTML is unexported, we test it via FormatPageState output indirectly.
// For direct parse testing we use the exported FormatPageState with a manually built PageState.
func parseTestPage(u, body string) *webview.PageState {
	// Use the package-internal function indirectly: we can't call parseHTML directly,
	// but we test it end-to-end via the exported API in integration tests.
	// For unit testing the parser directly, expose a ParseForTest helper.
	return webview.ParseForTest(u, []byte(body))
}
