package converter

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

var ErrConvert = errors.New("conversion failed")

const orphanPrefix = "[orphan tool result] "

// IDGenerator generates target tool call IDs.
type IDGenerator interface {
	NewCallID() (string, error)
}

type randomIDGenerator struct{}

func (g randomIDGenerator) NewCallID() (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const n = 24
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random id bytes: %w", err)
	}
	out := make([]byte, n)
	for i := range buf {
		out[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return "call_" + string(out), nil
}

// Converter implements IR -> Codex target conversion.
type Converter struct {
	IDGen IDGenerator
	Now   func() time.Time
}

func New() *Converter {
	return &Converter{
		IDGen: randomIDGenerator{},
		Now:   func() time.Time { return time.Now().UTC() },
	}
}

func (c *Converter) Convert(ctx context.Context, in session.SessionIR) (session.CodexSession, error) {
	select {
	case <-ctx.Done():
		return session.CodexSession{}, ctx.Err()
	default:
	}

	if c.IDGen == nil {
		c.IDGen = randomIDGenerator{}
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}

	out := session.CodexSession{
		SourceSessionID: in.SourceID,
		CWD:             in.CWD,
		StartedAt:       in.StartedAt,
	}
	if out.StartedAt.IsZero() {
		out.StartedAt = c.Now()
	}

	callIDMap := make(map[string]string)
	callTSMap := make(map[string]time.Time)
	callOrder := make([]string, 0, len(in.Calls))
	seenResult := make(map[string]int)

	for _, ev := range in.OrderedEvents {
		select {
		case <-ctx.Done():
			return session.CodexSession{}, ctx.Err()
		default:
		}

		switch ev.Kind {
		case session.EventUserMessage, session.EventAssistantMessage:
			if ev.Msg == nil {
				continue
			}
			text := strings.TrimSpace(ev.Msg.Content)
			if text == "" {
				continue
			}
			item := session.CodexItem{
				Timestamp: chooseTS(ev.Msg.Timestamp, out.StartedAt),
				Text:      text,
			}
			if ev.Kind == session.EventUserMessage {
				item.Kind = session.CodexItemUserMessage
				item.Role = "user"
				out.HasUserEvent = true
				if out.FirstUserMessage == "" {
					out.FirstUserMessage = text
				}
			} else {
				item.Kind = session.CodexItemAssistantText
				item.Role = "assistant"
			}
			out.Items = append(out.Items, item)

		case session.EventToolCall:
			if ev.Call == nil {
				continue
			}
			sourceID := strings.TrimSpace(ev.Call.SourceID)
			if sourceID == "" {
				sourceID = fmt.Sprintf("missing_tool_id_%d", ev.Call.Index)
			}
			targetID := strings.TrimSpace(ev.Call.TargetID)
			if targetID == "" {
				var err error
				targetID, err = c.IDGen.NewCallID()
				if err != nil {
					return session.CodexSession{}, fmt.Errorf("%w: generate call id: %v", ErrConvert, err)
				}
			}
			if _, exists := callIDMap[sourceID]; !exists {
				callIDMap[sourceID] = targetID
				callTSMap[sourceID] = chooseTS(ev.Call.Timestamp, out.StartedAt)
				callOrder = append(callOrder, sourceID)
			}

			name, args := normalizeToolCall(ev.Call.Name, ev.Call.Input)
			out.Items = append(out.Items, session.CodexItem{
				Kind:      session.CodexItemFunctionCall,
				CallID:    targetID,
				Name:      name,
				Arguments: args,
				Timestamp: chooseTS(ev.Call.Timestamp, out.StartedAt),
			})

		case session.EventToolResult:
			if ev.Result == nil {
				continue
			}
			sourceID := strings.TrimSpace(ev.Result.CallSourceID)
			targetID, ok := callIDMap[sourceID]
			if !ok || sourceID == "" {
				text := strings.TrimSpace(orphanPrefix + strings.TrimSpace(ev.Result.Output))
				if text == orphanPrefix {
					text += "[empty]"
				}
				out.Items = append(out.Items, session.CodexItem{
					Kind:      session.CodexItemAssistantText,
					Role:      "assistant",
					Text:      text,
					Timestamp: chooseTS(ev.Result.Timestamp, out.StartedAt),
				})
				continue
			}
			if seenResult[sourceID] > 0 {
				text := strings.TrimSpace(orphanPrefix + strings.TrimSpace(ev.Result.Output))
				out.Items = append(out.Items, session.CodexItem{
					Kind:      session.CodexItemAssistantText,
					Role:      "assistant",
					Text:      text,
					Timestamp: chooseTS(ev.Result.Timestamp, out.StartedAt),
				})
				continue
			}
			seenResult[sourceID]++
			out.Items = append(out.Items, session.CodexItem{
				Kind:      session.CodexItemFunctionOut,
				CallID:    targetID,
				Output:    strings.TrimSpace(ev.Result.Output),
				Timestamp: chooseTS(ev.Result.Timestamp, out.StartedAt),
			})
		}
	}

	for _, sourceID := range callOrder {
		if seenResult[sourceID] > 0 {
			continue
		}
		out.Items = append(out.Items, session.CodexItem{
			Kind:      session.CodexItemFunctionOut,
			CallID:    callIDMap[sourceID],
			Output:    "[no output recorded]",
			Timestamp: chooseTS(callTSMap[sourceID], out.StartedAt),
		})
	}

	if out.FirstUserMessage == "" {
		for _, it := range out.Items {
			if it.Kind == session.CodexItemUserMessage {
				out.FirstUserMessage = it.Text
				break
			}
		}
	}

	return out, nil
}

func chooseTS(ts, fallback time.Time) time.Time {
	if ts.IsZero() {
		return fallback
	}
	return ts.UTC()
}

func normalizeToolCall(name string, input map[string]any) (string, map[string]any) {
	if input == nil {
		input = map[string]any{}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash":
		cmdRaw, has := input["command"]
		if !has {
			return "shell", input
		}
		switch cmd := cmdRaw.(type) {
		case string:
			out := map[string]any{"command": []any{"bash", "-lc", cmd}}
			if desc := strings.TrimSpace(asString(input["description"])); desc != "" {
				out["description"] = desc
			}
			return "shell", out
		case []any:
			return "shell", map[string]any{"command": cmd}
		default:
			b, _ := json.Marshal(cmdRaw)
			return "shell", map[string]any{"command": []any{"bash", "-lc", string(b)}}
		}
	case "glob":
		out := map[string]any{}
		if pattern := strings.TrimSpace(asString(input["pattern"])); pattern != "" {
			out["pattern"] = pattern
		}
		if path := strings.TrimSpace(asString(input["path"])); path != "" {
			out["path"] = path
		}
		if len(out) == 0 {
			return "glob", input
		}
		return "glob", out
	case "read":
		out := map[string]any{}
		if path := strings.TrimSpace(asString(input["file_path"])); path != "" {
			out["path"] = path
		} else if path := strings.TrimSpace(asString(input["path"])); path != "" {
			out["path"] = path
		}
		if offset, ok := input["offset"]; ok {
			out["offset"] = offset
		}
		if limit, ok := input["limit"]; ok {
			out["limit"] = limit
		}
		if len(out) == 0 {
			return "read_file", input
		}
		return "read_file", out
	}
	return strings.TrimSpace(name), input
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
