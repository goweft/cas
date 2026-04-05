package conductor_test

import (
	"os"
	"testing"

	"github.com/goweft/cas/internal/conductor"
)

func newConductor(t *testing.T) *conductor.Conductor {
	t.Helper()
	f, err := os.CreateTemp("", "cas-conductor-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	return conductor.New(f.Name())
}

func TestObserveCreate(t *testing.T) {
	c := newConductor(t)
	c.Observe("create_workspace", "write a project proposal", "Project Proposal", "document")

	summary := c.ProfileSummary()
	if summary["workspace_count"].(int) != 1 {
		t.Errorf("expected workspace_count=1, got %v", summary["workspace_count"])
	}
	if summary["message_count"].(int) != 1 {
		t.Errorf("expected message_count=1, got %v", summary["message_count"])
	}
}

func TestObserveEdit(t *testing.T) {
	c := newConductor(t)
	c.Observe("create_workspace", "write a proposal", "Proposal", "document")
	c.Observe("edit_workspace", "add a section about budget", "", "")

	summary := c.ProfileSummary()
	if summary["edit_count"].(int) != 1 {
		t.Errorf("expected edit_count=1, got %v", summary["edit_count"])
	}
}

func TestObserveSessionStart(t *testing.T) {
	c := newConductor(t)
	c.ObserveSessionStart()
	c.ObserveSessionStart()

	summary := c.ProfileSummary()
	if summary["session_count"].(int) != 2 {
		t.Errorf("expected session_count=2, got %v", summary["session_count"])
	}
}

func TestUserContextEmptyWithNoSignal(t *testing.T) {
	c := newConductor(t)
	ctx := c.UserContext()
	if ctx != "" {
		t.Errorf("expected empty context with no observations, got %q", ctx)
	}
}

func TestUserContextAfterWorkspace(t *testing.T) {
	c := newConductor(t)
	c.Observe("create_workspace", "write a project proposal", "Project Proposal", "document")
	c.Observe("create_workspace", "draft a resume", "Resume", "document")

	ctx := c.UserContext()
	if ctx == "" {
		t.Error("expected non-empty context after workspace creates")
	}
}

func TestUserContextMentionsWSType(t *testing.T) {
	c := newConductor(t)
	c.Observe("create_workspace", "create a python script", "Script", "code")
	c.Observe("create_workspace", "write a bash script", "Bash Script", "code")

	ctx := c.UserContext()
	if ctx == "" {
		t.Error("expected context after code workspace creates")
	}
	// Should mention code workspaces
	if !containsAny(ctx, []string{"code", "script"}) {
		t.Errorf("expected context to mention code/script, got: %q", ctx)
	}
}

func TestUserContextEditStyle(t *testing.T) {
	c := newConductor(t)
	c.Observe("create_workspace", "write a proposal", "Proposal", "document")
	c.Observe("edit_workspace", "rewrite the introduction", "", "")
	c.Observe("edit_workspace", "revise the executive summary", "", "")

	ctx := c.UserContext()
	if ctx == "" {
		t.Error("expected context after observations")
	}
}

func TestUserContextReturningUser(t *testing.T) {
	c := newConductor(t)
	c.ObserveSessionStart()
	c.ObserveSessionStart()
	c.Observe("create_workspace", "write a proposal", "Proposal", "document")

	ctx := c.UserContext()
	if ctx == "" {
		t.Error("expected context for returning user")
	}
	if !containsAny(ctx, []string{"Returning", "session"}) {
		t.Errorf("expected returning user mention, got: %q", ctx)
	}
}

func TestReset(t *testing.T) {
	c := newConductor(t)
	c.ObserveSessionStart()
	c.Observe("create_workspace", "write a proposal", "Proposal", "document")

	c.Reset()

	summary := c.ProfileSummary()
	if summary["workspace_count"].(int) != 0 {
		t.Errorf("expected workspace_count=0 after reset, got %v", summary["workspace_count"])
	}
	if c.UserContext() != "" {
		t.Error("expected empty context after reset")
	}
}

func TestPersistence(t *testing.T) {
	f, err := os.CreateTemp("", "cas-conductor-persist-*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	// First conductor writes
	c1 := conductor.New(f.Name())
	c1.ObserveSessionStart()
	c1.Observe("create_workspace", "write a proposal", "Proposal", "document")

	// Second conductor reads same file
	c2 := conductor.New(f.Name())
	summary := c2.ProfileSummary()
	if summary["workspace_count"].(int) != 1 {
		t.Errorf("expected workspace_count=1 after reload, got %v", summary["workspace_count"])
	}
	if summary["session_count"].(int) != 1 {
		t.Errorf("expected session_count=1 after reload, got %v", summary["session_count"])
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
