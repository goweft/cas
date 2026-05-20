package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/goweft/cas/internal/llm"
	"github.com/goweft/cas/internal/shell"
	"github.com/goweft/cas/internal/store"
	"github.com/goweft/cas/internal/workspace"
	"github.com/goweft/cas/ui"
)

func main() {
	memFlag := flag.Bool("memory", false, "use in-memory store (no persistence)")
	dbFlag := flag.String("db", store.DefaultPath(), "path to SQLite database")
	providersFlag := flag.Bool("providers", false, "list configured providers and exit")
	flag.Parse()

	if *providersFlag {
		printProviders()
		return
	}

	if err := llm.ValidateProvider(); err != nil {
		fmt.Fprintf(os.Stderr, "cas: %v\n", err)
		os.Exit(1)
	}

	var s store.Store
	if *memFlag {
		s = store.NewMemoryStore()
	} else {
		var err error
		s, err = store.NewSQLiteStore(*dbFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cas: open store: %v\n", err)
			os.Exit(1)
		}
	}
	defer s.Close()

	sh := shell.NewShell(s)
	if err := sh.Restore(); err != nil {
		fmt.Fprintf(os.Stderr, "cas: restore: %v\n", err)
		os.Exit(1)
	}

	sess := sh.LatestSession()
	if sess == nil {
		var err error
		sess, err = sh.CreateSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cas: create session: %v\n", err)
			os.Exit(1)
		}
	}

	var initWS []*workspace.Workspace
	if active := sh.Workspaces().Active(); len(active) > 0 {
		initWS = active
	}

	m := ui.New(sh, sess.ID, sess.History, initWS)

	// No mouse mode — WithMouseAllMotion floods the event queue with motion
	// events on every cursor move, blocking key events and making the UI
	// unresponsive. Use keyboard scrolling: ↑↓ and PgUp/PgDn.
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "cas: %v\n", err)
		os.Exit(1)
	}
}

func printProviders() {
	statuses := llm.AllProviders()
	fmt.Println("CAS providers:")
	fmt.Println()
	for _, ps := range statuses {
		active := "  "
		if ps.Active {
			active = "▶ "
		}
		keyInfo := "(no key required)"
		if ps.KeyEnv != "" {
			if ps.KeySet {
				keyInfo = ps.KeyEnv + "=✓"
			} else {
				keyInfo = ps.KeyEnv + "=✗ (not set)"
			}
		}
		fmt.Printf("  %s%-12s  %s\n", active, string(ps.Provider), keyInfo)
	}
	fmt.Println()
	fmt.Println("Set CAS_PROVIDER=<name> to switch. Default: ollama")
}
