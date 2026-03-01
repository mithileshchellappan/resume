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

func TestWriterNormalizesCodexToolNames(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		input     map[string]any
		wantName  string
		wantCheck func(t *testing.T, input map[string]any)
	}{
		{
			name:     "shell_command to Bash",
			toolName: "shell_command",
			input:    map[string]any{"command": "ls -la"},
			wantName: "Bash",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["command"] != "ls -la" {
					t.Fatalf("command: %v", input["command"])
				}
			},
		},
		{
			name:     "shell to Bash with array command",
			toolName: "shell",
			input:    map[string]any{"command": []any{"bash", "-lc", "git status"}},
			wantName: "Bash",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["command"] != "git status" {
					t.Fatalf("command: %v", input["command"])
				}
			},
		},
		{
			name:     "apply_patch to Edit",
			toolName: "apply_patch",
			input:    map[string]any{"content": "*** Begin Patch\n*** Update File: src/main.go\n@@\n-old\n+new\n*** End Patch"},
			wantName: "Edit",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["file_path"] != "src/main.go" {
					t.Fatalf("file_path: %v", input["file_path"])
				}
				ns, _ := input["new_string"].(string)
				if !strings.Contains(ns, "*** Begin Patch") {
					t.Fatalf("new_string missing patch: %v", ns)
				}
			},
		},
		{
			name:     "view_image to Read",
			toolName: "view_image",
			input:    map[string]any{"path": "/tmp/screenshot.png"},
			wantName: "Read",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["file_path"] != "/tmp/screenshot.png" {
					t.Fatalf("file_path: %v", input["file_path"])
				}
			},
		},
		{
			name:     "spawn_agent to Agent with default type",
			toolName: "spawn_agent",
			input:    map[string]any{"agent_type": "default", "message": "search for bugs"},
			wantName: "Agent",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["prompt"] != "search for bugs" {
					t.Fatalf("prompt: %v", input["prompt"])
				}
				if input["subagent_type"] != "general-purpose" {
					t.Fatalf("subagent_type: got %v want general-purpose", input["subagent_type"])
				}
			},
		},
		{
			name:     "spawn_agent normalizes explorer to Explore",
			toolName: "spawn_agent",
			input:    map[string]any{"agent_type": "explorer", "message": "find files"},
			wantName: "Agent",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["subagent_type"] != "Explore" {
					t.Fatalf("subagent_type: got %v want Explore", input["subagent_type"])
				}
			},
		},
		{
			name:     "spawn_agent normalizes planner to Plan",
			toolName: "spawn_agent",
			input:    map[string]any{"agent_type": "planner", "message": "design approach"},
			wantName: "Agent",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["subagent_type"] != "Plan" {
					t.Fatalf("subagent_type: got %v want Plan", input["subagent_type"])
				}
			},
		},
		{
			name:     "spawn_agent empty type defaults to general-purpose",
			toolName: "spawn_agent",
			input:    map[string]any{"agent_type": "", "message": "do stuff"},
			wantName: "Agent",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["subagent_type"] != "general-purpose" {
					t.Fatalf("subagent_type: got %v want general-purpose", input["subagent_type"])
				}
			},
		},
		{
			name:     "request_user_input to AskUserQuestion",
			toolName: "request_user_input",
			input:    map[string]any{"message": "which option?"},
			wantName: "AskUserQuestion",
		},
		{
			name:     "update_plan to TodoWrite",
			toolName: "update_plan",
			input:    map[string]any{"plan": []any{"step 1"}},
			wantName: "TodoWrite",
		},
		{
			name:     "MCP tool passes through",
			toolName: "mcp__XcodeBuildMCP__build_sim",
			input:    map[string]any{"scheme": "MyApp"},
			wantName: "mcp__XcodeBuildMCP__build_sim",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotInput := normalizeCodexToolForClaude(tt.toolName, tt.input)
			if gotName != tt.wantName {
				t.Fatalf("name: got %q want %q", gotName, tt.wantName)
			}
			if tt.wantCheck != nil {
				tt.wantCheck(t, gotInput)
			}
		})
	}
}

func TestWriterDropsCodexLifecycleTools(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-lifecycle",
		CWD:       "/tmp/test",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventUserMessage, Msg: &session.Message{Role: "user", Content: "hello", Timestamp: now}},
			// spawn_agent should be kept
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "call_spawn", Name: "spawn_agent", Input: map[string]any{"agent_type": "explorer", "message": "find stuff"}, Timestamp: now.Add(time.Second)}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "call_spawn", Output: `{"agent_id":"abc-123"}`, Timestamp: now.Add(2 * time.Second)}},
			// wait should be dropped
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "call_wait", Name: "wait", Input: map[string]any{"ids": []any{"abc-123"}, "timeout_ms": 120000}, Timestamp: now.Add(3 * time.Second)}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "call_wait", Output: `{"status":"completed"}`, Timestamp: now.Add(4 * time.Second)}},
			// close_agent should be dropped
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "call_close", Name: "close_agent", Input: map[string]any{"id": "abc-123"}, Timestamp: now.Add(5 * time.Second)}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "call_close", Output: `{"status":"closed"}`, Timestamp: now.Add(6 * time.Second)}},
			{Kind: session.EventAssistantMessage, Msg: &session.Message{Role: "assistant", Content: "done", Timestamp: now.Add(7 * time.Second)}},
		},
	}

	_, sessionPath, err := w.Write(context.Background(), ir, session.ClaudeSessionMeta{CWD: ir.CWD})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	f, err := os.Open(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var toolNames []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("bad json: %v", err)
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
		if kind, _ := first["type"].(string); kind == "tool_use" {
			name, _ := first["name"].(string)
			toolNames = append(toolNames, name)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	// Only spawn_agent (normalized to Agent) should remain; wait and close_agent should be filtered.
	if len(toolNames) != 1 {
		t.Fatalf("expected 1 tool call, got %d: %v", len(toolNames), toolNames)
	}
	if toolNames[0] != "Agent" {
		t.Fatalf("expected Agent, got %q", toolNames[0])
	}
}

func TestIsCodexLifecycleTool(t *testing.T) {
	lifecycle := []string{"wait", "close_agent", "Wait", "CLOSE_AGENT"}
	for _, name := range lifecycle {
		if !isCodexLifecycleTool(name) {
			t.Fatalf("expected %q to be lifecycle tool", name)
		}
	}
	notLifecycle := []string{"shell_command", "spawn_agent", "apply_patch", "mcp__foo__bar"}
	for _, name := range notLifecycle {
		if isCodexLifecycleTool(name) {
			t.Fatalf("expected %q to NOT be lifecycle tool", name)
		}
	}
}

func TestExtractPatchFilePath(t *testing.T) {
	tests := []struct {
		patch string
		want  string
	}{
		{"*** Begin Patch\n*** Update File: src/main.go\n@@\n-old\n+new", "src/main.go"},
		{"*** Begin Patch\n*** Add File: new/file.swift\n@@\n+content", "new/file.swift"},
		{"*** Begin Patch\n*** Delete File: old/file.go\n@@", "old/file.go"},
		{"some random text", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractPatchFilePath(tt.patch)
		if got != tt.want {
			t.Fatalf("extractPatchFilePath(%q): got %q want %q", tt.patch[:min(30, len(tt.patch))], got, tt.want)
		}
	}
}
