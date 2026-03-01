package app

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mithileshchellappan/resume/internal/cli"
	_ "modernc.org/sqlite"
)

func TestRunEndToEnd(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")

	if err := os.MkdirAll(filepath.Join(claudeHome, "projects", "proj1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionPath := filepath.Join(claudeHome, "projects", "proj1", "abc-123.jsonl")
	sessionText := "" +
		`{"type":"user","timestamp":"2026-01-01T00:00:01Z","cwd":"/repo","sessionId":"abc-123","message":{"role":"user","content":"hello"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"hi"}]}}` + "\n"
	if err := os.WriteFile(sessionPath, []byte(sessionText), 0o644); err != nil {
		t.Fatal(err)
	}
	indexText := `{"version":1,"entries":[{"sessionId":"abc-123","fullPath":"` + sessionPath + `"}]}`
	if err := os.WriteFile(filepath.Join(claudeHome, "projects", "proj1", "sessions-index.json"), []byte(indexText), 0o644); err != nil {
		t.Fatal(err)
	}

	stateDB := filepath.Join(codexHome, "state_1.sqlite")
	createSchema(t, stateDB)

	opts := cli.Options{
		From:       "claude",
		To:         "codex",
		ID:         "abc-123",
		ClaudeHome: claudeHome,
		CodexHome:  codexHome,
	}

	res, err := Run(context.Background(), opts, "test")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ThreadID == "" || res.RolloutPath == "" {
		t.Fatalf("missing result identifiers: %+v", res)
	}
	if _, err := os.Stat(res.RolloutPath); err != nil {
		t.Fatalf("rollout missing: %v", err)
	}

	db, err := sql.Open("sqlite", stateDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM threads WHERE id = ?`, res.ThreadID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("thread not inserted")
	}

	idxBytes, err := os.ReadFile(filepath.Join(codexHome, "session_index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idxBytes), res.ThreadID) {
		t.Fatalf("session index missing thread id")
	}
}

func TestRunEndToEndCodexToClaude(t *testing.T) {
	root := t.TempDir()
	claudeHome := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")

	if err := os.MkdirAll(claudeHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions", "2026", "03", "01"), 0o755); err != nil {
		t.Fatal(err)
	}

	rolloutPath := filepath.Join(codexHome, "sessions", "2026", "03", "01", "rollout-test-thread.jsonl")
	rolloutText := "" +
		`{"timestamp":"2026-03-01T08:00:00Z","type":"session_meta","payload":{"id":"thread-1","cwd":"/Users/mithilesh/Code/clis/resume","timestamp":"2026-03-01T08:00:00Z"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:02Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","arguments":"{\"command\":\"git status --short\"}","call_id":"call_1"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:03Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":" M foo.go"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}],"phase":"final_answer"}}` + "\n"
	if err := os.WriteFile(rolloutPath, []byte(rolloutText), 0o644); err != nil {
		t.Fatal(err)
	}

	stateDB := filepath.Join(codexHome, "state_1.sqlite")
	createSchema(t, stateDB)
	db, err := sql.Open("sqlite", stateDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO threads (
		id, rollout_path, created_at, updated_at, source, model_provider, cwd, title,
		sandbox_policy, approval_mode, tokens_used, has_user_event, archived,
		archived_at, git_sha, git_branch, git_origin_url, cli_version, first_user_message
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"thread-1",
		rolloutPath,
		1700000000,
		1700000000,
		"cli",
		"openai",
		"/Users/mithilesh/Code/clis/resume",
		"title",
		"{}",
		"on-request",
		0,
		1,
		0,
		nil,
		nil,
		nil,
		nil,
		"test",
		"hello",
	)
	if err != nil {
		t.Fatal(err)
	}

	opts := cli.Options{
		From:       "codex",
		To:         "claude",
		ID:         "thread-1",
		ClaudeHome: claudeHome,
		CodexHome:  codexHome,
	}

	res, err := Run(context.Background(), opts, "test")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.SessionID == "" || res.SessionPath == "" {
		t.Fatalf("missing reverse result identifiers: %+v", res)
	}
	if _, err := os.Stat(res.SessionPath); err != nil {
		t.Fatalf("session file missing: %v", err)
	}

	indexPath := filepath.Join(filepath.Dir(res.SessionPath), "sessions-index.json")
	idxBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idxBytes), res.SessionID) {
		t.Fatalf("sessions-index missing session id")
	}

	f, err := os.Open(res.SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var sawToolUse bool
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("bad session json line: %v", err)
		}
		msg, _ := line["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		if len(content) == 0 {
			continue
		}
		first, _ := content[0].(map[string]any)
		if first == nil {
			continue
		}
		if typ, _ := first["type"].(string); typ == "tool_use" {
			sawToolUse = true
			if name, _ := first["name"].(string); name != "Bash" {
				t.Fatalf("unexpected claude tool name: %q", name)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawToolUse {
		t.Fatalf("expected tool_use entry in claude session")
	}
}

func createSchema(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := `CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		rollout_path TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		source TEXT NOT NULL,
		model_provider TEXT NOT NULL,
		cwd TEXT NOT NULL,
		title TEXT NOT NULL,
		sandbox_policy TEXT NOT NULL,
		approval_mode TEXT NOT NULL,
		tokens_used INTEGER NOT NULL DEFAULT 0,
		has_user_event INTEGER NOT NULL DEFAULT 0,
		archived INTEGER NOT NULL DEFAULT 0,
		archived_at INTEGER,
		git_sha TEXT,
		git_branch TEXT,
		git_origin_url TEXT,
		cli_version TEXT NOT NULL DEFAULT '',
		first_user_message TEXT NOT NULL DEFAULT ''
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
}
