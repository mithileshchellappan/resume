package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

func TestLoaderFindAndParse(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "projA")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "sess-1.jsonl")

	sessionContent := "" +
		`{"type":"progress","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"user","timestamp":"2026-01-01T00:00:01Z","cwd":"/repo","sessionId":"sess-1","message":{"role":"user","content":"hello"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"done"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la"}}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-1","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ir.SourceID != "sess-1" || ir.CWD != "/repo" {
		t.Fatalf("bad metadata: %+v", ir)
	}
	if len(ir.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ir.Messages))
	}
	if len(ir.Calls) != 1 || ir.Calls[0].Name != "Bash" {
		t.Fatalf("bad calls: %+v", ir.Calls)
	}
	if len(ir.Results) != 1 || ir.Results[0].Output != "ok" {
		t.Fatalf("bad results: %+v", ir.Results)
	}
	if got, want := len(ir.OrderedEvents), 4; got != want {
		t.Fatalf("ordered events: got %d want %d", got, want)
	}
	if ir.OrderedEvents[0].Kind != session.EventUserMessage || ir.OrderedEvents[3].Kind != session.EventToolResult {
		t.Fatalf("unexpected order: %+v", ir.OrderedEvents)
	}
}

func TestLoaderErrors(t *testing.T) {
	home := t.TempDir()

	loader := NewLoader(home)
	_, err := loader.LoadBySessionID(context.Background(), "missing")
	if err == nil || !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}

	p1 := filepath.Join(home, "projects", "p1")
	p2 := filepath.Join(home, "projects", "p2")
	_ = os.MkdirAll(p1, 0o755)
	_ = os.MkdirAll(p2, 0o755)
	_ = os.WriteFile(filepath.Join(p1, "sessions-index.json"), []byte(`{"entries":[{"sessionId":"dup","fullPath":"/tmp/a"}]}`), 0o644)
	_ = os.WriteFile(filepath.Join(p2, "sessions-index.json"), []byte(`{"entries":[{"sessionId":"dup","fullPath":"/tmp/b"}]}`), 0o644)

	_, err = loader.LoadBySessionID(context.Background(), "dup")
	if err == nil || !errors.Is(err, ErrSessionDuplicate) {
		t.Fatalf("expected duplicate, got %v", err)
	}
}

func TestLoaderFallbackWithoutSessionsIndex(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-no-index")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "4421e858-30a7-47d0-a9eb-ac4e8be53dce"
	sessionPath := filepath.Join(projectDir, sessionID+".jsonl")
	content := `{"type":"user","timestamp":"2026-03-01T07:30:29.647Z","cwd":"/Users/mithilesh/Code/clis/resume","sessionId":"` + sessionID + `","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load with fallback: %v", err)
	}
	if ir.SourceID != sessionID {
		t.Fatalf("unexpected source id: %s", ir.SourceID)
	}
	if ir.CWD != "/Users/mithilesh/Code/clis/resume" {
		t.Fatalf("unexpected cwd: %s", ir.CWD)
	}
	if len(ir.Messages) != 1 || ir.Messages[0].Content != "hello" {
		t.Fatalf("unexpected messages: %+v", ir.Messages)
	}
}

func TestLoaderStripsToolUseErrorWrapper(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-errors")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "sess-errors.jsonl")

	sessionContent := "" +
		`{"type":"assistant","timestamp":"2026-03-01T07:46:39Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"git log --oneline -5"}}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-03-01T07:46:40Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"<tool_use_error>Sibling tool call errored</tool_use_error>","is_error":true}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-errors","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), "sess-errors")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ir.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(ir.Results))
	}
	if got, want := ir.Results[0].Output, "Sibling tool call errored"; got != want {
		t.Fatalf("wrapped tool error should be stripped: got %q want %q", got, want)
	}
}

func TestLoaderIgnoresLocalCommandEnvelopeMessages(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-local-cmd")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "sess-local-cmd.jsonl")

	sessionContent := "" +
		`{"type":"user","timestamp":"2026-03-01T07:46:35Z","message":{"role":"user","content":"real user message"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-03-01T07:46:36Z","message":{"role":"assistant","content":[{"type":"text","text":"real assistant message"}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-03-01T07:46:37Z","message":{"role":"user","content":"<local-command-caveat>ignore me</local-command-caveat>"}}` + "\n" +
		`{"type":"user","timestamp":"2026-03-01T07:46:38Z","message":{"role":"user","content":"<command-name>/exit</command-name>\n<command-message>exit</command-message>\n<command-args></command-args>"}}` + "\n" +
		`{"type":"user","timestamp":"2026-03-01T07:46:39Z","message":{"role":"user","content":"<local-command-stdout>Catch you later!</local-command-stdout>"}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-local-cmd","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), "sess-local-cmd")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := len(ir.Messages), 2; got != want {
		t.Fatalf("unexpected message count: got %d want %d, messages=%+v", got, want, ir.Messages)
	}
	if ir.Messages[0].Content != "real user message" || ir.Messages[1].Content != "real assistant message" {
		t.Fatalf("unexpected messages: %+v", ir.Messages)
	}
}

