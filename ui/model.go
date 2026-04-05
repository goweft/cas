// Package ui implements the CAS terminal interface using Bubble Tea.
//
// Layout: split panel — chat (40%) left, tabbed workspace (60%) right.
//
// Tabs: each open workspace is a tab. '[' / ']' navigate tabs when the
// workspace panel is focused. A placeholder tab is created at submit time
// for create intents so streaming content lands in the right place.
//
// Streaming: a buffered channel stored in Model feeds one event per
// tea.Cmd tick into the event loop (correct Bubble Tea pattern).
package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/goweft/cas/internal/intent"
	"github.com/goweft/cas/internal/shell"
	"github.com/goweft/cas/internal/workspace"
)

// ── Palette ───────────────────────────────────────────────────────

var (
	colBorder    = lipgloss.AdaptiveColor{Light: "#C8C6C0", Dark: "#383838"}
	colActive    = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	colWorkspace = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	colDim       = lipgloss.AdaptiveColor{Light: "#9B9B9B", Dark: "#5C5C5C"}

	stylePanel       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colBorder).Padding(0, 1)
	styleActivePanel = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colActive).Padding(0, 1)
	styleWSPanel     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colWorkspace).Padding(0, 1)

	styleTitle  = lipgloss.NewStyle().Foreground(colActive).Bold(true)
	styleWSType = lipgloss.NewStyle().Foreground(colWorkspace).Italic(true)
	styleDim    = lipgloss.NewStyle().Foreground(colDim)
	styleUser   = lipgloss.NewStyle().Foreground(lipgloss.Color("#79c0ff"))
	styleShell  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7ee787"))
	styleInput  = lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3"))
	styleStatus = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	styleCode   = lipgloss.NewStyle().Foreground(lipgloss.Color("#e6edf3"))

	styleTabActive = lipgloss.NewStyle().
			Foreground(colWorkspace).
			Bold(true).
			Padding(0, 1)

	styleTabInactive = lipgloss.NewStyle().
				Foreground(colDim).
				Padding(0, 1)
)

// ── Tab state ─────────────────────────────────────────────────────

// tabState holds the display state for one workspace tab.
// ws is nil while the workspace is still being generated (placeholder tab).
type tabState struct {
	ws      *workspace.Workspace // nil until generation completes
	title   string               // display title (placeholder or confirmed)
	wsType  string               // "document" | "code" | "list"
	content string               // current displayed content
	scroll  int                  // scroll offset in lines
}

func tabFromWorkspace(ws *workspace.Workspace) tabState {
	return tabState{
		ws:      ws,
		title:   ws.Title,
		wsType:  ws.Type,
		content: ws.Content,
	}
}

// ── Stream event ──────────────────────────────────────────────────

type streamEvent struct {
	Token string
	Resp  *shell.StreamResponse
	Err   error
}

// ── Tea messages ──────────────────────────────────────────────────

type tokenMsg string

type responseMsg struct {
	resp *shell.StreamResponse
	err  error
}

// ── Focus ─────────────────────────────────────────────────────────

type Focus int

const (
	FocusChat      Focus = iota
	FocusWorkspace Focus = iota
)

// ── Model ─────────────────────────────────────────────────────────

type Model struct {
	sh        *shell.Shell
	sessionID string

	// Chat
	messages   []shell.Message
	input      string
	chatScroll int

	// Workspace tabs
	tabs      []tabState
	activeTab int

	// Streaming
	streaming bool
	streamBuf strings.Builder
	streamCh  chan streamEvent

	// Layout
	width  int
	height int
	focus  Focus

	// Status
	status string
}

// New creates a model seeded with existing session state.
// workspaces should be the active (non-closed) workspaces in creation order;
// the last one becomes the active tab.
func New(sh *shell.Shell, sessionID string, history []shell.Message, workspaces []*workspace.Workspace) Model {
	m := Model{
		sh:        sh,
		sessionID: sessionID,
		messages:  history,
		focus:     FocusChat,
	}
	for _, ws := range workspaces {
		m.tabs = append(m.tabs, tabFromWorkspace(ws))
	}
	if len(m.tabs) > 0 {
		m.activeTab = len(m.tabs) - 1
	}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// ── Update ────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tokenMsg:
		m.streamBuf.WriteString(string(msg))
		// Update the active tab's content live as tokens arrive
		if m.activeTab < len(m.tabs) {
			m.tabs[m.activeTab].content = m.streamBuf.String()
		}
		return m, listenStream(m.streamCh)

	case responseMsg:
		return m.handleResponse(msg)
	}

	return m, nil
}

