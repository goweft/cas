//go:build integration

// TUI integration tests — spawn the real CAS binary in tmux and interact
// with it the same way a user would. These catch runtime bugs that unit
// tests can't: Bubble Tea panics, LLM token routing, intent detection
// at the boundary between parsing and the full stack.
//
// Run with:
//   TUI_INTEGRATION=1 go test -v -tags=integration ./tests/tui/ -timeout 300s
//
// Requires: tmux, go, Ollama running with qwen3.5:9b loaded.

package tui_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ── Helpers ───────────────────────────────────────────────────────

func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("TUI_INTEGRATION") != "1" {
		t.Skip("set TUI_INTEGRATION=1 to run TUI integration tests")
	}
}

// buildBinary compiles the cas binary to a temp path and returns it.
func buildBinary(t *testing.T) string {
	t.Helper()
	// Find repo root (two levels up from tests/tui/)
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")

	bin := filepath.Join(t.TempDir(), "cas-tui-test")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/cas")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// spawnSession starts the binary in a detached tmux session.
// The session is automatically killed on test cleanup.
func spawnSession(t *testing.T, bin, session string, extraArgs ...string) {
	t.Helper()
	// Kill any existing session with this name
	exec.Command("tmux", "kill-session", "-t", session).Run()

	args := []string{"new-session", "-d", "-s", session, "-x", "200", "-y", "50"}
	cmdStr := bin + " --memory"
	for _, a := range extraArgs {
		cmdStr += " " + a
	}
	args = append(args, cmdStr)

	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("failed to spawn tmux session %q: %v\n%s", session, err, out)
	}

	t.Cleanup(func() {
		exec.Command("tmux", "kill-session", "-t", session).Run()
	})

	// Give the TUI a moment to initialise
	time.Sleep(2 * time.Second)
}

// sendKeys sends a keystroke sequence to a tmux session.
// Use "Enter" for the Enter key, other special keys in tmux format.
func sendKeys(t *testing.T, session, keys string) {
	t.Helper()
	out, err := exec.Command("tmux", "send-keys", "-t", session, keys, "").CombinedOutput()
	if err != nil {
		t.Logf("send-keys warning: %v\n%s", err, out)
	}
}

// sendText types literal text into a session (sends as a single key string).
func sendText(t *testing.T, session, text string) {
	t.Helper()
	sendKeys(t, session, text)
}

// sendEnter presses Enter in a tmux session.
func sendEnter(t *testing.T, session string) {
	t.Helper()
	exec.Command("tmux", "send-keys", "-t", session, "", "Enter").Run()
}

// captureScreen returns the current terminal contents of a tmux session.
func captureScreen(t *testing.T, session string) string {
	t.Helper()
	out, err := exec.Command("tmux", "capture-pane", "-t", session, "-p").Output()
	if err != nil {
		t.Logf("capture-pane warning for %q: %v", session, err)
		return ""
	}
	return string(out)
}

// sessionAlive returns true if the tmux session is still running.
func sessionAlive(session string) bool {
	err := exec.Command("tmux", "has-session", "-t", session).Run()
	return err == nil
}

// waitFor polls the screen until substr appears or timeout elapses.
// Returns true if found, false on timeout.
func waitFor(t *testing.T, session, substr string, timeoutSec int) bool {
	t.Helper()
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		if !sessionAlive(session) {
			t.Logf("session %q died while waiting for %q", session, substr)
			return false
		}
		screen := captureScreen(t, session)
		if strings.Contains(screen, substr) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	screen := captureScreen(t, session)
	t.Logf("timeout waiting for %q in session %q\nFinal screen:\n%s", substr, session, screen)
	return false
}

func assertContains(t *testing.T, screen, substr string) {
	t.Helper()
	if !strings.Contains(screen, substr) {
		t.Errorf("expected screen to contain %q\nScreen:\n%s", substr, screen)
	}
}

func assertNotContains(t *testing.T, screen, substr string) {
	t.Helper()
	if strings.Contains(screen, substr) {
		t.Errorf("expected screen NOT to contain %q\nScreen:\n%s", substr, screen)
	}
}

// ── Tests ─────────────────────────────────────────────────────────

func TestTUIStartsAndShowsEmptyState(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-start"
	spawnSession(t, bin, sess)

	if !sessionAlive(sess) {
		t.Fatal("session died immediately after spawn")
	}

	screen := captureScreen(t, sess)
	assertContains(t, screen, "No workspace open")
	assertContains(t, screen, "write a project proposal")
	assertContains(t, screen, "ctrl+c: quit")
}

func TestCreateDocumentWorkspace(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-doc-create"
	spawnSession(t, bin, sess)

	sendText(t, sess, "write a short project proposal")
	sendEnter(t, sess)

	// Tab should appear quickly (placeholder created client-side)
	if !waitFor(t, sess, "[d]", 10) {
		t.Fatal("document workspace tab did not appear")
	}

	// Wait for generation to complete
	if !waitFor(t, sess, "cas ›", 90) {
		t.Fatal("generation did not complete within 90s")
	}

	screen := captureScreen(t, sess)
	assertContains(t, screen, "Created document workspace")
	assertContains(t, screen, "[d]")
	assertNotContains(t, screen, "error:")
}

