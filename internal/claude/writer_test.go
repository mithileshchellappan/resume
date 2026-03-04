package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
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
	var bashToolUseResult map[string]any
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
			var ok bool
			bashToolUseResult, ok = line["toolUseResult"].(map[string]any)
			if !ok {
				t.Fatalf("expected Bash toolUseResult object, got %T", line["toolUseResult"])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawToolUse || !sawToolResult {
		t.Fatalf("missing tool records: use=%v result=%v", sawToolUse, sawToolResult)
	}
	if ok, err := regexp.MatchString(`^toolu_[0-9A-Za-z]{24}$`, toolUseID); err != nil || !ok {
		t.Fatalf("tool_use id format mismatch: %q", toolUseID)
	}
	if got, _ := bashToolUseResult["stdout"].(string); got != "M foo.go" {
		t.Fatalf("bash stdout mismatch: got %q want %q", got, "M foo.go")
	}
	if got, _ := bashToolUseResult["stderr"].(string); got != "" {
		t.Fatalf("bash stderr mismatch: got %q want empty", got)
	}
	if got, ok := bashToolUseResult["interrupted"].(bool); !ok || got {
		t.Fatalf("bash interrupted mismatch: got %#v", bashToolUseResult["interrupted"])
	}
	if got, ok := bashToolUseResult["isImage"].(bool); !ok || got {
		t.Fatalf("bash isImage mismatch: got %#v", bashToolUseResult["isImage"])
	}
	if got, ok := bashToolUseResult["noOutputExpected"].(bool); !ok || got {
		t.Fatalf("bash noOutputExpected mismatch: got %#v", bashToolUseResult["noOutputExpected"])
	}
}

func TestWriterUnwrapsJSONWrappedToolResultOutput(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-json-output",
		CWD:       "/Users/mithilesh/Code/clis/resume",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventUserMessage, Msg: &session.Message{Role: "user", Content: "apply patch", Timestamp: now}},
			{Kind: session.EventToolCall, Call: &session.ToolCall{
				SourceID:  "call_edit_1",
				Name:      "Edit",
				Input:     map[string]any{"file_path": "src/main.go", "old_string": "old", "new_string": "new"},
				Timestamp: now.Add(time.Second),
			}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{
				CallSourceID: "call_edit_1",
				Output: `{"output":"Success. Updated the following files:
M src/main.go
","metadata":{"exit_code":0}}`,
				Timestamp: now.Add(2 * time.Second),
			}},
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

	var toolResultContent string
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
		if kind, _ := first["type"].(string); kind == "tool_result" {
			toolResultContent, _ = first["content"].(string)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if strings.HasPrefix(strings.TrimSpace(toolResultContent), "{") {
		t.Fatalf("tool_result should be unwrapped plain text, got: %q", toolResultContent)
	}
	if !strings.Contains(toolResultContent, "Success. Updated the following files") {
		t.Fatalf("expected unwrapped success message, got: %q", toolResultContent)
	}
}

