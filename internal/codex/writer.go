package codex

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mithileshchellappan/resume/internal/session"
	_ "modernc.org/sqlite"
)

var ErrWrite = errors.New("codex native write failed")

const defaultSandboxPolicy = `{"type":"workspace-write","network_access":false,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":false}`

type Writer struct {
	CodexHome string
	Now       func() time.Time
}

func NewWriter(codexHome string) *Writer {
	return &Writer{
		CodexHome: codexHome,
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (w *Writer) Write(ctx context.Context, s session.CodexSession, meta session.CodexThreadMeta) (string, string, error) {
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	default:
	}

	if w.Now == nil {
		w.Now = func() time.Time { return time.Now().UTC() }
	}

	now := w.Now().UTC()
	threadID := uuid.NewString()
	if s.StartedAt.IsZero() {
		s.StartedAt = now
	}

	meta = normalizeMeta(meta, s)
	rolloutPath := rolloutPathFor(w.CodexHome, now, threadID)
	if err := writeRolloutFile(rolloutPath, threadID, s, meta); err != nil {
		return "", "", fmt.Errorf("%w: write rollout: %v", ErrWrite, err)
	}

	dbPath, err := findLatestStateDB(w.CodexHome)
	if err != nil {
		_ = os.Remove(rolloutPath)
		return "", "", fmt.Errorf("%w: resolve state db: %v", ErrWrite, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		_ = os.Remove(rolloutPath)
		return "", "", fmt.Errorf("%w: open state db: %v", ErrWrite, err)
	}
	defer db.Close()

	createdAt := now.Unix()
	if err := insertThread(ctx, db, threadID, rolloutPath, createdAt, meta); err != nil {
		_ = os.Remove(rolloutPath)
		return "", "", fmt.Errorf("%w: insert thread: %v", ErrWrite, err)
	}

	if err := appendSessionIndex(w.CodexHome, threadID, meta.Title, now); err != nil {
		_ = removeThreadByID(ctx, db, threadID)
		_ = os.Remove(rolloutPath)
		return "", "", fmt.Errorf("%w: append session index: %v", ErrWrite, err)
	}

	return threadID, rolloutPath, nil
}

func normalizeMeta(meta session.CodexThreadMeta, s session.CodexSession) session.CodexThreadMeta {
	meta.CWD = strings.TrimSpace(meta.CWD)
	if meta.CWD == "" {
		meta.CWD = strings.TrimSpace(s.CWD)
	}
	if meta.CWD == "" {
		meta.CWD = "."
	}
	meta.CLIVersion = strings.TrimSpace(meta.CLIVersion)
	if meta.CLIVersion == "" {
		meta.CLIVersion = "0.1.0-dev"
	}
	meta.ApprovalMode = strings.TrimSpace(meta.ApprovalMode)
	if meta.ApprovalMode == "" {
		meta.ApprovalMode = "on-request"
	}
	meta.SandboxPolicyJSON = strings.TrimSpace(meta.SandboxPolicyJSON)
	if meta.SandboxPolicyJSON == "" {
		meta.SandboxPolicyJSON = defaultSandboxPolicy
	}
	meta.FirstUserMessage = strings.TrimSpace(meta.FirstUserMessage)
	if meta.FirstUserMessage == "" {
		meta.FirstUserMessage = strings.TrimSpace(s.FirstUserMessage)
	}
	meta.Title = strings.TrimSpace(meta.Title)
	if meta.Title == "" {
		meta.Title = truncate(meta.FirstUserMessage, 180)
	}
	if meta.Title == "" {
		meta.Title = "Imported session"
	}
	return meta
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max])
}

func rolloutPathFor(codexHome string, ts time.Time, threadID string) string {
	dir := filepath.Join(codexHome, "sessions", ts.Format("2006"), ts.Format("01"), ts.Format("02"))
	name := fmt.Sprintf("rollout-%s-%s.jsonl", ts.Format("2006-01-02T15-04-05"), threadID)
	return filepath.Join(dir, name)
}

