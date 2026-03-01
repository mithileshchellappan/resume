package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

var (
	ErrSessionNotFound  = errors.New("claude session not found")
	ErrSessionDuplicate = errors.New("claude session id matched multiple indexes")
)

type Loader struct {
	ClaudeHome string
}

func NewLoader(claudeHome string) *Loader {
	return &Loader{ClaudeHome: claudeHome}
}

type indexEntry struct {
	SessionID string `json:"sessionId"`
	FullPath  string `json:"fullPath"`
}

type indexFile struct {
	Entries []indexEntry `json:"entries"`
}

func (l *Loader) LoadBySessionID(ctx context.Context, id string) (session.SessionIR, error) {
	select {
	case <-ctx.Done():
		return session.SessionIR{}, ctx.Err()
	default:
	}

	sessionPath, err := l.findSessionPath(ctx, id)
	if err != nil {
		return session.SessionIR{}, err
	}
	return l.loadSessionFile(ctx, id, sessionPath)
}

func (l *Loader) ListSessions(ctx context.Context) ([]session.SourceSession, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	pattern := filepath.Join(l.ClaudeHome, "projects", "*", "sessions-index.json")
	indexPaths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob claude session indexes: %w", err)
	}
	sort.Strings(indexPaths)

	seenByPath := map[string]bool{}
	out := make([]session.SourceSession, 0)
	for _, idxPath := range indexPaths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		b, readErr := os.ReadFile(idxPath)
		if readErr != nil {
			continue
		}
		var idx indexFile
		if unmarshalErr := json.Unmarshal(b, &idx); unmarshalErr != nil {
			continue
		}
		for _, entry := range idx.Entries {
			id := strings.TrimSpace(entry.SessionID)
			if id == "" {
				continue
			}
			full := strings.TrimSpace(entry.FullPath)
			if full == "" {
				full = filepath.Join(filepath.Dir(idxPath), id+".jsonl")
			}
			full = filepath.Clean(full)
			if seenByPath[full] {
				continue
			}
			seenByPath[full] = true
			out = append(out, l.summarizeSessionFile(ctx, id, full))
		}
	}

	// Include sessions not yet present in sessions-index.json.
	fallbackPattern := filepath.Join(l.ClaudeHome, "projects", "*", "*.jsonl")
	fallbackPaths, ferr := filepath.Glob(fallbackPattern)
	if ferr == nil {
		sort.Strings(fallbackPaths)
		for _, full := range fallbackPaths {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			full = filepath.Clean(full)
			if seenByPath[full] {
				continue
			}
			seenByPath[full] = true
			id := strings.TrimSuffix(filepath.Base(full), filepath.Ext(full))
			if strings.TrimSpace(id) == "" {
				continue
			}
			out = append(out, l.summarizeSessionFile(ctx, id, full))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (l *Loader) findSessionPath(ctx context.Context, id string) (string, error) {
	pattern := filepath.Join(l.ClaudeHome, "projects", "*", "sessions-index.json")
	indexPaths, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob claude session indexes: %w", err)
	}
	sort.Strings(indexPaths)

	matches := make([]string, 0, 1)
	for _, idxPath := range indexPaths {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		b, err := os.ReadFile(idxPath)
		if err != nil {
			continue
		}
		var idx indexFile
		if err := json.Unmarshal(b, &idx); err != nil {
			continue
		}
		for _, entry := range idx.Entries {
			if entry.SessionID != id {
				continue
			}
			full := strings.TrimSpace(entry.FullPath)
			if full == "" {
				full = filepath.Join(filepath.Dir(idxPath), entry.SessionID+".jsonl")
			}
			matches = append(matches, full)
		}
	}

	if len(matches) == 0 {
		// Fallback: recently-created Claude sessions can exist as JSONL files
		// before sessions-index.json is created/updated.
		fallbackPattern := filepath.Join(l.ClaudeHome, "projects", "*", id+".jsonl")
		fallbackMatches, ferr := filepath.Glob(fallbackPattern)
		if ferr == nil {
			sort.Strings(fallbackMatches)
			matches = append(matches, fallbackMatches...)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%w: %s", ErrSessionDuplicate, id)
	}
	return matches[0], nil
}

type claudeLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	CWD       string          `json:"cwd"`
	SessionID string          `json:"sessionId"`
	Message   json.RawMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentItem struct {
	Kind         string
	Text         string
	Reasoning    string
	ToolUseID    string
	ToolName     string
	ToolInput    map[string]any
	ToolResultID string
	ToolOutput   string
}

func (l *Loader) loadSessionFile(ctx context.Context, requestedID, path string) (session.SessionIR, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.SessionIR{}, fmt.Errorf("open claude session file: %w", err)
	}
	defer f.Close()

	ir := session.SessionIR{SourceID: requestedID}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)

	callIndex := 0
	missingCallIDCounter := 0

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
		var line claudeLine
		if err := json.Unmarshal([]byte(lineText), &line); err != nil {
			continue
		}
		if line.Type != "user" && line.Type != "assistant" {
			continue
		}

		ts := parseTS(line.Timestamp)
		if ir.StartedAt.IsZero() && !ts.IsZero() {
			ir.StartedAt = ts
		}
		if ir.CWD == "" && strings.TrimSpace(line.CWD) != "" {
			ir.CWD = strings.TrimSpace(line.CWD)
		}
		if strings.TrimSpace(line.SessionID) != "" {
			ir.SourceID = strings.TrimSpace(line.SessionID)
		}
		if len(line.Message) == 0 {
			continue
		}

		var msg claudeMessage
		if err := json.Unmarshal(line.Message, &msg); err != nil {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role == "" {
			role = line.Type
		}

		items := parseContent(msg.Content)
		pendingReasoning := ""
		for _, item := range items {
			switch item.Kind {
			case "reasoning":
				if pendingReasoning == "" {
					pendingReasoning = item.Reasoning
				} else {
					pendingReasoning += "\n" + item.Reasoning
				}
			case "text":
				if role == "user" && isLocalCommandEnvelopeMessage(item.Text) {
					continue
				}
				m := session.Message{
					Role:      role,
					Content:   item.Text,
					Timestamp: ts,
					Reasoning: pendingReasoning,
				}
				ir.Messages = append(ir.Messages, m)
				kind := session.EventAssistantMessage
				if role == "user" {
					kind = session.EventUserMessage
				}
				msgCopy := m
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: kind, Msg: &msgCopy})
			case "tool_use":
				if role != "assistant" {
					continue
				}
				callID := strings.TrimSpace(item.ToolUseID)
				if callID == "" {
					missingCallIDCounter++
					callID = fmt.Sprintf("missing_tool_id_%d", missingCallIDCounter)
				}
				callIndex++
				c := session.ToolCall{
					SourceID:  callID,
					Name:      strings.TrimSpace(item.ToolName),
					Input:     item.ToolInput,
					Index:     callIndex,
					Timestamp: ts,
				}
				ir.Calls = append(ir.Calls, c)
				callCopy := c
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: session.EventToolCall, Call: &callCopy})
			case "tool_result":
				if role != "user" {
					continue
				}
				tr := session.ToolResult{
					CallSourceID: strings.TrimSpace(item.ToolResultID),
					Output:       item.ToolOutput,
					Timestamp:    ts,
				}
				ir.Results = append(ir.Results, tr)
				trCopy := tr
				ir.OrderedEvents = append(ir.OrderedEvents, session.Event{Kind: session.EventToolResult, Result: &trCopy})
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return session.SessionIR{}, fmt.Errorf("scan claude session file: %w", err)
	}
	if ir.StartedAt.IsZero() {
		ir.StartedAt = time.Now().UTC()
	}
	if ir.SourceID == "" {
		ir.SourceID = requestedID
	}
	return ir, nil
}