func TestWriterBuildsStructuredEditToolUseResult(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-edit-result-shape",
		CWD:       "/Users/mithilesh/Code/clis/resume",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventToolCall, Call: &session.ToolCall{
				SourceID:  "call_edit_1",
				Name:      "Edit",
				Input:     map[string]any{"file_path": "src/main.go", "old_string": "old", "new_string": "new", "replace_all": false},
				Timestamp: now.Add(time.Second),
			}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{
				CallSourceID: "call_edit_1",
				Output:       "The file src/main.go has been updated successfully.",
				Timestamp:    now.Add(2 * time.Second),
			}},
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

	var editToolUseResult map[string]any
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
		if kind, _ := first["type"].(string); kind == "tool_result" {
			var ok bool
			editToolUseResult, ok = line["toolUseResult"].(map[string]any)
			if !ok {
				t.Fatalf("expected Edit toolUseResult object, got %T", line["toolUseResult"])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if got, _ := editToolUseResult["filePath"].(string); got != "src/main.go" {
		t.Fatalf("filePath mismatch: got %q want %q", got, "src/main.go")
	}
	if got, _ := editToolUseResult["oldString"].(string); got != "old" {
		t.Fatalf("oldString mismatch: got %q want %q", got, "old")
	}
	if got, _ := editToolUseResult["newString"].(string); got != "new" {
		t.Fatalf("newString mismatch: got %q want %q", got, "new")
	}
	if _, ok := editToolUseResult["originalFile"].(string); !ok {
		t.Fatalf("originalFile should be string: %#v", editToolUseResult["originalFile"])
	}
	if _, ok := editToolUseResult["structuredPatch"].([]any); !ok {
		t.Fatalf("structuredPatch should be []any: %#v", editToolUseResult["structuredPatch"])
	}
	if got, ok := editToolUseResult["replaceAll"].(bool); !ok || got {
		t.Fatalf("replaceAll mismatch: got %#v", editToolUseResult["replaceAll"])
	}
	if got, ok := editToolUseResult["userModified"].(bool); !ok || got {
		t.Fatalf("userModified mismatch: got %#v", editToolUseResult["userModified"])
	}
}

func TestWriterEditToolUseResultDoesNotReadOriginalFileFromDisk(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	workspace := t.TempDir()
	target := filepath.Join(workspace, "main.go")
	if err := os.WriteFile(target, []byte(strings.Repeat("x", 50_000)), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	ir := session.SessionIR{
		SourceID:  "codex-thread-edit-no-disk-hydrate",
		CWD:       workspace,
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventToolCall, Call: &session.ToolCall{
				SourceID: "call_edit_1",
				Name:     "Edit",
				Input: map[string]any{
					"file_path":  target,
					"new_string": "new",
				},
				Timestamp: now.Add(time.Second),
			}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{
				CallSourceID: "call_edit_1",
				Output:       "ok",
				Timestamp:    now.Add(2 * time.Second),
			}},
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

	var editToolUseResult map[string]any
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
		if kind, _ := first["type"].(string); kind == "tool_result" {
			var ok bool
			editToolUseResult, ok = line["toolUseResult"].(map[string]any)
			if !ok {
				t.Fatalf("expected Edit toolUseResult object, got %T", line["toolUseResult"])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if got, _ := editToolUseResult["originalFile"].(string); got != "" {
		t.Fatalf("originalFile should not be hydrated from disk: len=%d", len(got))
	}
}

func TestWriterBuildsStructuredAgentToolUseResult(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-agent-result-shape",
		CWD:       "/Users/mithilesh/Code/clis/resume",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventToolCall, Call: &session.ToolCall{
				SourceID:  "call_agent_1",
				Name:      "spawn_agent",
				Input:     map[string]any{"agent_type": "explorer", "message": "find tests"},
				Timestamp: now.Add(time.Second),
			}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{
				CallSourceID: "call_agent_1",
				Output:       `{"agent_id":"abc-123","status":"completed"}`,
				Timestamp:    now.Add(2 * time.Second),
			}},
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

	var agentToolUseResult map[string]any
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
		if kind, _ := first["type"].(string); kind == "tool_result" {
			var ok bool
			agentToolUseResult, ok = line["toolUseResult"].(map[string]any)
			if !ok {
				t.Fatalf("expected Agent toolUseResult object, got %T", line["toolUseResult"])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if got, _ := agentToolUseResult["agentId"].(string); got != "abc-123" {
		t.Fatalf("agentId mismatch: got %q want %q", got, "abc-123")
	}
	if got, _ := agentToolUseResult["status"].(string); got != "completed" {
		t.Fatalf("status mismatch: got %q want %q", got, "completed")
	}
	if got, _ := agentToolUseResult["prompt"].(string); got != "find tests" {
		t.Fatalf("prompt mismatch: got %q want %q", got, "find tests")
	}
	if got, ok := agentToolUseResult["usage"].(map[string]any); !ok || got == nil {
		t.Fatalf("usage mismatch: %#v", agentToolUseResult["usage"])
	}
}

func TestWriterBuildsStructuredAskUserQuestionToolUseResult(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-ask-user-question",
		CWD:       "/Users/mithilesh/Code/clis/resume",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventToolCall, Call: &session.ToolCall{
				SourceID: "call_ask_1",
				Name:     "request_user_input",
				Input: map[string]any{
					"questions": []any{
						map[string]any{
							"id":       "fix_scope",
							"header":   "Fix scope",
							"question": "For the first patch, which compatibility scope do you want?",
							"options": []any{
								map[string]any{"label": "Edit-first (Recommended)", "description": "smallest change"},
								map[string]any{"label": "All tool parity", "description": "broader change"},
							},
						},
					},
				},
				Timestamp: now.Add(time.Second),
			}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{
				CallSourceID: "call_ask_1",
				Output:       `{"answers":{"fix_scope":{"answers":["All tool parity"]}}}`,
				Timestamp:    now.Add(2 * time.Second),
			}},
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

	var askToolUseResult map[string]any
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
		if kind, _ := first["type"].(string); kind == "tool_result" {
			var ok bool
			askToolUseResult, ok = line["toolUseResult"].(map[string]any)
			if !ok {
				t.Fatalf("expected AskUserQuestion toolUseResult object, got %T", line["toolUseResult"])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	questions, ok := askToolUseResult["questions"].([]any)
	if !ok || len(questions) == 0 {
		t.Fatalf("questions mismatch: %#v", askToolUseResult["questions"])
	}
	answers, ok := askToolUseResult["answers"].(map[string]any)
	if !ok {
		t.Fatalf("answers mismatch: %#v", askToolUseResult["answers"])
	}
	key := "For the first patch, which compatibility scope do you want?"
	if got, _ := answers[key].(string); got != "All tool parity" {
		t.Fatalf("answer mismatch: got %q want %q", got, "All tool parity")
	}
}

func TestBuildAskUserQuestionToolUseResultAlwaysObject(t *testing.T) {
	toolInput := map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "scope",
				"question": "Choose scope?",
			},
		},
	}

	tests := []struct {
		name         string
		output       string
		rawOutput    string
		wantResponse bool
	}{
		{
			name:         "truncated wrapper with partial json",
			output:       "[truncated for target model context; original chars=256, removed=160]\n{\"answers\":{\"scope\":{\"answers\":[\"All tool parity\"",
			rawOutput:    "",
			wantResponse: true,
		},
		{
			name:         "plain text fallback",
			output:       "user selected option 2",
			rawOutput:    "",
			wantResponse: true,
		},
		{
			name:         "empty output keeps empty answers map",
			output:       "",
			rawOutput:    "",
			wantResponse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAny := buildAskUserQuestionToolUseResult(toolInput, tt.output, tt.rawOutput)
			got, ok := gotAny.(map[string]any)
			if !ok {
				t.Fatalf("expected map toolUseResult, got %T", gotAny)
			}
			answers, ok := got["answers"].(map[string]any)
			if !ok || answers == nil {
				t.Fatalf("expected answers map, got %#v", got["answers"])
			}
			if tt.wantResponse {
				resp, ok := answers["response"].(string)
				if !ok || strings.TrimSpace(resp) == "" {
					t.Fatalf("expected fallback response, got %#v", answers["response"])
				}
			}
			if !tt.wantResponse && len(answers) != 0 {
				t.Fatalf("expected empty answers map, got %#v", answers)
			}
			if _, ok := got["questions"].([]any); !ok {
				t.Fatalf("expected questions array, got %#v", got["questions"])
			}
		})
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
			name:     "Edit normalizes filePath alias",
			toolName: "Edit",
			input:    map[string]any{"filePath": "src/main.go", "old_string": "old", "new_string": "new"},
			wantName: "Edit",
			wantCheck: func(t *testing.T, input map[string]any) {
				if input["file_path"] != "src/main.go" {
					t.Fatalf("file_path: got %v want src/main.go", input["file_path"])
				}
				if _, ok := input["filePath"]; ok {
					t.Fatalf("filePath alias should be removed: %+v", input)
				}
			},
		},
		{
			name:     "Edit defaults empty file_path when missing",
			toolName: "Edit",
			input:    map[string]any{"new_string": "content"},
			wantName: "Edit",
			wantCheck: func(t *testing.T, input map[string]any) {
				v, ok := input["file_path"].(string)
				if !ok {
					t.Fatalf("file_path missing or non-string: %+v", input)
				}
				if v != "" {
					t.Fatalf("file_path: got %q want empty string", v)
				}
			},
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

func TestNormalizeEventsForClaudeContextTruncatesOnlyToolResultContent(t *testing.T) {
	huge := strings.Repeat("A", 4000)
	events := []session.Event{
		{
			Kind: session.EventUserMessage,
			Msg: &session.Message{
				Role:    "user",
				Content: huge,
			},
		},
		{
			Kind: session.EventToolCall,
			Call: &session.ToolCall{
				SourceID: "call_1",
				Name:     "Edit",
				Input: map[string]any{
					"file_path":  "src/main.go",
					"new_string": huge,
				},
			},
		},
		{
			Kind: session.EventToolResult,
			Result: &session.ToolResult{
				CallSourceID: "call_1",
				Output:       huge,
			},
		},
		{
			Kind: session.EventAssistantMessage,
			Msg: &session.Message{
				Role:    "assistant",
				Content: "kept recent context",
			},
		},
	}

	out := normalizeEventsForClaudeContext(events, 1300)
	if got := out[0].Msg.Content; got != huge {
		t.Fatalf("expected user message to remain unchanged, got %q", got[:min(len(got), 80)])
	}
	toolInput := out[1].Call.Input
	newString, _ := toolInput["new_string"].(string)
	if newString != huge {
		t.Fatalf("expected tool input to remain unchanged, got %q", newString[:min(len(newString), 80)])
	}
	if got := out[2].Result.Output; !strings.HasPrefix(got, "[truncated for target model context;") {
		t.Fatalf("expected tool result truncation, got %q", got[:min(len(got), 80)])
	}
	if got := out[3].Msg.Content; got != "kept recent context" {
		t.Fatalf("assistant message should remain unchanged, got %q", got)
	}
}

func TestNormalizeEventsForClaudeContextKeepsToolResultWhenWithinLimit(t *testing.T) {
	huge := strings.Repeat("Z", 1200)
	events := []session.Event{
		{
			Kind: session.EventToolResult,
			Result: &session.ToolResult{
				CallSourceID: "call_1",
				Output:       huge,
			},
		},
	}

	out := normalizeEventsForClaudeContext(events, 10_000)
	if len(out) != 1 || out[0].Result == nil {
		t.Fatalf("unexpected output shape: %#v", out)
	}
	if got := out[0].Result.Output; got != huge {
		t.Fatalf("expected output unchanged within limit")
	}
}

func TestTruncateForContextAvoidsNestedMarkers(t *testing.T) {
	orig := strings.Repeat("output-line\n", 500)
	first := truncateForContext(orig, 256)
	second := truncateForContext(first, 96)

	if got := strings.Count(second, claudeTruncationPrefix); got != 1 {
		t.Fatalf("expected a single truncation marker, got %d in %q", got, second[:min(len(second), 120)])
	}
	body, ok := unwrapTruncatedBody(second)
	if !ok {
		t.Fatalf("expected truncation body")
	}
	if strings.HasPrefix(body, claudeTruncationPrefix) {
		t.Fatalf("body should contain payload content, got nested marker")
	}
	if strings.TrimSpace(body) == "" {
		t.Fatalf("body should retain payload content")
	}
}

func TestWriterEmitsThinkingBlocksForReasoningMessages(t *testing.T) {
	home := t.TempDir()
	w := NewWriter(home)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }

	ir := session.SessionIR{
		SourceID:  "codex-thread-thinking",
		CWD:       "/Users/mithilesh/Code/clis/resume",
		StartedAt: now,
		OrderedEvents: []session.Event{
			{Kind: session.EventUserMessage, Msg: &session.Message{Role: "user", Content: "explain this", Timestamp: now}},
			{Kind: session.EventAssistantMessage, Msg: &session.Message{Role: "assistant", Content: "Here is the explanation.", Reasoning: "Let me think about the code structure.", Timestamp: now.Add(time.Second)}},
			{Kind: session.EventAssistantMessage, Msg: &session.Message{Role: "assistant", Content: "plain reply", Timestamp: now.Add(2 * time.Second)}},
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

	var sawThinking bool
	var sawPlain bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("bad json: %v", err)
		}
		lineType, _ := line["type"].(string)
		if lineType != "assistant" {
			continue
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
		if kind == "thinking" {
			sawThinking = true
			thinking, _ := first["thinking"].(string)
			if thinking != "Let me think about the code structure." {
				t.Fatalf("thinking content mismatch: %q", thinking)
			}
			if len(content) < 2 {
				t.Fatalf("expected text block after thinking, got %d blocks", len(content))
			}
			textBlock, _ := content[1].(map[string]any)
			text, _ := textBlock["text"].(string)
			if text != "Here is the explanation." {
				t.Fatalf("text after thinking mismatch: %q", text)
			}
		} else if kind == "text" {
			text, _ := first["text"].(string)
			if text == "plain reply" {
				sawPlain = true
				if len(content) != 1 {
					t.Fatalf("plain message should have exactly 1 content block, got %d", len(content))
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawThinking {
		t.Fatalf("missing assistant message with thinking block")
	}
	if !sawPlain {
		t.Fatalf("missing plain assistant message without thinking")
	}
}

func TestNormalizeEventsForClaudeContextDoesNotMutateInput(t *testing.T) {
	huge := strings.Repeat("B", 5000)
	events := []session.Event{
		{
			Kind: session.EventUserMessage,
			Msg: &session.Message{
				Role:    "user",
				Content: huge,
			},
		},
		{
			Kind: session.EventToolCall,
			Call: &session.ToolCall{
				SourceID: "call_1",
				Name:     "Edit",
				Input: map[string]any{
					"file_path":  "src/main.go",
					"new_string": huge,
				},
			},
		},
		{
			Kind: session.EventToolResult,
			Result: &session.ToolResult{
				CallSourceID: "call_1",
				Output:       huge,
			},
		},
	}

	_ = normalizeEventsForClaudeContext(events, 1200)

	if got := events[0].Msg.Content; got != huge {
		t.Fatalf("input message mutated")
	}
	gotNewString, _ := events[1].Call.Input["new_string"].(string)
	if gotNewString != huge {
		t.Fatalf("input call args mutated")
	}
	if got := events[2].Result.Output; got != huge {
		t.Fatalf("input tool result mutated")
	}
}