func TestLoaderParsesServerToolUseBlocks(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-server-tools")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "sess-server-tools.jsonl")

	sessionContent := "" +
		`{"type":"user","timestamp":"2026-03-01T08:00:00Z","cwd":"/repo","sessionId":"sess-server-tools","message":{"role":"user","content":"search for Go tutorials"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-03-01T08:00:01Z","message":{"role":"assistant","content":[{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"Go tutorials"}},{"type":"text","text":"Let me search for that."}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-03-01T08:00:02Z","message":{"role":"user","content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://go.dev/tour","title":"A Tour of Go","snippet":"Interactive Go tutorial"}]}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-server-tools","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), "sess-server-tools")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ir.Calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(ir.Calls))
	}
	if ir.Calls[0].Name != "web_search" {
		t.Fatalf("expected web_search tool call, got %q", ir.Calls[0].Name)
	}
	if ir.Calls[0].SourceID != "srvtoolu_1" {
		t.Fatalf("expected source id srvtoolu_1, got %q", ir.Calls[0].SourceID)
	}
	if len(ir.Results) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(ir.Results))
	}
	if ir.Results[0].CallSourceID != "srvtoolu_1" {
		t.Fatalf("result call source id mismatch: %q", ir.Results[0].CallSourceID)
	}
	if ir.Results[0].Output == "" {
		t.Fatalf("expected non-empty result output")
	}
	// Should have: user_message, tool_call, assistant_message (text), tool_result
	if len(ir.OrderedEvents) != 4 {
		t.Fatalf("expected 4 ordered events, got %d: %+v", len(ir.OrderedEvents), ir.OrderedEvents)
	}
}

func TestLoaderAttachesThinkingAcrossJSONLLines(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-cross-line-thinking")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "sess-thinking.jsonl")

	// Claude emits thinking and text as separate JSONL lines.
	sessionContent := "" +
		`{"type":"user","timestamp":"2026-03-01T08:00:00Z","cwd":"/repo","sessionId":"sess-thinking","message":{"role":"user","content":"explain this code"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-03-01T08:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me analyze the structure first."}]}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-03-01T08:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"Here is the explanation."}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-thinking","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), "sess-thinking")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Should have 2 messages: user + assistant.
	if len(ir.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(ir.Messages), ir.Messages)
	}

	assistant := ir.Messages[1]
	if assistant.Content != "Here is the explanation." {
		t.Fatalf("assistant content mismatch: %q", assistant.Content)
	}
	if assistant.Reasoning != "Let me analyze the structure first." {
		t.Fatalf("reasoning not attached across JSONL lines: got %q", assistant.Reasoning)
	}
}

func TestLoaderDoesNotAttachAssistantThinkingToUserMessages(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-reasoning-boundary")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(projectDir, "sess-reasoning-boundary.jsonl")

	sessionContent := "" +
		`{"type":"user","timestamp":"2026-03-01T08:00:00Z","cwd":"/repo","sessionId":"sess-reasoning-boundary","message":{"role":"user","content":"start"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-03-01T08:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"private plan"}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-03-01T08:00:02Z","message":{"role":"user","content":"Actually, new request"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-03-01T08:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"Sure."}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionContent), 0o644); err != nil {
		t.Fatal(err)
	}

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-reasoning-boundary","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadBySessionID(context.Background(), "sess-reasoning-boundary")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ir.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(ir.Messages), ir.Messages)
	}

	second := ir.Messages[1]
	if second.Role != "user" {
		t.Fatalf("expected second message to be user, got role=%q", second.Role)
	}
	if second.Reasoning != "" {
		t.Fatalf("user message should not inherit assistant reasoning: got %q", second.Reasoning)
	}

	third := ir.Messages[2]
	if third.Role != "assistant" {
		t.Fatalf("expected third message to be assistant, got role=%q", third.Role)
	}
	if third.Reasoning != "" {
		t.Fatalf("stale reasoning should be cleared before next assistant text: got %q", third.Reasoning)
	}
}

func TestLoaderListSessions(t *testing.T) {
	home := t.TempDir()
	projectDir := filepath.Join(home, "projects", "proj-list")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeSession := func(id, cwd, userText string, modTime time.Time) string {
		t.Helper()
		path := filepath.Join(projectDir, id+".jsonl")
		content := "" +
			`{"type":"user","timestamp":"2026-03-01T07:30:29.647Z","cwd":"` + cwd + `","sessionId":"` + id + `","message":{"role":"user","content":"` + userText + `"}}` + "\n" +
			`{"type":"assistant","timestamp":"2026-03-01T07:30:30.000Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}` + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		return path
	}

	newestPath := writeSession(
		"sess-new",
		"/repo/new",
		"new title",
		time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC),
	)
	oldestPath := writeSession(
		"sess-old",
		"/repo/old",
		"",
		time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC),
	)

	indexJSON := `{"version":1,"entries":[{"sessionId":"sess-old","fullPath":"` + oldestPath + `"},{"sessionId":"sess-new","fullPath":"` + newestPath + `"}]}`
	if err := os.WriteFile(filepath.Join(projectDir, "sessions-index.json"), []byte(indexJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	sessions, err := loader.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if got, want := len(sessions), 2; got != want {
		t.Fatalf("session count mismatch: got %d want %d", got, want)
	}
	if sessions[0].ID != "sess-new" {
		t.Fatalf("expected newest first, got %+v", sessions)
	}
	if sessions[0].Title != "new title" {
		t.Fatalf("unexpected title: %q", sessions[0].Title)
	}
	if sessions[0].CWD != "/repo/new" {
		t.Fatalf("unexpected cwd: %q", sessions[0].CWD)
	}
	if sessions[0].SizeBytes == 0 {
		t.Fatalf("expected non-zero size bytes")
	}
	if sessions[1].Title != "sess-old" {
		t.Fatalf("expected fallback title to id, got %q", sessions[1].Title)
	}
}