func writeRolloutFile(path, threadID string, s session.CodexSession, meta session.CodexThreadMeta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	bw := bufio.NewWriter(f)
	writeLine := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := bw.Write(append(b, '\n')); err != nil {
			return err
		}
		return nil
	}

	metaLine := map[string]any{
		"timestamp": s.StartedAt.UTC().Format(time.RFC3339Nano),
		"type":      "session_meta",
		"payload": map[string]any{
			"id":           threadID,
			"timestamp":    s.StartedAt.UTC().Format(time.RFC3339Nano),
			"cwd":          meta.CWD,
			"originator":   "resume_cli",
			"cli_version":  meta.CLIVersion,
			"instructions": "",
		},
	}
	if err := writeLine(metaLine); err != nil {
		return err
	}

	for _, item := range s.Items {
		ts := item.Timestamp.UTC().Format(time.RFC3339Nano)
		if item.Timestamp.IsZero() {
			ts = s.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		line := map[string]any{
			"timestamp": ts,
			"type":      "response_item",
			"payload":   payloadFromItem(item),
		}
		if err := writeLine(line); err != nil {
			return err
		}
		if eventPayload, ok := eventMsgPayloadFromItem(item); ok {
			eventLine := map[string]any{
				"timestamp": ts,
				"type":      "event_msg",
				"payload":   eventPayload,
			}
			if err := writeLine(eventLine); err != nil {
				return err
			}
		}
	}

	turnContext := map[string]any{
		"timestamp": s.StartedAt.UTC().Format(time.RFC3339Nano),
		"type":      "turn_context",
		"payload": map[string]any{
			"cwd":             meta.CWD,
			"approval_policy": meta.ApprovalMode,
			"sandbox_policy": map[string]any{
				"mode":                   "workspace-write",
				"writable_roots":         []string{meta.CWD},
				"network_access":         false,
				"exclude_tmpdir_env_var": false,
				"exclude_slash_tmp":      false,
			},
			"model":   "gpt-5",
			"effort":  "high",
			"summary": "auto",
		},
	}
	if err := writeLine(turnContext); err != nil {
		return err
	}

	if err := bw.Flush(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

func payloadFromItem(item session.CodexItem) map[string]any {
	switch item.Kind {
	case session.CodexItemUserMessage:
		return map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": item.Text,
			}},
		}
	case session.CodexItemAssistantText:
		return map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": item.Text,
			}},
		}
	case session.CodexItemFunctionCall:
		argJSON, _ := json.Marshal(item.Arguments)
		return map[string]any{
			"type":      "function_call",
			"name":      item.Name,
			"arguments": string(argJSON),
			"call_id":   item.CallID,
		}
	case session.CodexItemFunctionOut:
		return map[string]any{
			"type":    "function_call_output",
			"call_id": item.CallID,
			"output":  item.Output,
		}
	default:
		return map[string]any{}
	}
}

func eventMsgPayloadFromItem(item session.CodexItem) (map[string]any, bool) {
	switch item.Kind {
	case session.CodexItemUserMessage:
		return map[string]any{
			"type":    "user_message",
			"message": item.Text,
			"images":  []any{},
		}, true
	case session.CodexItemAssistantText:
		return map[string]any{
			"type":    "agent_message",
			"message": item.Text,
			"phase":   "final",
		}, true
	default:
		return nil, false
	}
}

func findLatestStateDB(codexHome string) (string, error) {
	glob := filepath.Join(codexHome, "state_*.sqlite")
	paths, err := filepath.Glob(glob)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", errors.New("no state_*.sqlite found")
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	cand := make([]candidate, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		cand = append(cand, candidate{path: p, mod: st.ModTime()})
	}
	if len(cand) == 0 {
		return "", errors.New("no readable state sqlite files found")
	}
	sort.Slice(cand, func(i, j int) bool {
		return cand[i].mod.After(cand[j].mod)
	})
	return cand[0].path, nil
}

func insertThread(ctx context.Context, db *sql.DB, threadID, rolloutPath string, nowUnix int64, meta session.CodexThreadMeta) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	query := `INSERT INTO threads (
		id, rollout_path, created_at, updated_at, source, model_provider, cwd, title,
		sandbox_policy, approval_mode, tokens_used, has_user_event, archived,
		archived_at, git_sha, git_branch, git_origin_url, cli_version, first_user_message
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	hasUserEvent := 0
	if strings.TrimSpace(meta.FirstUserMessage) != "" {
		hasUserEvent = 1
	}

	_, err = tx.ExecContext(ctx, query,
		threadID,
		rolloutPath,
		nowUnix,
		nowUnix,
		"cli",
		"openai",
		meta.CWD,
		meta.Title,
		meta.SandboxPolicyJSON,
		meta.ApprovalMode,
		0,
		hasUserEvent,
		0,
		nil,
		nil,
		nil,
		nil,
		meta.CLIVersion,
		meta.FirstUserMessage,
	)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func appendSessionIndex(codexHome, threadID, title string, now time.Time) error {
	path := filepath.Join(codexHome, "session_index.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := map[string]any{
		"id":          threadID,
		"thread_name": title,
		"updated_at":  now.UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func removeThreadByID(ctx context.Context, db *sql.DB, threadID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM threads WHERE id = ?`, threadID)
	return err
}
