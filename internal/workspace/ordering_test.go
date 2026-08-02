package workspace_test

// Ordering must be deterministic and independent of clock resolution:
// on platforms with coarse wall clocks (Windows), back-to-back creates
// can share a CreatedAt timestamp, so creation order is tracked by a
// manager-local sequence instead. These tests pin that behavior for the
// restore path; TestActiveOrdering pins it for live creates.

import (
	"fmt"
	"testing"

	"github.com/goweft/cas/internal/store"
	"github.com/goweft/cas/internal/workspace"
)

func TestRestoreOrdering(t *testing.T) {
	s := store.NewMemoryStore()

	m1 := workspace.NewManager(s)
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("ws%d", i)
		if _, err := m1.Create(id, "document", "T"+id, "content", "ses1"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	m2 := workspace.NewManager(s)
	if err := m2.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}

	active := m2.Active()
	if len(active) != 5 {
		t.Fatalf("expected 5 restored workspaces, got %d", len(active))
	}
	for i, ws := range active {
		want := fmt.Sprintf("ws%d", i+1)
		if ws.ID != want {
			t.Errorf("position %d: got %s, want %s", i, ws.ID, want)
		}
	}
}

func TestRestoreThenCreateOrdering(t *testing.T) {
	s := store.NewMemoryStore()

	m1 := workspace.NewManager(s)
	m1.Create("old1", "document", "Old1", "c", "ses1")
	m1.Create("old2", "document", "Old2", "c", "ses1")

	m2 := workspace.NewManager(s)
	if err := m2.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	m2.Create("new1", "document", "New1", "c", "ses2")

	active := m2.Active()
	if len(active) != 3 {
		t.Fatalf("expected 3, got %d", len(active))
	}
	got := []string{active[0].ID, active[1].ID, active[2].ID}
	want := []string{"old1", "old2", "new1"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
