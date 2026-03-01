package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

func TestWriterWritesSessionAndIndex(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-1",
		CWD:       "/Users/mithilesh/Code/clis/resume",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventUserMessage, Msg: &session.Message{Role: "user", Content: "hello", Timestamp: now}},
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "call_1", Name: "shell_command", Input: map[string]any{"command": "git status --short"}, Timestamp: now.Add(time.Second)}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "call_1", Output: " M foo.go", Timestamp: now.Add(2 * time.Second)}},
			{Kind: session.EventAssistantMessage, Msg: &session.Message{Role: "assistant", Content: "done", Timestamp: now.Add(3 * time.Second)}},
		},
	}

	sessionID, sessionPath, err := w.Write(context.Background(), ir, session.ClaudeSessionMeta{CWD: ir.CWD})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if sessionID == "" || sessionPath == "" {
		t.Fatalf("missing outputs")
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("missing session file: %v", err)
	}

	indexPath := filepath.Join(filepath.Dir(sessionPath), "sessions-index.json")
	indexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(indexBytes), sessionID) {
		t.Fatalf("sessions-index missing session id")
	}

	f, err := os.Open(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var sawToolUse bool
	var sawToolResult bool
	var toolUseID string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("bad json line: %v", err)
		}
		msg, _ := line["message"].(map[string]any)
		if msg == nil {
			continue
		}
		content, _ := msg["content"].([]any)
		if len(content) == 0 {
			continue
		}
		first, _ := content[0].(map[string]any)
		if first == nil {
			continue
		}
		kind, _ := first["type"].(string)
		switch kind {
		case "tool_use":
			sawToolUse = true
			toolUseID, _ = first["id"].(string)
			if name, _ := first["name"].(string); name != "Bash" {
				t.Fatalf("tool_use name mismatch: %q", name)
			}
		case "tool_result":
			sawToolResult = true
			if got, _ := first["tool_use_id"].(string); got == "" || got != toolUseID {
				t.Fatalf("tool_result id mismatch: got %q want %q", got, toolUseID)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawToolUse || !sawToolResult {
		t.Fatalf("missing tool records: use=%v result=%v", sawToolUse, sawToolResult)
	}
}
