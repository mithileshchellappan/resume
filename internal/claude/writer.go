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

	"github.com/google/uuid"
	"github.com/mithileshchellappan/resume/internal/session"
)

var ErrWrite = errors.New("claude native write failed")

const defaultClaudeVersion = "2.1.63"

type Writer struct {
	ClaudeHome string
	Now        func() time.Time
}

func NewWriter(claudeHome string) *Writer {
	return &Writer{
		ClaudeHome: claudeHome,
		Now: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (w *Writer) Write(ctx context.Context, in session.SessionIR, meta session.ClaudeSessionMeta) (string, string, error) {
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	default:
	}

	if w.Now == nil {
		w.Now = func() time.Time { return time.Now().UTC() }
	}

	startedAt := in.StartedAt.UTC()
	if startedAt.IsZero() {
		startedAt = w.Now().UTC()
	}

	cwd := choose(meta.CWD, in.CWD, ".")
	gitBranch := choose(meta.GitBranch, "main")

	sessionID := uuid.NewString()
	projectKey := projectKeyFromCWD(cwd)
	projectDir := filepath.Join(w.ClaudeHome, "projects", projectKey)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return "", "", fmt.Errorf("%w: create project dir: %v", ErrWrite, err)
	}

	sessionPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := writeSessionJSONL(sessionPath, sessionID, cwd, gitBranch, startedAt, in.OrderedEvents); err != nil {
		return "", "", fmt.Errorf("%w: write session file: %v", ErrWrite, err)
	}
	if err := upsertSessionsIndex(filepath.Join(projectDir, "sessions-index.json"), sessionID, sessionPath); err != nil {
		_ = os.Remove(sessionPath)
		return "", "", fmt.Errorf("%w: update sessions-index: %v", ErrWrite, err)
	}

	return sessionID, sessionPath, nil
}

func writeSessionJSONL(path, sessionID, cwd, gitBranch string, startedAt time.Time, events []session.Event) error {
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

	callIDMap := map[string]string{}
	droppedCalls := map[string]bool{} // source IDs of lifecycle tool calls to drop
	prevUUID := ""
	eventTS := startedAt
	callCount := 0

	for _, ev := range events {
		if !eventTS.IsZero() {
			eventTS = eventTS.Add(time.Millisecond)
		}
		ts := chooseTS(ev, eventTS, startedAt)
		lineUUID := uuid.NewString()
		parent := any(nil)
		if prevUUID != "" {
			parent = prevUUID
		}

		base := map[string]any{
			"parentUuid":  parent,
			"isSidechain": false,
			"userType":    "external",
			"cwd":         cwd,
			"sessionId":   sessionID,
			"version":     defaultClaudeVersion,
			"gitBranch":   gitBranch,
			"uuid":        lineUUID,
			"timestamp":   ts.Format(time.RFC3339Nano),
		}

		switch ev.Kind {
		case session.EventUserMessage:
			if ev.Msg == nil || strings.TrimSpace(ev.Msg.Content) == "" {
				continue
			}
			line := cloneMap(base)
			line["type"] = "user"
			line["message"] = map[string]any{
				"role":    "user",
				"content": strings.TrimSpace(ev.Msg.Content),
			}
			line["permissionMode"] = "bypassPermissions"
			if err := writeLine(line); err != nil {
				return err
			}
			prevUUID = lineUUID

		case session.EventAssistantMessage:
			if ev.Msg == nil || strings.TrimSpace(ev.Msg.Content) == "" {
				continue
			}
			line := cloneMap(base)
			line["type"] = "assistant"
			line["message"] = map[string]any{
				"model": "claude-opus-4-6",
				"id":    "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				"type":  "message",
				"role":  "assistant",
				"content": []map[string]any{
					{"type": "text", "text": strings.TrimSpace(ev.Msg.Content)},
				},
				"stop_reason":   nil,
				"stop_sequence": nil,
			}
			line["requestId"] = "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if err := writeLine(line); err != nil {
				return err
			}
			prevUUID = lineUUID

		case session.EventToolCall:
			if ev.Call == nil {
				continue
			}
			if isCodexLifecycleTool(ev.Call.Name) {
				sourceID := strings.TrimSpace(ev.Call.SourceID)
				if sourceID != "" {
					droppedCalls[sourceID] = true
				}
				continue
			}
			sourceID := strings.TrimSpace(ev.Call.SourceID)
			if sourceID == "" {
				callCount++
				sourceID = fmt.Sprintf("call_missing_%d", callCount)
			}
			claudeToolID := "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			callIDMap[sourceID] = claudeToolID

			toolName, toolInput := normalizeCodexToolForClaude(ev.Call.Name, ev.Call.Input)
			line := cloneMap(base)
			line["type"] = "assistant"
			line["message"] = map[string]any{
				"model": "claude-opus-4-6",
				"id":    "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				"type":  "message",
				"role":  "assistant",
				"content": []map[string]any{
					{
						"type":   "tool_use",
						"id":     claudeToolID,
						"name":   toolName,
						"input":  toolInput,
						"caller": map[string]any{"type": "direct"},
					},
				},
				"stop_reason":   "tool_use",
				"stop_sequence": nil,
			}
			line["requestId"] = "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			if err := writeLine(line); err != nil {
				return err
			}
			prevUUID = lineUUID

		case session.EventToolResult:
			if ev.Result == nil {
				continue
			}
			callSourceID := strings.TrimSpace(ev.Result.CallSourceID)
			if droppedCalls[callSourceID] {
				continue
			}
			toolUseID := callIDMap[callSourceID]
			if toolUseID == "" {
				toolUseID = "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			}
			output := strings.TrimSpace(ev.Result.Output)
			if output == "" {
				output = "[no output recorded]"
			}

			line := cloneMap(base)
			line["type"] = "user"
			line["message"] = map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": toolUseID,
						"content":     output,
					},
				},
			}
			line["toolUseResult"] = output
			if err := writeLine(line); err != nil {
				return err
			}
			prevUUID = lineUUID
		}
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

