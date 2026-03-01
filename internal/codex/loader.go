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

	"github.com/mithileshchellappan/resume/internal/session"
	_ "modernc.org/sqlite"
)

var ErrThreadNotFound = errors.New("codex thread not found")

type Loader struct {
	CodexHome string
}

func NewLoader(codexHome string) *Loader {
	return &Loader{CodexHome: codexHome}
}

func (l *Loader) LoadByThreadID(ctx context.Context, id string) (session.SessionIR, error) {
	select {
	case <-ctx.Done():
		return session.SessionIR{}, ctx.Err()
	default:
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return session.SessionIR{}, fmt.Errorf("%w: empty id", ErrThreadNotFound)
	}

	rolloutPath, cwd, createdAt, err := l.findThread(ctx, id)
	if err != nil {
		return session.SessionIR{}, err
	}
	return l.loadRollout(ctx, id, rolloutPath, cwd, createdAt)
}

func (l *Loader) ListSessions(ctx context.Context) ([]session.SourceSession, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	glob := filepath.Join(l.CodexHome, "state_*.sqlite")
	paths, err := filepath.Glob(glob)
	if err != nil {
		return nil, fmt.Errorf("glob codex state dbs: %w", err)
	}
	if len(paths) == 0 {
		return nil, nil
	}

	sort.Slice(paths, func(i, j int) bool {
		si, ei := os.Stat(paths[i])
		sj, ej := os.Stat(paths[j])
		if ei != nil || ej != nil {
			return paths[i] > paths[j]
		}
		return si.ModTime().After(sj.ModTime())
	})

	seen := map[string]bool{}
	out := make([]session.SourceSession, 0)
	for _, dbPath := range paths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		db, openErr := sql.Open("sqlite", dbPath)
		if openErr != nil {
			continue
		}

		rows, queryErr := db.QueryContext(ctx, `SELECT id, rollout_path, cwd, title, git_branch, updated_at FROM threads ORDER BY updated_at DESC`)
		if queryErr != nil {
			_ = db.Close()
			continue
		}
		for rows.Next() {
			var id, rolloutPath, cwd, title string
			var gitBranch sql.NullString
			var updatedAt int64
			if scanErr := rows.Scan(&id, &rolloutPath, &cwd, &title, &gitBranch, &updatedAt); scanErr != nil {
				continue
			}
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			rolloutPath = strings.TrimSpace(rolloutPath)
			if rolloutPath == "" {
				continue
			}
			seen[id] = true
			cwd = strings.TrimSpace(cwd)
			if cwd == "" {
				cwd = "."
			}
			title = strings.TrimSpace(title)
			if title == "" {
				title = id
			}
			stat, statErr := os.Stat(rolloutPath)
			if statErr != nil {
				continue
			}
			item := session.SourceSession{
				ID:        id,
				CWD:       cwd,
				Title:     title,
				GitBranch: strings.TrimSpace(gitBranch.String),
				SizeBytes: stat.Size(),
			}
			if updatedAt > 0 {
				item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
			}
			out = append(out, item)
		}
		_ = rows.Close()
		_ = db.Close()
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (l *Loader) findThread(ctx context.Context, id string) (rolloutPath, cwd string, createdAt int64, err error) {
	glob := filepath.Join(l.CodexHome, "state_*.sqlite")
	paths, err := filepath.Glob(glob)
	if err != nil {
		return "", "", 0, fmt.Errorf("glob codex state dbs: %w", err)
	}
	if len(paths) == 0 {
		return "", "", 0, fmt.Errorf("%w: %s", ErrThreadNotFound, id)
	}

	sort.Slice(paths, func(i, j int) bool {
		si, ei := os.Stat(paths[i])
		sj, ej := os.Stat(paths[j])
		if ei != nil || ej != nil {
			return paths[i] > paths[j]
		}
		return si.ModTime().After(sj.ModTime())
	})

	for _, dbPath := range paths {
		select {
		case <-ctx.Done():
			return "", "", 0, ctx.Err()
		default:
		}

		db, openErr := sql.Open("sqlite", dbPath)
		if openErr != nil {
			continue
		}

		row := db.QueryRowContext(ctx, `SELECT rollout_path, cwd, created_at FROM threads WHERE id = ?`, id)
		var rp, c string
		var ts int64
		scanErr := row.Scan(&rp, &c, &ts)
		_ = db.Close()
		if scanErr == nil {
			return strings.TrimSpace(rp), strings.TrimSpace(c), ts, nil
		}
		if errors.Is(scanErr, sql.ErrNoRows) {
			continue
		}
	}

	return "", "", 0, fmt.Errorf("%w: %s", ErrThreadNotFound, id)
}

type rolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type responseItemPayload struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Name      string          `json:"name"`
	Arguments any             `json:"arguments"`
	Input     any             `json:"input"`
	CallID    string          `json:"call_id"`
	Output    any             `json:"output"`
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (l *Loader) loadRollout(ctx context.Context, threadID, rolloutPath, fallbackCWD string, createdAt int64) (session.SessionIR, error) {
	f, err := os.Open(rolloutPath)
	if err != nil {
		return session.SessionIR{}, fmt.Errorf("open codex rollout: %w", err)
	}
	defer f.Close()

	ir := session.SessionIR{
		SourceID: threadID,
		CWD:      strings.TrimSpace(fallbackCWD),
	}
	if createdAt > 0 {
		ir.StartedAt = time.Unix(createdAt, 0).UTC()
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	callIndex := 0
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return session.SessionIR{}, ctx.Err()
		default:
		}

		lineText := strings.TrimSpace(scanner.Text())
		if lineText == "" {
			continue
		}

		var line rolloutLine
		if err := json.Unmarshal([]byte(lineText), &line); err != nil {
			continue
		}
		ts := parseCodexTS(line.Timestamp)

		switch line.Type {
		case "session_meta":
			var meta struct {
				CWD       string `json:"cwd"`
				Timestamp string `json:"timestamp"`
			}
			if err := json.Unmarshal(line.Payload, &meta); err == nil {
				if ir.CWD == "" && strings.TrimSpace(meta.CWD) != "" {
					ir.CWD = strings.TrimSpace(meta.CWD)
				}
				if ir.StartedAt.IsZero() {
					if t := parseCodexTS(meta.Timestamp); !t.IsZero() {
						ir.StartedAt = t
					}
				}
			}

		case "turn_context":
			var tc struct {
				CWD string `json:"cwd"`
			}
			if err := json.Unmarshal(line.Payload, &tc); err == nil && ir.CWD == "" {
				ir.CWD = strings.TrimSpace(tc.CWD)
			}

		case "response_item":
			var p responseItemPayload
			if err := json.Unmarshal(line.Payload, &p); err != nil {
				continue
			}
			switch p.Type {
			case "message":
				role := strings.TrimSpace(strings.ToLower(p.Role))
				if !isConversationRole(role) {
					continue
				}
				parts := parseContentParts(p.Content)
				for _, part := range parts {
					text := strings.TrimSpace(part.Text)
					if text == "" {
						continue
					}
					if shouldDropCodexMessage(role, text) {
						continue
					}
					msg := session.Message{
						Role:      role,
						Content:   text,
						Timestamp: chooseCodexTS(ts, ir.StartedAt),
					}
					ir.Messages = append(ir.Messages, msg)
					msgCopy := msg
					kind := session.EventAssistantMessage
					if role == "user" {
						kind = session.EventUserMessage
					}
					ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: kind, Msg: &msgCopy})
				}

			case "function_call":
				callID := strings.TrimSpace(p.CallID)
				if callID == "" {
					callIndex++
					callID = fmt.Sprintf("call_missing_%d", callIndex)
				}
				args := decodeArguments(p.Arguments)
				callIndex++
				call := session.ToolCall{
					SourceID:  callID,
					Name:      strings.TrimSpace(p.Name),
					Input:     args,
					Index:     callIndex,
					Timestamp: chooseCodexTS(ts, ir.StartedAt),
				}
				ir.Calls = append(ir.Calls, call)
				callCopy := call
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: session.EventToolCall, Call: &callCopy})

			case "function_call_output":
				result := session.ToolResult{
					CallSourceID: strings.TrimSpace(p.CallID),
					Output:       stringifyOutput(p.Output),
					Timestamp:    chooseCodexTS(ts, ir.StartedAt),
				}
				ir.Results = append(ir.Results, result)
				resultCopy := result
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: session.EventToolResult, Result: &resultCopy})

			case "custom_tool_call":
				callID := strings.TrimSpace(p.CallID)
				if callID == "" {
					callIndex++
					callID = fmt.Sprintf("call_missing_%d", callIndex)
				}
				args := decodeCustomInput(p.Input)
				callIndex++
				call := session.ToolCall{
					SourceID:  callID,
					Name:      strings.TrimSpace(p.Name),
					Input:     args,
					Index:     callIndex,
					Timestamp: chooseCodexTS(ts, ir.StartedAt),
				}
				ir.Calls = append(ir.Calls, call)
				callCopy := call
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: session.EventToolCall, Call: &callCopy})

			case "custom_tool_call_output":
				result := session.ToolResult{
					CallSourceID: strings.TrimSpace(p.CallID),
					Output:       stringifyOutput(p.Output),
					Timestamp:    chooseCodexTS(ts, ir.StartedAt),
				}
				ir.Results = append(ir.Results, result)
				resultCopy := result
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: session.EventToolResult, Result: &resultCopy})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return session.SessionIR{}, fmt.Errorf("scan codex rollout: %w", err)
	}

	if ir.StartedAt.IsZero() {
		ir.StartedAt = time.Now().UTC()
	}
	if ir.CWD == "" {
		ir.CWD = "."
	}

	return ir, nil
}

