package codex

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

func TestWriterWritesRolloutDBAndIndex(t *testing.T) {
	home := t.TempDir()
	stateDB := filepath.Join(home, "state_1.sqlite")
	createThreadsSchema(t, stateDB)

	w := NewWriter(home)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	w.Now = func() time.Time { return now }

	s := session.CodexSession{
		SourceSessionID:  "sess-1",
		CWD:              "/repo",
		StartedAt:        now,
		HasUserEvent:     true,
		FirstUserMessage: "hello",
		Items: []session.CodexItem{
			{Kind: session.CodexItemUserMessage, Role: "user", Text: "hello", Timestamp: now},
			{Kind: session.CodexItemFunctionCall, CallID: "call_abc", Name: "shell", Arguments: map[string]any{"command": []any{"bash", "-lc", "pwd"}}, Timestamp: now},
			{Kind: session.CodexItemFunctionOut, CallID: "call_abc", Output: "ok", Timestamp: now},
			{Kind: session.CodexItemAssistantText, Role: "assistant", Text: "done", Timestamp: now},
		},
	}

	meta := session.CodexThreadMeta{CWD: "/repo", Title: "hello", CLIVersion: "test", FirstUserMessage: "hello"}
	threadID, rolloutPath, err := w.Write(context.Background(), s, meta)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if threadID == "" || rolloutPath == "" {
		t.Fatalf("missing output identifiers")
	}
	if _, err := os.Stat(rolloutPath); err != nil {
		t.Fatalf("rollout missing: %v", err)
	}

	// Validate rollout includes expected line types.
	f, err := os.Open(rolloutPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lineTypes := []string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("bad json line: %v", err)
		}
		typ, _ := m["type"].(string)
		lineTypes = append(lineTypes, typ)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lineTypes, ",")
	if !strings.Contains(joined, "session_meta") || !strings.Contains(joined, "response_item") || !strings.Contains(joined, "turn_context") || !strings.Contains(joined, "event_msg") {
		t.Fatalf("unexpected rollout line types: %v", lineTypes)
	}

	// Validate assistant messages are emitted with Codex-compatible final phase markers.
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	scanner = bufio.NewScanner(f)
	var sawAssistantResponse bool
	var sawAssistantEvent bool
	var sawTaskStarted bool
	var sawTaskComplete bool
	var turnIDFromStart string
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatalf("bad json line: %v", err)
		}
		typ, _ := m["type"].(string)
		payload, _ := m["payload"].(map[string]any)
		if typ == "response_item" && payload != nil {
			if role, _ := payload["role"].(string); role == "assistant" {
				if phase, _ := payload["phase"].(string); phase != "final_answer" {
					t.Fatalf("assistant response phase mismatch: %q", phase)
				}
				sawAssistantResponse = true
			}
		}
		if typ == "event_msg" && payload != nil {
			if pType, _ := payload["type"].(string); pType == "agent_message" {
				if phase, _ := payload["phase"].(string); phase != "final_answer" {
					t.Fatalf("assistant event phase mismatch: %q", phase)
				}
				sawAssistantEvent = true
			}
			if pType, _ := payload["type"].(string); pType == "task_started" {
				sawTaskStarted = true
				turnIDFromStart, _ = payload["turn_id"].(string)
			}
			if pType, _ := payload["type"].(string); pType == "task_complete" {
				sawTaskComplete = true
				if gotTurnID, _ := payload["turn_id"].(string); gotTurnID == "" {
					t.Fatalf("task_complete missing turn_id")
				}
			}
		}
		if typ == "turn_context" && payload != nil {
			if gotTurnID, _ := payload["turn_id"].(string); gotTurnID == "" {
				t.Fatalf("turn_context missing turn_id")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawAssistantResponse || !sawAssistantEvent {
		t.Fatalf("missing assistant artifacts: response=%v event=%v", sawAssistantResponse, sawAssistantEvent)
	}
	if !sawTaskStarted || !sawTaskComplete || turnIDFromStart == "" {
		t.Fatalf("missing task lifecycle events: started=%v complete=%v turn_id=%q", sawTaskStarted, sawTaskComplete, turnIDFromStart)
	}

	db, err := sql.Open("sqlite", stateDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var gotRollout string
	if err := db.QueryRow(`SELECT rollout_path FROM threads WHERE id = ?`, threadID).Scan(&gotRollout); err != nil {
		t.Fatalf("thread row missing: %v", err)
	}
	if gotRollout != rolloutPath {
		t.Fatalf("rollout path mismatch: got %s want %s", gotRollout, rolloutPath)
	}

	indexPath := filepath.Join(home, "session_index.jsonl")
	b, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), threadID) {
		t.Fatalf("session index missing thread id")
	}
}

func createThreadsSchema(t *testing.T, dbPath string) {
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