func chooseTS(ev session.Event, fallback, startedAt time.Time) time.Time {
	var ts time.Time
	switch ev.Kind {
	case session.EventUserMessage, session.EventAssistantMessage:
		if ev.Msg != nil {
			ts = ev.Msg.Timestamp
		}
	case session.EventToolCall:
		if ev.Call != nil {
			ts = ev.Call.Timestamp
		}
	case session.EventToolResult:
		if ev.Result != nil {
			ts = ev.Result.Timestamp
		}
	}
	if !ts.IsZero() {
		return ts.UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return startedAt.UTC()
}

func normalizeCodexToolForClaude(name string, input map[string]any) (string, map[string]any) {
	name = strings.TrimSpace(name)
	if input == nil {
		input = map[string]any{}
	}
	switch strings.ToLower(name) {
	case "shell", "shell_command":
		cmd := strings.TrimSpace(asStringValue(input["command"]))
		if cmd == "" {
			if arr, ok := input["command"].([]any); ok && len(arr) >= 3 {
				cmd = strings.TrimSpace(asStringValue(arr[2]))
			}
		}
		out := map[string]any{"command": cmd}
		if desc := strings.TrimSpace(asStringValue(input["description"])); desc != "" {
			out["description"] = desc
		}
		return "Bash", out

	case "apply_patch":
		patch := strings.TrimSpace(asStringValue(input["content"]))
		if patch == "" {
			patch = strings.TrimSpace(asStringValue(input["raw"]))
		}
		filePath := extractPatchFilePath(patch)
		return "Edit", map[string]any{
			"file_path":  filePath,
			"new_string": patch,
		}

	case "view_image":
		path := strings.TrimSpace(asStringValue(input["path"]))
		return "Read", map[string]any{"file_path": path}

	case "spawn_agent":
		prompt := strings.TrimSpace(asStringValue(input["message"]))
		agentType := normalizeCodexAgentType(strings.TrimSpace(asStringValue(input["agent_type"])))
		desc := prompt
		if len(desc) > 50 {
			desc = desc[:50]
		}
		return "Agent", map[string]any{
			"prompt":        prompt,
			"subagent_type": agentType,
			"description":   desc,
		}

	case "request_user_input":
		return "AskUserQuestion", input

	case "update_plan":
		return "TodoWrite", input

	default:
		return name, input
	}
}

// normalizeCodexAgentType maps Codex agent_type values to Claude subagent_type values.
func normalizeCodexAgentType(agentType string) string {
	switch strings.ToLower(agentType) {
	case "explorer":
		return "Explore"
	case "planner":
		return "Plan"
	case "default", "":
		return "general-purpose"
	default:
		return agentType
	}
}

// isCodexLifecycleTool returns true for Codex-only agent lifecycle tools
// that have no Claude equivalent and should be filtered during conversion.
func isCodexLifecycleTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "wait", "close_agent":
		return true
	default:
		return false
	}
}

func extractPatchFilePath(patch string) string {
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if strings.HasPrefix(line, prefix) {
				return strings.TrimSpace(strings.TrimPrefix(line, prefix))
			}
		}
	}
	return ""
}

func asStringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

type sessionsIndexEntry struct {
	SessionID string `json:"sessionId"`
	FullPath  string `json:"fullPath"`
}

type sessionsIndexFile struct {
	Version int                  `json:"version"`
	Entries []sessionsIndexEntry `json:"entries"`
}

func upsertSessionsIndex(path, sessionID, sessionPath string) error {
	idx := sessionsIndexFile{
		Version: 1,
		Entries: []sessionsIndexEntry{},
	}
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		_ = json.Unmarshal(b, &idx)
	}
	if idx.Version == 0 {
		idx.Version = 1
	}

	found := false
	for i := range idx.Entries {
		if idx.Entries[i].SessionID == sessionID {
			idx.Entries[i].FullPath = sessionPath
			found = true
			break
		}
	}
	if !found {
		idx.Entries = append(idx.Entries, sessionsIndexEntry{
			SessionID: sessionID,
			FullPath:  sessionPath,
		})
	}
	sort.SliceStable(idx.Entries, func(i, j int) bool {
		return idx.Entries[i].SessionID < idx.Entries[j].SessionID
	})

	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func projectKeyFromCWD(cwd string) string {
	clean := filepath.Clean(strings.TrimSpace(cwd))
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.ReplaceAll(clean, string(filepath.Separator), "-")
	return "-" + clean
}

func choose(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