func parseContentParts(raw json.RawMessage) []contentPart {
	if len(raw) == 0 {
		return nil
	}
	var arr []contentPart
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var single contentPart
	if err := json.Unmarshal(raw, &single); err == nil {
		return []contentPart{single}
	}
	return nil
}

func decodeCustomInput(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return map[string]any{}
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(x), &m); err == nil && m != nil {
			return m
		}
		return map[string]any{"content": x}
	case map[string]any:
		return x
	default:
		b, _ := json.Marshal(x)
		return map[string]any{"content": string(b)}
	}
}

func decodeArguments(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	switch x := v.(type) {
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return map[string]any{}
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(x), &m); err == nil && m != nil {
			return m
		}
		return map[string]any{"raw": x}
	case map[string]any:
		return x
	default:
		b, _ := json.Marshal(x)
		return map[string]any{"raw": string(b)}
	}
}

func stringifyOutput(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	default:
		b, _ := json.Marshal(v)
		return strings.TrimSpace(string(b))
	}
}

func parseCodexTS(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func chooseCodexTS(ts, fallback time.Time) time.Time {
	if ts.IsZero() {
		return fallback
	}
	return ts.UTC()
}

func isConversationRole(role string) bool {
	return role == "user" || role == "assistant"
}

func shouldDropCodexMessage(role, text string) bool {
	normalized := strings.TrimSpace(strings.ToLower(text))
	if normalized == "" {
		return true
	}

	// Codex-local envelopes and instruction injections are not user conversation content.
	prefixes := []string{
		"<permissions instructions>",
		"<collaboration_mode>",
		"<environment_context>",
		"<subagent_notification>",
		"<local-command-caveat>",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(normalized, p) {
			return true
		}
	}

	if strings.HasPrefix(normalized, "# agents.md instructions for ") {
		return true
	}
	if strings.Contains(normalized, "agents.md instructions for ") {
		return true
	}
	if strings.Contains(normalized, "<local-command-stdout>") ||
		strings.Contains(normalized, "<local-command-stderr>") ||
		strings.Contains(normalized, "<command-name>") ||
		strings.Contains(normalized, "<command-message>") ||
		strings.Contains(normalized, "<command-args>") {
		return true
	}

	return false
}