func (l *Loader) summarizeSessionFile(ctx context.Context, id, path string) session.SourceSession {
	summary := session.SourceSession{
		ID:    strings.TrimSpace(id),
		CWD:   ".",
		Title: strings.TrimSpace(id),
	}
	if stat, err := os.Stat(path); err == nil {
		summary.UpdatedAt = stat.ModTime().UTC()
	}

	f, err := os.Open(path)
	if err != nil {
		return summary
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 8*1024), 2*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return summary
		default:
		}
		lineText := strings.TrimSpace(scanner.Text())
		if lineText == "" {
			continue
		}
		var line claudeLine
		if err := json.Unmarshal([]byte(lineText), &line); err != nil {
			continue
		}
		if summary.CWD == "." && strings.TrimSpace(line.CWD) != "" {
			summary.CWD = strings.TrimSpace(line.CWD)
		}
		if line.Timestamp != "" && summary.UpdatedAt.IsZero() {
			if ts := parseTS(line.Timestamp); !ts.IsZero() {
				summary.UpdatedAt = ts.UTC()
			}
		}
		if len(line.Message) == 0 {
			continue
		}
		var msg claudeMessage
		if err := json.Unmarshal(line.Message, &msg); err != nil {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		if role == "" {
			role = line.Type
		}
		for _, item := range parseContent(msg.Content) {
			if item.Kind != "text" {
				continue
			}
			text := normalizeSessionTitle(item.Text)
			if text == "" {
				continue
			}
			if role != "user" {
				continue
			}
			if isLocalCommandEnvelopeMessage(text) {
				continue
			}
			summary.Title = text
			return summary
		}
	}
	return summary
}

