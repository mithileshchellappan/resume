package cli

import "testing"

func TestParseUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unsupported direction", args: []string{"--from", "claude", "--to", "claude", "--id", "x"}},
		{name: "invalid from", args: []string{"--from", "foo"}},
		{name: "invalid to", args: []string{"--to", "bar"}},
		{name: "unexpected positional", args: []string{"oops"}},
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

	opts, err = Parse([]string{"--from", "claude", "--to", "codex", "--interactive", "--source-folder", " /repo "})
	if err != nil {
		t.Fatalf("parse interactive valid: %v", err)
	}
	if !opts.Interactive {
		t.Fatalf("expected interactive")
	}
	if opts.ID != "" {
		t.Fatalf("expected empty id in interactive mode")
	}
	if opts.SourceFolder != "/repo" {
		t.Fatalf("unexpected source folder: %q", opts.SourceFolder)
	}

	opts, err = Parse([]string{"--from", "claude", "--to", "codex"})
	if err != nil {
		t.Fatalf("parse missing id should default to interactive: %v", err)
	}
	if !opts.Interactive {
		t.Fatalf("expected interactive default when id is missing")
	}
	if opts.ID != "" {
		t.Fatalf("expected empty id when id is omitted")
	}

	opts, err = Parse([]string{})
	if err != nil {
		t.Fatalf("parse empty args should be valid for interactive tool selection: %v", err)
	}
	if opts.From != "" || opts.To != "" {
		t.Fatalf("expected empty from/to, got %+v", opts)
	}
	if !opts.Interactive {
		t.Fatalf("expected interactive default when id is omitted")
	}

	opts, err = Parse([]string{"--from", "codex"})
	if err != nil {
		t.Fatalf("parse single from: %v", err)
	}
	if opts.From != "codex" || opts.To != "" {
		t.Fatalf("unexpected opts for single from: %+v", opts)
	}

	opts, err = Parse([]string{"--to", "claude", "--id", "thread-123"})
	if err != nil {
		t.Fatalf("parse single to with id: %v", err)
	}
	if opts.From != "" || opts.To != "claude" {
		t.Fatalf("unexpected opts for single to: %+v", opts)
	}
}
