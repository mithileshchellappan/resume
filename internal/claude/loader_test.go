package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