func (m Model) handleResponse(msg responseMsg) (Model, tea.Cmd) {
	m.streaming = false
	m.streamCh = nil
	m.status = ""

	if msg.err != nil {
		m.status = "error: " + msg.err.Error()
		// Remove placeholder tab if we added one
		if m.activeTab < len(m.tabs) && m.tabs[m.activeTab].ws == nil {
			m.tabs = append(m.tabs[:m.activeTab], m.tabs[m.activeTab+1:]...)
			m.activeTab = clamp(m.activeTab-1, 0, len(m.tabs)-1)
		}
		return m, nil
	}

	resp := msg.resp
	m.messages = append(m.messages, shell.Message{Role: "shell", Text: resp.ChatReply})

	if resp.Workspace != nil {
		ws := resp.Workspace

		if resp.Intent == intent.KindClose {
			// Remove the closed workspace's tab
			for i, tab := range m.tabs {
				if tab.ws != nil && tab.ws.ID == ws.ID {
					m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
					m.activeTab = clamp(m.activeTab, 0, len(m.tabs)-1)
					break
				}
			}
		} else {
			// Find existing confirmed tab for this workspace ID
			found := -1
			for i, tab := range m.tabs {
				if tab.ws != nil && tab.ws.ID == ws.ID {
					found = i
					break
				}
			}
			if found >= 0 {
				// Update existing confirmed tab (edit)
				m.tabs[found].ws = ws
				m.tabs[found].title = ws.Title
				m.tabs[found].content = ws.Content
				m.activeTab = found
			} else if m.activeTab < len(m.tabs) && m.tabs[m.activeTab].ws == nil {
				// Confirm the placeholder tab (create)
				m.tabs[m.activeTab].ws = ws
				m.tabs[m.activeTab].title = ws.Title
				m.tabs[m.activeTab].wsType = ws.Type
				m.tabs[m.activeTab].content = ws.Content
			} else {
				// Fallback: no placeholder found, append new tab
				m.tabs = append(m.tabs, tabFromWorkspace(ws))
				m.activeTab = len(m.tabs) - 1
			}
		}
	}

	m.streamBuf.Reset()
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {

	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEsc:
		if m.focus == FocusWorkspace {
			m.focus = FocusChat
		} else {
			return m, tea.Quit
		}
		return m, nil

	case tea.KeyTab:
		if m.focus == FocusChat {
			m.focus = FocusWorkspace
		} else {
			m.focus = FocusChat
		}
		return m, nil

	case tea.KeyEnter:
		if m.focus != FocusChat || m.streaming || strings.TrimSpace(m.input) == "" {
			return m, nil
		}
		return m.submitMessage()

	case tea.KeyBackspace:
		if m.focus == FocusChat && len(m.input) > 0 {
			runes := []rune(m.input)
			m.input = string(runes[:len(runes)-1])
		}
		return m, nil

	case tea.KeyUp:
		switch m.focus {
		case FocusWorkspace:
			if m.activeTab < len(m.tabs) && m.tabs[m.activeTab].scroll > 0 {
				m.tabs[m.activeTab].scroll--
			}
		case FocusChat:
			if m.chatScroll < len(m.messages) {
				m.chatScroll++
			}
		}
		return m, nil

	case tea.KeyDown:
		switch m.focus {
		case FocusWorkspace:
			if m.activeTab < len(m.tabs) {
				m.tabs[m.activeTab].scroll++
			}
		case FocusChat:
			if m.chatScroll > 0 {
				m.chatScroll--
			}
		}
		return m, nil

	case tea.KeyPgUp:
		if m.focus == FocusWorkspace && m.activeTab < len(m.tabs) {
			m.tabs[m.activeTab].scroll -= 10
			if m.tabs[m.activeTab].scroll < 0 {
				m.tabs[m.activeTab].scroll = 0
			}
		}
		return m, nil

	case tea.KeyPgDown:
		if m.focus == FocusWorkspace && m.activeTab < len(m.tabs) {
			m.tabs[m.activeTab].scroll += 10
		}
		return m, nil

	case tea.KeyRunes:
		// Tab navigation — only in workspace focus, not during text input
		if m.focus == FocusWorkspace {
			switch string(msg.Runes) {
			case "[":
				if m.activeTab > 0 {
					m.activeTab--
				}
				return m, nil
			case "]":
				if m.activeTab < len(m.tabs)-1 {
					m.activeTab++
				}
				return m, nil
			}
		}
		if m.focus == FocusChat && !m.streaming {
			m.input += string(msg.Runes)
		}
		return m, nil
	}

	return m, nil
}

