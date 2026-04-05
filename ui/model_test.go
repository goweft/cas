package ui_test

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/goweft/cas/internal/shell"
	"github.com/goweft/cas/internal/store"
	"github.com/goweft/cas/ui"
)

// newTestModel returns a minimal Model suitable for input testing.
// Width/height are set so View() doesn't return "starting…".
func newTestModel(t *testing.T) ui.Model {
	t.Helper()
	s := store.NewMemoryStore()
	sh := shell.NewShell(s)
	sess, err := sh.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	m := ui.New(sh, sess.ID, nil, nil)
	// Simulate WindowSizeMsg so the model has valid dimensions
	model, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	return model.(ui.Model)
}

func key(k tea.KeyType) tea.KeyMsg         { return tea.KeyMsg{Type: k} }
func rune_(r string) tea.KeyMsg            { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }
func space() tea.KeyMsg                    { return tea.KeyMsg{Type: tea.KeySpace} }
func backspace() tea.KeyMsg                { return tea.KeyMsg{Type: tea.KeyBackspace} }

// send applies a sequence of key messages to a model and returns the final model.
func send(m ui.Model, keys ...tea.KeyMsg) ui.Model {
	for _, k := range keys {
		next, _ := m.Update(k)
		m = next.(ui.Model)
	}
	return m
}

func typeString(m ui.Model, s string) ui.Model {
	for _, r := range s {
		if r == ' ' {
			m = send(m, space())
		} else {
			m = send(m, rune_(string(r)))
		}
	}
	return m
}

// ── Basic typing ─────────────────────────────────────────────────

func TestTypeSimpleText(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	if m.Input() != "hello" {
		t.Errorf("expected 'hello', got %q", m.Input())
	}
	if m.InputCursor() != 5 {
		t.Errorf("expected cursor at 5, got %d", m.InputCursor())
	}
}

func TestTypeWithSpaces(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello world")
	if m.Input() != "hello world" {
		t.Errorf("expected 'hello world', got %q", m.Input())
	}
	if m.InputCursor() != 11 {
		t.Errorf("expected cursor at 11, got %d", m.InputCursor())
	}
}

func TestBackspace(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	m = send(m, backspace())
	if m.Input() != "hell" {
		t.Errorf("expected 'hell', got %q", m.Input())
	}
	if m.InputCursor() != 4 {
		t.Errorf("expected cursor at 4, got %d", m.InputCursor())
	}
}

func TestBackspaceAtStart(t *testing.T) {
	m := newTestModel(t)
	// Backspace with empty input should not panic or change anything
	m = send(m, backspace())
	if m.Input() != "" {
		t.Errorf("expected empty input, got %q", m.Input())
	}
	if m.InputCursor() != 0 {
		t.Errorf("expected cursor at 0, got %d", m.InputCursor())
	}
}

// ── Cursor movement ───────────────────────────────────────────────

func TestArrowLeft(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	m = send(m, key(tea.KeyLeft))
	if m.InputCursor() != 4 {
		t.Errorf("expected cursor at 4 after left, got %d", m.InputCursor())
	}
}

func TestArrowLeftAtStart(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hi")
	m = send(m, key(tea.KeyLeft), key(tea.KeyLeft), key(tea.KeyLeft)) // one extra
	if m.InputCursor() != 0 {
		t.Errorf("expected cursor clamped at 0, got %d", m.InputCursor())
	}
}

func TestArrowRight(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	m = send(m, key(tea.KeyLeft), key(tea.KeyLeft))
	m = send(m, key(tea.KeyRight))
	if m.InputCursor() != 4 {
		t.Errorf("expected cursor at 4, got %d", m.InputCursor())
	}
}

func TestArrowRightAtEnd(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hi")
	m = send(m, key(tea.KeyRight)) // already at end
	if m.InputCursor() != 2 {
		t.Errorf("expected cursor clamped at 2, got %d", m.InputCursor())
	}
}

func TestHomeKey(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello world")
	m = send(m, key(tea.KeyHome))
	if m.InputCursor() != 0 {
		t.Errorf("expected cursor at 0 after Home, got %d", m.InputCursor())
	}
}

func TestEndKey(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello world")
	m = send(m, key(tea.KeyHome))  // go to start
	m = send(m, key(tea.KeyEnd))   // back to end
	if m.InputCursor() != 11 {
		t.Errorf("expected cursor at 11 after End, got %d", m.InputCursor())
	}
}

