package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

func TestLoaderSkipsInjectedSystemAndEnvelopeMessages(t *testing.T) {
	home := t.TempDir()
	stateDB := filepath.Join(home, "state_1.sqlite")
	createThreadsSchema(t, stateDB)

	threadID := "thread-filter"
	rolloutPath := filepath.Join(home, "sessions", "2026", "03", "01", "rollout-filter.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatal(err)
	}

	rollout := "" +
		`{"timestamp":"2026-03-01T08:00:00Z","type":"session_meta","payload":{"id":"thread-filter","cwd":"/repo","timestamp":"2026-03-01T08:00:00Z"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>..." }]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /Users/me/repo"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>...</environment_context>"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"<collaboration_mode>...</collaboration_mode>"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"do exactly 3 tool calls"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:06Z","type":"response_item","payload":{"type":"function_call","name":"shell_command","arguments":"{\"command\":\"pwd\"}","call_id":"call_1"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:07Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Exit code: 0\nOutput:\n/repo"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:08Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<subagent_notification>{\"agent_id\":\"a1\"}</subagent_notification>"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:09Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<local-command-caveat>...</local-command-caveat>"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:10Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}` + "\n"
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
		"{}", "on-request", 0, 1, 0, nil, nil, nil, nil, "test", "do exactly 3 tool calls",
	)
	if err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadByThreadID(context.Background(), threadID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := len(ir.Messages), 2; got != want {
		t.Fatalf("expected %d messages after filtering, got %d", want, got)
	}
	if got, want := ir.Messages[0].Content, "do exactly 3 tool calls"; got != want {
		t.Fatalf("first message mismatch: got %q want %q", got, want)
	}
	if got, want := ir.Messages[1].Content, "done"; got != want {
		t.Fatalf("second message mismatch: got %q want %q", got, want)
	}
	if got, want := len(ir.Calls), 1; got != want {
		t.Fatalf("expected %d tool call, got %d", want, got)
	}
	if got, want := len(ir.Results), 1; got != want {
		t.Fatalf("expected %d tool result, got %d", want, got)
	}
	if got, want := len(ir.OrderedEvents), 4; got != want {
		t.Fatalf("expected %d ordered events, got %d", want, got)
	}
}

func TestLoaderHandlesCustomToolCalls(t *testing.T) {
	home := t.TempDir()
	stateDB := filepath.Join(home, "state_1.sqlite")
	createThreadsSchema(t, stateDB)

	threadID := "thread-custom-tools"
	rolloutPath := filepath.Join(home, "sessions", "2026", "03", "01", "rollout-custom.jsonl")
	if err := os.MkdirAll(filepath.Dir(rolloutPath), 0o755); err != nil {
		t.Fatal(err)
	}

	rollout := "" +
		`{"timestamp":"2026-03-01T08:00:00Z","type":"session_meta","payload":{"cwd":"/repo","timestamp":"2026-03-01T08:00:00Z"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fix the bug"}]}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:02Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_abc123","name":"apply_patch","input":"*** Begin Patch\n*** Update File: src/main.go\n@@\n-old line\n+new line\n*** End Patch"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:03Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_abc123","output":"{\"output\":\"Success. Updated the following files:\\nM src/main.go\\n\",\"metadata\":{\"exit_code\":0}}"}}` + "\n" +
		`{"timestamp":"2026-03-01T08:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Fixed it."}]}}` + "\n"
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
		threadID, rolloutPath, createdAt, createdAt, "cli", "openai", "/repo", "Custom tools",
		"{}", "on-request", 0, 1, 0, nil, nil, nil, nil, "test", "fix the bug",
	)
	if err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(home)
	ir, err := loader.LoadByThreadID(context.Background(), threadID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := len(ir.Calls), 1; got != want {
		t.Fatalf("expected %d tool call, got %d", want, got)
	}
	if ir.Calls[0].Name != "apply_patch" {
		t.Fatalf("expected apply_patch, got %q", ir.Calls[0].Name)
	}
	if ir.Calls[0].SourceID != "call_abc123" {
		t.Fatalf("wrong call id: %s", ir.Calls[0].SourceID)
	}
	content, _ := ir.Calls[0].Input["content"].(string)
	if !strings.Contains(content, "*** Begin Patch") {
		t.Fatalf("patch content not preserved: %v", ir.Calls[0].Input)
	}

	if got, want := len(ir.Results), 1; got != want {
		t.Fatalf("expected %d result, got %d", want, got)
	}
	if ir.Results[0].CallSourceID != "call_abc123" {
		t.Fatalf("result call id mismatch: %s", ir.Results[0].CallSourceID)
	}
	if !strings.Contains(ir.Results[0].Output, "Success") {
		t.Fatalf("expected success output, got %q", ir.Results[0].Output)
	}

	if got, want := len(ir.Messages), 2; got != want {
		t.Fatalf("expected %d messages, got %d", want, got)
	}
	if got, want := len(ir.OrderedEvents), 4; got != want {
		t.Fatalf("expected %d events, got %d", want, got)
	}
}