// submitMessage detects intent locally so it can prepare a placeholder tab
// for creates before the shell responds. This ensures streaming tokens land
// in the correct tab rather than being lost.
func (m Model) submitMessage() (Model, tea.Cmd) {
	message := strings.TrimSpace(m.input)
	in := intent.Detect(message)

	m.input = ""
	m.messages = append(m.messages, shell.Message{Role: "user", Text: message})
	m.streaming = true
	m.streamBuf.Reset()
	m.status = "thinking…"

	// For creates: add a placeholder tab now so tokens have a home
	if in.Kind == intent.KindCreate {
		title := in.TitleHint
		if title == "" {
			title = "New Workspace"
		}
		m.tabs = append(m.tabs, tabState{
			title:  title,
			wsType: string(in.WSType),
		})
		m.activeTab = len(m.tabs) - 1
	}

	ch := make(chan streamEvent, 512)
	m.streamCh = ch

	sessionID := m.sessionID
	sh := m.sh

	go func() {
		resp, err := sh.StreamMessage(
			context.Background(), sessionID, message,
			func(token string) { ch <- streamEvent{Token: token} },
		)
		ch <- streamEvent{Resp: resp, Err: err}
		close(ch)
	}()

	return m, listenStream(ch)
}

func listenStream(ch chan streamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return responseMsg{err: fmt.Errorf("stream closed unexpectedly")}
		}
		if ev.Resp != nil || ev.Err != nil {
			return responseMsg{resp: ev.Resp, err: ev.Err}
		}
		return tokenMsg(ev.Token)
	}
}

// ── View ──────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.width == 0 {
		return "Loading…"
	}

	chatW := m.width * 40 / 100
	wsW := m.width - chatW - 2
	if chatW < 28 {
		chatW = 28
	}
	if wsW < 28 {
		wsW = 28
	}
	innerH := m.height - 4

	chatPane := m.renderChat(chatW, innerH)
	wsPane := m.renderWorkspace(wsW, innerH)
	row := lipgloss.JoinHorizontal(lipgloss.Top, chatPane, " ", wsPane)
	return lipgloss.JoinVertical(lipgloss.Left, row, m.renderStatus())
}

// ── Chat pane ─────────────────────────────────────────────────────

func (m Model) renderChat(w, h int) string {
	st := stylePanel
	if m.focus == FocusChat {
		st = styleActivePanel
	}
	st = st.Width(w - 2)

	var lines []string
	for _, msg := range m.messages {
		wrapped := wordWrap(msg.Text, w-8)
		if msg.Role == "user" {
			for i, l := range wrapped {
				if i == 0 {
					lines = append(lines, styleUser.Render("you › ")+l)
				} else {
					lines = append(lines, "      "+l)
				}
			}
		} else {
			for i, l := range wrapped {
				if i == 0 {
					lines = append(lines, styleShell.Render("cas › ")+l)
				} else {
					lines = append(lines, "      "+l)
				}
			}
		}
		lines = append(lines, "")
	}

	histH := h - 5
	if histH < 0 {
		histH = 0
	}

	total := len(lines)
	end := total - m.chatScroll
	if end < 0 {
		end = 0
	}
	start := end - histH
	if start < 0 {
		start = 0
	}
	visible := lines[start:end]
	for len(visible) < histH {
		visible = append([]string{""}, visible...)
	}

	cursor := "█"
	if m.streaming {
		cursor = styleDim.Render("…")
	}
	sep := styleDim.Render(strings.Repeat("─", w-4))
	inputLine := styleInput.Render("> " + m.input + cursor)
	content := strings.Join(visible, "\n") + "\n" + sep + "\n" + inputLine
	return st.Render(content)
}

