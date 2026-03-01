package cli

import "testing"

func TestParseUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing all", args: []string{}},
		{name: "missing id", args: []string{"--from", "claude", "--to", "codex"}},
		{name: "unsupported direction", args: []string{"--from", "claude", "--to", "claude", "--id", "x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args)
			if err == nil {
				t.Fatalf("expected error")
			}
			if _, ok := err.(UsageError); !ok {
				t.Fatalf("expected UsageError, got %T", err)
			}
		})
	}
}

func TestParseValidAndHelpVersion(t *testing.T) {
	opts, err := Parse([]string{"--help"})
	if err != nil {
		t.Fatalf("parse --help: %v", err)
	}
	if !opts.ShowHelp {
		t.Fatalf("expected ShowHelp")
	}

	opts, err = Parse([]string{"--version"})
	if err != nil {
		t.Fatalf("parse --version: %v", err)
	}
	if !opts.ShowVersion {
		t.Fatalf("expected ShowVersion")
	}

	opts, err = Parse([]string{"--from", "claude", "--to", "codex", "--id", "abc", "--dry-run"})
	if err != nil {
		t.Fatalf("parse valid: %v", err)
	}
	if opts.From != "claude" || opts.To != "codex" || opts.ID != "abc" {
		t.Fatalf("unexpected opts: %+v", opts)
	}
	if !opts.DryRun {
		t.Fatalf("expected dry run")
	}
	if opts.ClaudeHome == "" || opts.CodexHome == "" {
		t.Fatalf("expected default homes")
	}

	opts, err = Parse([]string{"--from", "codex", "--to", "claude", "--id", "thread-1", "--dry-run"})
	if err != nil {
		t.Fatalf("parse reverse valid: %v", err)
	}
	if opts.From != "codex" || opts.To != "claude" || opts.ID != "thread-1" {
		t.Fatalf("unexpected reverse opts: %+v", opts)
	}
}