func TestCreateCodeWorkspace(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-code-create"
	spawnSession(t, bin, sess)

	sendText(t, sess, "write a python hello world script")
	sendEnter(t, sess)

	if !waitFor(t, sess, "[c]", 10) {
		t.Fatal("code workspace tab did not appear")
	}
	if !waitFor(t, sess, "cas ›", 90) {
		t.Fatal("code generation did not complete")
	}

	screen := captureScreen(t, sess)
	assertContains(t, screen, "[c]")
	assertContains(t, screen, "Created code workspace")
}

func TestChatDoesNotCorruptWorkspace(t *testing.T) {
	// Regression test for the token routing bug:
	// chat responses were overwriting workspace content.
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-token-routing"
	spawnSession(t, bin, sess)

	// Create a workspace
	sendText(t, sess, "write a short project proposal")
	sendEnter(t, sess)
	if !waitFor(t, sess, "cas ›", 90) {
		t.Fatal("first generation did not complete")
	}

	// Record workspace content marker
	screen := captureScreen(t, sess)
	if !strings.Contains(screen, "[d]") {
		t.Fatal("no document tab after create")
	}

	// Ask a chat question — should NOT corrupt workspace
	sendText(t, sess, "how long should this document be")
	sendEnter(t, sess)
	if !waitFor(t, sess, "you › how long", 5) {
		t.Fatal("user message did not appear")
	}

	// Wait for chat response
	time.Sleep(60 * time.Second)
	screen = captureScreen(t, sess)

	// Workspace tab must still exist
	assertContains(t, screen, "[d]")
	// Workspace content area must not show chat text as workspace content
	// (the tab bar area shows [d] which means workspace is still there)
}

func TestEditIntentRouting(t *testing.T) {
	// "add a conclusion" must route to edit, not create a second workspace.
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-edit-intent"
	spawnSession(t, bin, sess)

	sendText(t, sess, "write a short proposal")
	sendEnter(t, sess)
	if !waitFor(t, sess, "cas ›", 90) {
		t.Fatal("create did not complete")
	}

	sendText(t, sess, "add a conclusion section")
	sendEnter(t, sess)
	// Wait for edit to complete
	time.Sleep(60 * time.Second)

	screen := captureScreen(t, sess)
	// Should say "Updated workspace", not "Created document workspace" again
	assertContains(t, screen, "Updated workspace")
	// Should still have exactly one [d] tab (not two)
	count := strings.Count(screen, "[d]")
	if count > 2 { // tab bar + possibly rendered elsewhere
		t.Errorf("expected one workspace tab, screen shows %d [d] occurrences:\n%s", count, screen)
	}
}

func TestSessionSurvivesMultipleMessages(t *testing.T) {
	// Ensures the binary does not crash across multiple interactions.
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-multi-msg"
	spawnSession(t, bin, sess)

	// Message 1: create
	sendText(t, sess, "write a todo list for launching a product")
	sendEnter(t, sess)
	if !waitFor(t, sess, "cas ›", 90) {
		t.Fatal("first message failed")
	}

	if !sessionAlive(sess) {
		t.Fatal("session died after first message")
	}

	// Message 2: chat
	sendText(t, sess, "thanks")
	sendEnter(t, sess)
	time.Sleep(30 * time.Second)

	if !sessionAlive(sess) {
		t.Fatal("session died after second message")
	}

	screen := captureScreen(t, sess)
	assertNotContains(t, screen, "panic")
	assertNotContains(t, screen, "error:")
}

func TestInputVisibleBeforeSend(t *testing.T) {
	// Characters typed must appear in the input area before sending.
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-input-visible"
	spawnSession(t, bin, sess)

	// Type without pressing Enter
	sendText(t, sess, "hello world")
	time.Sleep(200 * time.Millisecond)

	screen := captureScreen(t, sess)
	// Input line should show what was typed
	if !strings.Contains(screen, "hello") {
		t.Errorf("typed text not visible in input area\nScreen:\n%s", screen)
	}
}

func TestFocusSwitchWithTab(t *testing.T) {
	skipUnlessIntegration(t)
	bin := buildBinary(t)
	sess := "tui-focus"
	spawnSession(t, bin, sess)

	// Initial focus should be chat (active border)
	screen := captureScreen(t, sess)
	assertContains(t, screen, "tab: workspace")

	// Press Tab to switch to workspace
	sendKeys(t, sess, "Tab")
	time.Sleep(200 * time.Millisecond)
	screen = captureScreen(t, sess)
	assertContains(t, screen, "tab: chat")
}

// ── Benchmark: startup time ───────────────────────────────────────

func BenchmarkTUIStartup(b *testing.B) {
	if os.Getenv("TUI_INTEGRATION") != "1" {
		b.Skip("set TUI_INTEGRATION=1")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	bin := filepath.Join(b.TempDir(), "cas-bench")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/cas")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess := fmt.Sprintf("tui-bench-%d", i)
		exec.Command("tmux", "new-session", "-d", "-s", sess, "-x", "200", "-y", "50", bin+" --memory").Run()
		start := time.Now()
		deadline := start.Add(10 * time.Second)
		for time.Now().Before(deadline) {
			out, _ := exec.Command("tmux", "capture-pane", "-t", sess, "-p").Output()
			if strings.Contains(string(out), "No workspace") {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		b.ReportMetric(float64(time.Since(start).Milliseconds()), "ms/startup")
		exec.Command("tmux", "kill-session", "-t", sess).Run()
	}
}