func normalizeSessionTitle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 140 {
		return strings.TrimSpace(s[:140])
	}
	return s
}

func parseTS(s string) time.Time {
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

func parseContent(raw json.RawMessage) []contentItem {
	if len(raw) == 0 {
		return nil
	}

	// Bare string content.
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return []contentItem{{Kind: "text", Text: strings.TrimSpace(asString)}}
	}

	// Array content.
	var asArray []map[string]any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		out := make([]contentItem, 0, len(asArray))
		for _, item := range asArray {
			out = append(out, parseContentItem(item)...)
		}
		return out
	}

	// Object content.
	var asObj map[string]any
	if err := json.Unmarshal(raw, &asObj); err == nil {
		return parseContentItem(asObj)
	}

	return nil
}

func parseContentItem(item map[string]any) []contentItem {
	kind := strings.TrimSpace(asString(item["type"]))
	switch kind {
	case "text", "input_text", "output_text":
		return []contentItem{{Kind: "text", Text: strings.TrimSpace(asString(item["text"]))}}
	case "thinking":
		return []contentItem{{Kind: "reasoning", Reasoning: strings.TrimSpace(asString(item["thinking"]))}}
	case "tool_use":
		input := map[string]any{}
		if raw, ok := item["input"].(map[string]any); ok {
			input = raw
		}
		return []contentItem{{
			Kind:      "tool_use",
			ToolUseID: strings.TrimSpace(asString(item["id"])),
			ToolName:  strings.TrimSpace(asString(item["name"])),
			ToolInput: input,
		}}
	case "tool_result":
		return []contentItem{{
			Kind:         "tool_result",
			ToolResultID: strings.TrimSpace(asString(item["tool_use_id"])),
			ToolOutput:   parseToolResultContent(item["content"]),
		}}
	default:
		if t := strings.TrimSpace(asString(item["text"])); t != "" {
			return []contentItem{{Kind: "text", Text: t}}
		}
	}
	return nil
}

func parseToolResultContent(v any) string {
	switch x := v.(type) {
	case string:
		return normalizeToolResultString(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			switch p := e.(type) {
			case string:
				parts = append(parts, p)
			case map[string]any:
				if txt := strings.TrimSpace(asString(p["text"])); txt != "" {
					parts = append(parts, txt)
				} else {
					b, _ := json.Marshal(p)
					parts = append(parts, string(b))
				}
			default:
				b, _ := json.Marshal(p)
				parts = append(parts, string(b))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		if txt := strings.TrimSpace(asString(x["text"])); txt != "" {
			return txt
		}
		b, _ := json.Marshal(x)
		return string(b)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func normalizeToolResultString(s string) string {
	s = strings.TrimSpace(s)
	const open = "<tool_use_error>"
	const close = "</tool_use_error>"
	if strings.HasPrefix(s, open) && strings.HasSuffix(s, close) {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, open), close))
	}
	return s
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func isLocalCommandEnvelopeMessage(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	prefixes := []string{
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<local-command-stderr>",
		"<local-command-status>",
		"<command-name>",
		"<command-message>",
		"<command-args>",
		"<command-result>",
		"<command-exit-code>",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}