// ── Workspace pane ────────────────────────────────────────────────

func (m Model) renderWorkspace(w, h int) string {
	st := styleWSPanel.Width(w - 2)

	if len(m.tabs) == 0 {
		hint := styleDim.Render(
			"No workspace open.\n\n" +
				"  write a project proposal\n" +
				"  create a python script\n" +
				"  make a todo list",
		)
		return st.Render(hint)
	}

	// Tab bar (1 line)
	tabBar := m.renderTabBar(w - 4)

	// Separator
	sep := styleDim.Render(strings.Repeat("─", w-4))

	// Content area: total height minus tab bar, sep, padding
	contentH := h - 4
	if contentH < 1 {
		contentH = 1
	}

	tab := m.tabs[m.activeTab]
	body := m.renderTabContent(tab, w-4, contentH)

	return st.Render(tabBar + "\n" + sep + "\n" + body)
}

func (m Model) renderTabBar(w int) string {
	var parts []string
	for i, tab := range m.tabs {
		// Type badge: first letter of type, or "?" for placeholders
		badge := "?"
		if len(tab.wsType) > 0 {
			badge = string(tab.wsType[0]) // d, c, l
		}

		title := truncate(tab.title, 18)
		if tab.ws == nil {
			title += "…" // still generating
		}
		label := fmt.Sprintf("[%s] %s", badge, title)

		if i == m.activeTab {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTabInactive.Render(label))
		}
	}

	bar := strings.Join(parts, " ")
	// Truncate if it overflows the pane width
	runes := []rune(bar)
	if len(runes) > w {
		bar = string(runes[:w])
	}
	return bar
}

func (m Model) renderTabContent(tab tabState, w, h int) string {
	if tab.content == "" {
		if m.streaming {
			return styleDim.Render("generating…")
		}
		return styleDim.Render("(empty)")
	}

	var rendered string
	if tab.wsType == "code" {
		rendered = styleCode.Render(tab.content)
	} else {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(w),
		)
		if err == nil {
			if out, err := renderer.Render(tab.content); err == nil {
				rendered = strings.TrimRight(out, "\n")
			} else {
				rendered = tab.content
			}
		} else {
			rendered = tab.content
		}
	}

	lines := strings.Split(rendered, "\n")

	maxScroll := len(lines) - h
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := tab.scroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}

	end := scroll + h
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[scroll:end]

	if len(lines) > h && maxScroll > 0 {
		pct := scroll * 100 / maxScroll
		indicator := styleDim.Render(fmt.Sprintf(" ↕ %d%%", pct))
		if len(visible) > 0 {
			visible[len(visible)-1] = indicator
		}
	}

	return strings.Join(visible, "\n")
}

// ── Status bar ────────────────────────────────────────────────────

func (m Model) renderStatus() string {
	if m.status != "" {
		return styleStatus.Render(" " + m.status)
	}

	hints := []string{
		styleDim.Render("tab: switch panel"),
		styleDim.Render("enter: send"),
		styleDim.Render("ctrl+c: quit"),
	}

	if m.focus == FocusWorkspace {
		hints = append([]string{
			styleDim.Render("[/]: prev/next tab"),
			styleDim.Render("↑↓/pgup/pgdn: scroll"),
		}, hints...)
	} else {
		hints = append([]string{styleDim.Render("↑↓: scroll history")}, hints...)
	}

	return "  " + strings.Join(hints, "  │  ")
}

// ── Helpers ───────────────────────────────────────────────────────

func wordWrap(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) <= width {
			line += " " + w
		} else {
			lines = append(lines, line)
			line = w
		}
	}
	return append(lines, line)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
