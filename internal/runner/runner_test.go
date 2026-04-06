package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ── DetectLang ────────────────────────────────────────────────────

func TestDetectLangPython(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"import+print", "import json\nprint(json.dumps({'a': 1}))"},
		{"def+colon", "def hello():\n    return 'world'"},
		{"shebang", "#!/usr/bin/env python3\nprint('hi')"},
		{"dunder main", "if __name__ == '__main__':\n    pass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang := DetectLang(tt.content)
			if lang == nil || lang.Name != "python" {
				t.Errorf("expected python, got %v", lang)
			}
		})
	}
}

func TestDetectLangGo(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"package main", "package main\n\nfunc main() {}"},
		{"import fmt", "package main\n\nimport (\n\t\"fmt\"\n)\n\nfunc main() { fmt.Println(1) }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang := DetectLang(tt.content)
			if lang == nil || lang.Name != "go" {
				t.Errorf("expected go, got %v", lang)
			}
		})
	}
}

func TestDetectLangBash(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"bash shebang", "#!/bin/bash\necho hello"},
		{"sh shebang", "#!/bin/sh\necho hello"},
		{"env bash", "#!/usr/bin/env bash\necho hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang := DetectLang(tt.content)
			if lang == nil || lang.Name != "bash" {
				t.Errorf("expected bash, got %v", lang)
			}
		})
	}
}

func TestDetectLangJavaScript(t *testing.T) {
	lang := DetectLang("const add = (a, b) => a + b;\nconsole.log(add(1, 2));")
	if lang == nil || lang.Name != "javascript" {
		t.Errorf("expected javascript, got %v", lang)
	}
}

func TestDetectLangUnknown(t *testing.T) {
	lang := DetectLang("just some random text with no language markers")
	if lang != nil {
		t.Errorf("expected nil for unknown language, got %v", lang)
	}
}

// ── Run ───────────────────────────────────────────────────────────

func TestRunBashHello(t *testing.T) {
	content := "#!/bin/bash\necho hello world"
	result, err := Run(context.Background(), content, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Language != "bash" {
		t.Errorf("expected language bash, got %q", result.Language)
	}
	if strings.TrimSpace(result.Stdout) != "hello world" {
		t.Errorf("expected 'hello world', got %q", result.Stdout)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0, got %d", result.ExitCode)
	}
}

func TestRunBashExitCode(t *testing.T) {
	content := "#!/bin/bash\nexit 42"
	result, err := Run(context.Background(), content, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit 42, got %d", result.ExitCode)
	}
}

func TestRunBashStderr(t *testing.T) {
	content := "#!/bin/bash\necho oops >&2\nexit 1"
	result, err := Run(context.Background(), content, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Stderr, "oops") {
		t.Errorf("expected 'oops' in stderr, got %q", result.Stderr)
	}
}

func TestRunTimeout(t *testing.T) {
	content := "#!/bin/bash\nsleep 60"
	result, err := Run(context.Background(), content, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExitCode != -1 {
		t.Errorf("expected exit -1 for timeout, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "timed out") {
		t.Errorf("expected timeout message in stderr, got %q", result.Stderr)
	}
}

func TestRunUnknownLanguage(t *testing.T) {
	_, err := Run(context.Background(), "random text no language", 5*time.Second)
	if err == nil {
		t.Fatal("expected error for unknown language")
	}
	if !strings.Contains(err.Error(), "cannot detect language") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunPythonIfAvailable(t *testing.T) {
	// Skip if python3 is not installed
	if _, err := Run(context.Background(), "#!/bin/bash\nwhich python3", 2*time.Second); err != nil {
		t.Skip("python3 not available")
	}

	content := "import sys\nprint(2 + 2)"
	result, err := Run(context.Background(), content, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Language != "python" {
		t.Errorf("expected python, got %q", result.Language)
	}
	if strings.TrimSpace(result.Stdout) != "4" {
		t.Errorf("expected '4', got %q", result.Stdout)
	}
}

func TestRunRestrictedEnv(t *testing.T) {
	// The subprocess should NOT inherit secrets from the parent env
	t.Setenv("SECRET_KEY", "do-not-leak")
	content := "#!/bin/bash\necho ${SECRET_KEY:-empty}"
	result, err := Run(context.Background(), content, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Stdout, "do-not-leak") {
		t.Error("SECRET_KEY leaked to subprocess")
	}
	if strings.TrimSpace(result.Stdout) != "empty" {
		t.Errorf("expected 'empty', got %q", result.Stdout)
	}
}

// ── FormatResult ──────────────────────────────────────────────────

func TestFormatResultSuccess(t *testing.T) {
	r := &Result{
		Stdout:   "hello world\n",
		ExitCode: 0,
		Duration: 42 * time.Millisecond,
		Language: "bash",
	}
	formatted := FormatResult(r)
	if !strings.Contains(formatted, "ran bash") {
		t.Error("missing language in formatted output")
	}
	if !strings.Contains(formatted, "hello world") {
		t.Error("missing stdout in formatted output")
	}
	if !strings.Contains(formatted, "exit 0") {
		t.Error("missing exit code in formatted output")
	}
}

func TestFormatResultError(t *testing.T) {
	r := &Result{
		Stderr:   "file not found\n",
		ExitCode: 1,
		Duration: 10 * time.Millisecond,
		Language: "python",
	}
	formatted := FormatResult(r)
	if !strings.Contains(formatted, "stderr:") {
		t.Error("missing stderr label")
	}
	if !strings.Contains(formatted, "file not found") {
		t.Error("missing stderr content")
	}
}

func TestFormatResultNoOutput(t *testing.T) {
	r := &Result{ExitCode: 0, Duration: 5 * time.Millisecond, Language: "bash"}
	formatted := FormatResult(r)
	if !strings.Contains(formatted, "(no output)") {
		t.Errorf("expected '(no output)', got %q", formatted)
	}
}
