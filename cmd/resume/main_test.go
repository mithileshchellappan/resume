package main

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mithileshchellappan/resume/internal/app"
	"github.com/mithileshchellappan/resume/internal/cli"
)

func TestBuildTargetResumeCommand(t *testing.T) {
	tests := []struct {
		name   string
		target string
		result app.Result
		want   []string
	}{
		{
			name:   "codex target",
			target: "codex",
			result: app.Result{ThreadID: "thread-123"},
			want:   []string{"codex", "resume", "thread-123"},
		},
		{
			name:   "claude target",
			target: "claude",
			result: app.Result{SessionID: "session-123"},
			want:   []string{"claude", "--resume", "session-123"},
		},
		{
			name:   "missing id returns nil",
			target: "claude",
			result: app.Result{},
			want:   nil,
		},
		{
			name:   "unsupported target returns nil",
			target: "unknown",
			result: app.Result{ThreadID: "x"},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildTargetResumeCommand(tt.target, tt.result)
			if tt.want == nil {
				if cmd != nil {
					t.Fatalf("expected nil command, got %+v", cmd)
				}
				return
			}
			if cmd == nil {
				t.Fatalf("expected command")
			}
			if got := append([]string{filepath.Base(cmd.Path)}, cmd.Args[1:]...); len(got) != len(tt.want) {
				t.Fatalf("args length mismatch: got %v want %v", got, tt.want)
			} else {
				for i := range got {
					if got[i] != tt.want[i] {
						t.Fatalf("arg %d mismatch: got %q want %q", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestDetectInstalledTools(t *testing.T) {
	got := detectInstalledTools(func(file string) (string, error) {
		switch file {
		case "claude", "codex":
			return "/usr/bin/" + file, nil
		default:
			return "", errors.New("not found")
		}
	})
	want := []string{"claude", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installed tools mismatch: got %v want %v", got, want)
	}
}

func TestResolveToolsWithPickerBothMissing(t *testing.T) {
	opts := cli.Options{}
	calls := make([]struct {
		title string
		tools []string
	}, 0)
	picker := func(_ io.Reader, _ io.Writer, title string, tools []string) (string, error) {
		calls = append(calls, struct {
			title string
			tools []string
		}{
			title: title,
			tools: append([]string(nil), tools...),
		})
		if title == "Select Source Tool" {
			return "codex", nil
		}
		return "claude", nil
	}

	err := resolveToolsWithPicker(&opts, []string{"claude", "codex"}, strings.NewReader(""), &bytes.Buffer{}, picker)
	if err != nil {
		t.Fatalf("resolve tools: %v", err)
	}
	if opts.From != "codex" || opts.To != "claude" {
		t.Fatalf("unexpected resolved opts: %+v", opts)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 picker calls, got %d", len(calls))
	}
	if calls[0].title != "Select Source Tool" {
		t.Fatalf("unexpected first picker title: %s", calls[0].title)
	}
	if calls[1].title != "Select Target Tool" {
		t.Fatalf("unexpected second picker title: %s", calls[1].title)
	}
	if !reflect.DeepEqual(calls[1].tools, []string{"claude"}) {
		t.Fatalf("target options should exclude selected source, got %v", calls[1].tools)
	}
}

func TestResolveToolsWithPickerSingleMissingSide(t *testing.T) {
	opts := cli.Options{From: "codex"}
	calls := 0
	var lastTitle string
	var lastTools []string
	picker := func(_ io.Reader, _ io.Writer, title string, tools []string) (string, error) {
		calls++
		lastTitle = title
		lastTools = append([]string(nil), tools...)
		return "claude", nil
	}

	err := resolveToolsWithPicker(&opts, []string{"claude", "codex"}, strings.NewReader(""), &bytes.Buffer{}, picker)
	if err != nil {
		t.Fatalf("resolve tools: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one picker call, got %d", calls)
	}
	if lastTitle != "Select Target Tool" {
		t.Fatalf("unexpected picker title: %s", lastTitle)
	}
	if !reflect.DeepEqual(lastTools, []string{"claude"}) {
		t.Fatalf("unexpected picker options: %v", lastTools)
	}
	if opts.To != "claude" {
		t.Fatalf("expected resolved target claude, got %s", opts.To)
	}
}

func TestResolveToolsWithPickerMissingInstallFails(t *testing.T) {
	opts := cli.Options{From: "codex"}
	err := resolveToolsWithPicker(&opts, []string{"claude"}, strings.NewReader(""), &bytes.Buffer{}, func(io.Reader, io.Writer, string, []string) (string, error) {
		t.Fatalf("picker should not be called")
		return "", nil
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var usageErr cli.UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("expected UsageError, got %T", err)
	}
}

func TestResolveToolsWithPickerNoInstalledTools(t *testing.T) {
	opts := cli.Options{}
	err := resolveToolsWithPicker(&opts, nil, strings.NewReader(""), &bytes.Buffer{}, func(io.Reader, io.Writer, string, []string) (string, error) {
		t.Fatalf("picker should not be called")
		return "", nil
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	var usageErr cli.UsageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("expected UsageError, got %T", err)
	}
}
