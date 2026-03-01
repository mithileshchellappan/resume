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
		return x
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
