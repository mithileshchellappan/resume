package main

import (
	"path/filepath"
	"testing"

	"github.com/mithileshchellappan/resume/internal/app"
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