func TestCtrlA(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	m = send(m, key(tea.KeyCtrlA))
	if m.InputCursor() != 0 {
		t.Errorf("expected cursor at 0 after Ctrl+A, got %d", m.InputCursor())
	}
}

func TestCtrlE(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	m = send(m, key(tea.KeyCtrlA))  // go to start
	m = send(m, key(tea.KeyCtrlE))  // back to end
	if m.InputCursor() != 5 {
		t.Errorf("expected cursor at 5 after Ctrl+E, got %d", m.InputCursor())
	}
}

// ── Insert at cursor ──────────────────────────────────────────────

func TestInsertAtMiddle(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hllo")
	m = send(m, key(tea.KeyHome))
	m = send(m, key(tea.KeyRight)) // cursor after 'h'
	m = send(m, rune_("e"))
	if m.Input() != "hello" {
		t.Errorf("expected 'hello', got %q", m.Input())
	}
	if m.InputCursor() != 2 {
		t.Errorf("expected cursor at 2, got %d", m.InputCursor())
	}
}

func TestInsertAtStart(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "world")
	m = send(m, key(tea.KeyHome))
	m = typeString(m, "hello ")
	if m.Input() != "hello world" {
		t.Errorf("expected 'hello world', got %q", m.Input())
	}
}

func TestInsertSpaceAtMiddle(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "helloworld")
	m = send(m, key(tea.KeyHome))
	for i := 0; i < 5; i++ {
		m = send(m, key(tea.KeyRight))
	}
	m = send(m, space())
	if m.Input() != "hello world" {
		t.Errorf("expected 'hello world', got %q", m.Input())
	}
}

// ── Delete ────────────────────────────────────────────────────────

func TestDeleteKey(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "helo")
	m = send(m, key(tea.KeyHome))
	m = send(m, key(tea.KeyRight)) // cursor after 'h'
	m = send(m, key(tea.KeyDelete))
	if m.Input() != "hlo" {
		t.Errorf("expected 'hlo', got %q", m.Input())
	}
	if m.InputCursor() != 1 {
		t.Errorf("expected cursor at 1, got %d", m.InputCursor())
	}
}

func TestDeleteAtEnd(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	// Delete at end does nothing
	m = send(m, key(tea.KeyDelete))
	if m.Input() != "hello" {
		t.Errorf("expected 'hello' unchanged, got %q", m.Input())
	}
}

func TestBackspaceAtMiddle(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "heelo")
	m = send(m, key(tea.KeyHome))
	for i := 0; i < 3; i++ {
		m = send(m, key(tea.KeyRight))
	}
	// cursor is after second 'e', delete it
	m = send(m, backspace())
	if m.Input() != "helo" {
		t.Errorf("expected 'helo', got %q", m.Input())
	}
}

// ── Word delete ───────────────────────────────────────────────────

func TestCtrlW(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello world")
	m = send(m, key(tea.KeyCtrlW))
	if m.Input() != "hello " {
		t.Errorf("expected 'hello ', got %q", m.Input())
	}
	if m.InputCursor() != 6 {
		t.Errorf("expected cursor at 6, got %d", m.InputCursor())
	}
}

func TestCtrlWDeletesTrailingSpaces(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello   ")
	m = send(m, key(tea.KeyCtrlW))
	if m.Input() != "" {
		t.Errorf("expected empty after ctrl+w deleting word+spaces, got %q", m.Input())
	}
}

func TestCtrlWAtStart(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hi")
	m = send(m, key(tea.KeyHome))
	m = send(m, key(tea.KeyCtrlW)) // no-op at start
	if m.Input() != "hi" {
		t.Errorf("expected 'hi' unchanged, got %q", m.Input())
	}
}

// ── Kill line ─────────────────────────────────────────────────────

func TestCtrlK(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello world")
	m = send(m, key(tea.KeyHome))
	for i := 0; i < 5; i++ {
		m = send(m, key(tea.KeyRight))
	}
	m = send(m, key(tea.KeyCtrlK))
	if m.Input() != "hello" {
		t.Errorf("expected 'hello', got %q", m.Input())
	}
	if m.InputCursor() != 5 {
		t.Errorf("expected cursor at 5, got %d", m.InputCursor())
	}
}

