// Package webview provides the CAS web ingestion layer.
//
// WebView mode ingests a URL as a workspace by fetching the page over HTTP
// and extracting its readable structure. This gives the WebAgent authoritative
// access to page content without requiring a headless browser runtime.
//
// For CAS — a terminal TUI — this is the correct implementation: the workspace
// panel renders the page's extracted content as structured markdown, and the
// WebAgent can re-fetch or navigate to derived URLs as needed.
//
// A Session represents a live web context for a single ingested URL.
// It tracks the current URL and can navigate to linked pages within the
// same origin (or any URL the user specifies).
package webview

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

const (
	defaultTimeout = 20 * time.Second
	maxBodyBytes   = 512 * 1024 // 512 KB read limit
	maxTextBytes   = 4096       // workspace display limit
)

// PageState is a snapshot of a fetched page.
type PageState struct {
	URL      string
	Title    string
	Headings []string
	Links    []Link
	Text     string // visible body text, truncated for display
}

// Link is a hyperlink extracted from the page.
type Link struct {
	Text string
	Href string
}

// Session represents a live web context for an ingested URL.
// It tracks navigation history and provides page fetching.
type Session struct {
	StartURL   string
	CurrentURL string
	client     *http.Client
}

// NewSession creates a Session for the given start URL.
// No network call is made until Navigate or Fetch is called.
func NewSession(_ context.Context, startURL string) (*Session, error) {
	if _, err := url.Parse(startURL); err != nil {
		return nil, fmt.Errorf("webview: invalid URL %q: %w", startURL, err)
	}
	return &Session{
		StartURL:   startURL,
		CurrentURL: startURL,
		client: &http.Client{
			Timeout: defaultTimeout,
		},
	}, nil
}

// Fetch retrieves and parses the given URL, updating the session's current URL.
func (s *Session) Fetch(ctx context.Context, targetURL string) (*PageState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("webview fetch %s: %w", targetURL, err)
	}
	req.Header.Set("User-Agent", "CAS/0.1 (Conversational Agent Shell)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webview fetch %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("webview: %s returned HTTP %d", targetURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("webview read body: %w", err)
	}

	ps, err := parseHTML(targetURL, body)
	if err != nil {
		return nil, err
	}
	s.CurrentURL = targetURL
	return ps, nil
}

// Navigate fetches the start URL and returns the page state.
func (s *Session) Navigate(ctx context.Context) (*PageState, error) {
	return s.Fetch(ctx, s.StartURL)
}

// parseHTML parses raw HTML bytes and extracts a PageState.
func parseHTML(pageURL string, body []byte) (*PageState, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("webview parse %s: %w", pageURL, err)
	}

	ps := &PageState{URL: pageURL}
	base, _ := url.Parse(pageURL)

	var walk func(*html.Node)
	var textBuf strings.Builder
	var inScript, inStyle bool

	walk = func(n *html.Node) {
		switch n.Type {
		case html.ElementNode:
			tag := strings.ToLower(n.Data)
			switch tag {
			case "script":
				inScript = true
				defer func() { inScript = false }()
				return
			case "style":
				inStyle = true
				defer func() { inStyle = false }()
				return
			case "title":
				if c := n.FirstChild; c != nil && c.Type == html.TextNode {
					ps.Title = strings.TrimSpace(c.Data)
				}
			case "h1", "h2", "h3":
				var hb strings.Builder
				collectText(n, &hb)
				if t := strings.TrimSpace(hb.String()); t != "" {
					ps.Headings = append(ps.Headings, t)
				}
			case "a":
				href := attrVal(n, "href")
				if href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") {
					if u, err := base.Parse(href); err == nil {
						var lb strings.Builder
						collectText(n, &lb)
						text := strings.TrimSpace(lb.String())
						if text != "" && len(ps.Links) < 30 {
							ps.Links = append(ps.Links, Link{Text: text, Href: u.String()})
						}
					}
				}
			}
		case html.TextNode:
			if !inScript && !inStyle && textBuf.Len() < maxTextBytes {
				t := strings.TrimFunc(n.Data, unicode.IsSpace)
				if t != "" {
					if textBuf.Len() > 0 {
						textBuf.WriteByte(' ')
					}
					textBuf.WriteString(t)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	text := textBuf.String()
	if len(text) > maxTextBytes {
		text = text[:maxTextBytes] + "\n… (truncated)"
	}
	ps.Text = text
	return ps, nil
}

func collectText(n *html.Node, sb *strings.Builder) {
	if n.Type == html.TextNode {
		sb.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectText(c, sb)
	}
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// FormatPageState produces a markdown representation of a PageState
// suitable for use as workspace content.
func FormatPageState(ps *PageState) string {
	var sb strings.Builder
	title := ps.Title
	if title == "" {
		title = ps.URL
	}
	sb.WriteString("# " + title + "\n\n")
	sb.WriteString("**URL:** " + ps.URL + "\n\n")

	if len(ps.Headings) > 0 {
		sb.WriteString("## Structure\n\n")
		for _, h := range ps.Headings {
			sb.WriteString("- " + h + "\n")
		}
		sb.WriteString("\n")
	}

	if len(ps.Links) > 0 {
		sb.WriteString("## Links\n\n")
		for _, l := range ps.Links {
			sb.WriteString(fmt.Sprintf("- [%s](%s)\n", l.Text, l.Href))
		}
		sb.WriteString("\n")
	}

	if ps.Text != "" {
		sb.WriteString("## Content\n\n")
		sb.WriteString(ps.Text + "\n")
	}

	return sb.String()
}

// Close is a no-op for HTTP sessions (no persistent connection to close).
// Present for interface symmetry with MCP Connection.
func (s *Session) Close() {}

// ParseForTest exposes parseHTML for package-level testing.
// Not intended for use outside tests.
func ParseForTest(pageURL string, body []byte) *PageState {
	ps, err := parseHTML(pageURL, body)
	if err != nil {
		return &PageState{URL: pageURL}
	}
	return ps
}
