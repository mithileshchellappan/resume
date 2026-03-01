package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLoaderLoadByThreadID(t *testing.T) {
	home := t.TempDir()
	stateDB := filepath.Join(home, "state_1.sqlite")
	createThreadsSchema(t, stateDB)

	threadID := "thread-abc"
	rolloutPath := filepath.Join(home, "sessions", "2026", "03", "01", "rollout-test.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatal(err)
	}

	rollout := "" +
		`{"timestamp":"2026-03-01T08:00:00Z","type":"session_meta","payload":{"id":"thread-abc","cwd":"/repo","timestamp":"2026-03-01T08:00:00Z"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","arguments":"{\"command\":\"git status --short\"}","call_id":"call_1"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":" M foo.go"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}],"phase":"final_answer"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", stateDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createdAt := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC).Unix()
	_, err = db.Exec(`INSERT INTO threads (
		id, rollout_path, created_at, updated_at, source, model_provider, cwd, title,
		sandbox_policy, approval_mode, tokens_used, has_user_event, archived,
		archived_at, git_sha, git_branch, git_origin_url, cli_version, first_user_message
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		threadID, rolloutPath, createdAt, createdAt, "cli", "openai", "/repo", "title",
		"{}", "on-request", 0, 1, 0, nil, nil, nil, nil, "test", "hello",
	)
	if err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadByThreadID(context.Background(), threadID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if ir.SourceID != threadID {
		t.Fatalf("source id mismatch: %s", ir.SourceID)
	}
	if ir.CWD != "/repo" {
		t.Fatalf("cwd mismatch: %s", ir.CWD)
	}
	if len(ir.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ir.Messages))
	}
	if len(ir.Calls) != 1 || ir.Calls[0].Name != "shell_command" {
		t.Fatalf("unexpected calls: %+v", ir.Calls)
	}
	if len(ir.Results) != 1 || ir.Results[0].CallSourceID != "call_1" {
		t.Fatalf("unexpected results: %+v", ir.Results)
	}
	if got, want := len(ir.OrderedEvents), 4; got != want {
		t.Fatalf("ordered events mismatch: got %d want %d", got, want)
	}
}

func TestLoaderThreadNotFound(t *testing.T) {
	home := t.TempDir()
	loader := NewLoader(home)
	_, err := loader.LoadByThreadID(context.Background(), "missing")
	if err == nil {
		t.Fatalf("expected not found error")
	}
}