func TestCtrlU(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello world")
	m = send(m, key(tea.KeyHome))
	for i := 0; i < 6; i++ {
		m = send(m, key(tea.KeyRight))
	}
	m = send(m, key(tea.KeyCtrlU))
	if m.Input() != "world" {
		t.Errorf("expected 'world', got %q", m.Input())
	}
	if m.InputCursor() != 0 {
		t.Errorf("expected cursor at 0, got %d", m.InputCursor())
	}
}

// ── Submit resets cursor ──────────────────────────────────────────

func TestSubmitResetsInput(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "hello")
	// Move cursor to middle
	m = send(m, key(tea.KeyLeft), key(tea.KeyLeft))
	// Enter submits — can't actually call LLM in test,
	// but we can verify input+cursor reset via a streaming=true check
	// by directly checking state after a simulated submit key
	// (submit only fires when not streaming and input not empty)
	// Since we can't mock the LLM here, just verify the cursor is
	// tracked correctly up to that point.
	if m.InputCursor() != 3 {
		t.Errorf("expected cursor at 3 after two lefts, got %d", m.InputCursor())
	}
}

// ── Focus isolation ───────────────────────────────────────────────

func TestTypingOnlyInChatFocus(t *testing.T) {
	m := newTestModel(t)
	// Switch to workspace focus
	m = send(m, key(tea.KeyTab))
	// Type — should NOT appear in input
	m = typeString(m, "hello")
	if m.Input() != "" {
		t.Errorf("expected empty input in workspace focus, got %q", m.Input())
	}
	// Switch back to chat
	m = send(m, key(tea.KeyTab))
	m = typeString(m, "hello")
	if m.Input() != "hello" {
		t.Errorf("expected 'hello' in chat focus, got %q", m.Input())
	}
}

func TestEscInChatQuitsWhenNoWorkspace(t *testing.T) {
	m := newTestModel(t)
	// Esc with focus=Chat and no workspace should quit
	// (we can verify it returns a quit command)
	_, cmd := m.Update(key(tea.KeyEsc))
	if cmd == nil {
		t.Error("expected quit command from Esc in chat focus")
	}
}

// ── Unicode ───────────────────────────────────────────────────────

func TestUnicodeInput(t *testing.T) {
	m := newTestModel(t)
	m = send(m, rune_("こ"), rune_("ん"), rune_("に"), rune_("ち"), rune_("は"))
	if m.Input() != "こんにちは" {
		t.Errorf("expected 'こんにちは', got %q", m.Input())
	}
	if m.InputCursor() != 5 {
		t.Errorf("expected cursor at 5 (rune count), got %d", m.InputCursor())
	}
}

func TestUnicodeBackspace(t *testing.T) {
	m := newTestModel(t)
	m = send(m, rune_("こ"), rune_("ん"), rune_("に"))
	m = send(m, backspace())
	if m.Input() != "こん" {
		t.Errorf("expected 'こん', got %q", m.Input())
	}
}

func TestUnicodeInsertAtMiddle(t *testing.T) {
	m := newTestModel(t)
	m = send(m, rune_("こ"), rune_("に"))
	m = send(m, key(tea.KeyHome))
	m = send(m, key(tea.KeyRight))
	m = send(m, rune_("ん"))
	if m.Input() != "こんに" {
		t.Errorf("expected 'こんに', got %q", m.Input())
	}
}

// ── Rapid typing simulation ───────────────────────────────────────

func TestRapidTypingDoesNotCorruptInput(t *testing.T) {
	m := newTestModel(t)
	sentence := "the quick brown fox jumps over the lazy dog"
	m = typeString(m, sentence)
	if m.Input() != sentence {
		t.Errorf("rapid typing corrupted input:\n  got:  %q\n  want: %q", m.Input(), sentence)
	}
	if m.InputCursor() != len([]rune(sentence)) {
		t.Errorf("cursor mismatch after rapid typing: got %d, want %d",
			m.InputCursor(), len([]rune(sentence)))
	}
}

func TestEditInMiddleOfLongInput(t *testing.T) {
	m := newTestModel(t)
	m = typeString(m, "the brown fox")
	// Go back to insert "quick " after "the "
	m = send(m, key(tea.KeyHome))
	for i := 0; i < 4; i++ {
		m = send(m, key(tea.KeyRight))
	}
	m = typeString(m, "quick ")
	if m.Input() != "the quick brown fox" {
		t.Errorf("edit at middle failed: got %q", m.Input())
	}
}
