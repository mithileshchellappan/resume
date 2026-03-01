package claude

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mithileshchellappan/resume/internal/session"
)

var ErrWrite = errors.New("claude native write failed")

const defaultClaudeVersion = "2.1.63"
const claudeToolIDLength = 24

// Keep a conservative content budget: Claude session rendering duplicates
// portions of tool payloads, so this must sit well below nominal model context.
const claudeContextBudgetChars = 250_000
const claudeTruncatedKeepChars = 256
const claudeToolInputSoftLimitChars = 1_024
const claudeEventHardLimitChars = 2_048
const claudeToolInputHardLimitChars = 1_024

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
	events = normalizeEventsForClaudeContext(events, claudeContextBudgetChars)

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
	callInfoMap := map[string]toolCallInfo{}
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
			claudeToolID := newClaudeToolUseID()
			callIDMap[sourceID] = claudeToolID

			toolName, toolInput := normalizeCodexToolForClaude(ev.Call.Name, ev.Call.Input)
			callInfoMap[sourceID] = toolCallInfo{
				Name:  toolName,
				Input: cloneMap(toolInput),
			}
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
				toolUseID = newClaudeToolUseID()
			}
			output := normalizeToolResultOutput(ev.Result.Output)
			if output == "" {
				output = "[no output recorded]"
			}
			callInfo := callInfoMap[callSourceID]
			toolUseResult := buildToolUseResult(callInfo.Name, callInfo.Input, output, ev.Result.Output)

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
			line["toolUseResult"] = toolUseResult
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

func normalizeEventsForClaudeContext(events []session.Event, maxChars int) []session.Event {
	if len(events) == 0 {
		return events
	}
	if maxChars <= 0 {
		maxChars = claudeContextBudgetChars
	}

	out := cloneEvents(events)
	for i := range out {
		out[i] = trimEventForContext(out[i], claudeEventHardLimitChars, claudeToolInputHardLimitChars)
	}

	total := estimateEventsChars(out)
	if total <= maxChars {
		return out
	}

	// Progressive passes preserve readability while forcing convergence under budget.
	passPlan := []struct {
		keepChars          int
		toolInputSoftLimit int
	}{
		{keepChars: claudeTruncatedKeepChars, toolInputSoftLimit: claudeToolInputSoftLimitChars},
		{keepChars: 96, toolInputSoftLimit: 256},
		{keepChars: 24, toolInputSoftLimit: 64},
	}
	for _, pass := range passPlan {
		for i := 0; i < len(out) && total > maxChars; i++ {
			before := estimateEventChars(out[i])
			out[i] = trimEventForContext(out[i], pass.keepChars, pass.toolInputSoftLimit)
			after := estimateEventChars(out[i])
			total -= before - after
		}
		if total <= maxChars {
			break
		}
	}

	return out
}

func estimateEventsChars(events []session.Event) int {
	total := 0
	for _, ev := range events {
		total += estimateEventChars(ev)
	}
	return total
}

func estimateEventChars(ev session.Event) int {
	switch ev.Kind {
	case session.EventUserMessage, session.EventAssistantMessage:
		if ev.Msg == nil {
			return 0
		}
		return len(ev.Msg.Content)
	case session.EventToolCall:
		if ev.Call == nil {
			return 0
		}
		size := len(ev.Call.Name)
		if ev.Call.Input != nil {
			if b, err := json.Marshal(ev.Call.Input); err == nil {
				size += len(b)
			}
		}
		return size
	case session.EventToolResult:
		if ev.Result == nil {
			return 0
		}
		return len(ev.Result.Output)
	default:
		return 0
	}
}

func trimEventForContext(ev session.Event, keepChars, toolInputSoftLimit int) session.Event {
	switch ev.Kind {
	case session.EventUserMessage, session.EventAssistantMessage:
		if ev.Msg == nil {
			return ev
		}
		ev.Msg.Content = truncateForContext(ev.Msg.Content, keepChars)
	case session.EventToolCall:
		if ev.Call == nil || ev.Call.Input == nil {
			return ev
		}
		ev.Call.Input = truncateInputForContext(ev.Call.Input, toolInputSoftLimit)
	case session.EventToolResult:
		if ev.Result == nil {
			return ev
		}
		ev.Result.Output = truncateForContext(ev.Result.Output, keepChars)
	}
	return ev
}

func truncateForContext(s string, keepChars int) string {
	if keepChars <= 0 {
		keepChars = claudeTruncatedKeepChars
	}
	if len(s) <= keepChars {
		return s
	}
	head := s[:keepChars]
	removed := len(s) - keepChars
	return fmt.Sprintf("[truncated for target model context; original chars=%d, removed=%d]\n%s", len(s), removed, head)
}

func truncateInputForContext(in map[string]any, limit int) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = truncateAnyForContext(v, limit)
	}
	return out
}

func truncateAnyForContext(v any, limit int) any {
	switch x := v.(type) {
	case string:
		if limit > 0 && len(x) > limit {
			return truncateForContext(x, limit)
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = truncateAnyForContext(x[i], limit)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = truncateAnyForContext(vv, limit)
		}
		return out
	default:
		return v
	}
}

func cloneEvents(events []session.Event) []session.Event {
	out := make([]session.Event, 0, len(events))
	for _, ev := range events {
		clone := ev
		switch ev.Kind {
		case session.EventUserMessage, session.EventAssistantMessage:
			if ev.Msg != nil {
				m := *ev.Msg
				clone.Msg = &m
			}
		case session.EventToolCall:
			if ev.Call != nil {
				c := *ev.Call
				c.Input = cloneMap(ev.Call.Input)
				clone.Call = &c
			}
		case session.EventToolResult:
			if ev.Result != nil {
				r := *ev.Result
				clone.Result = &r
			}
		}
		out = append(out, clone)
	}
	return out
}

type toolCallInfo struct {
	Name  string
	Input map[string]any
}

func buildToolUseResult(toolName string, toolInput map[string]any, output, rawOutput string) any {
	switch strings.TrimSpace(toolName) {
	case "Edit", "Write", "MultiEdit":
		return buildFileToolUseResult(toolInput)
	case "Bash":
		return buildBashToolUseResult(output, rawOutput)
	case "Agent":
		return buildAgentToolUseResult(toolInput, output, rawOutput)
	case "AskUserQuestion":
		return buildAskUserQuestionToolUseResult(toolInput, output, rawOutput)
	default:
		return output
	}
}

func buildFileToolUseResult(toolInput map[string]any) map[string]any {
	if toolInput == nil {
		toolInput = map[string]any{}
	}

	filePath := firstNonEmptyString(toolInput["file_path"], toolInput["filePath"], toolInput["path"])
	oldString := firstNonEmptyString(toolInput["old_string"], toolInput["oldString"])
	newString := firstNonEmptyString(toolInput["new_string"], toolInput["newString"], toolInput["content"])
	// Keep migrated output deterministic and bounded: never hydrate from local disk.
	originalFile := firstNonEmptyString(toolInput["original_file"], toolInput["originalFile"], oldString)

	replaceAll := false
	if b, ok := asBoolValue(toolInput["replace_all"]); ok {
		replaceAll = b
	} else if b, ok := asBoolValue(toolInput["replaceAll"]); ok {
		replaceAll = b
	}

	userModified := false
	if b, ok := asBoolValue(toolInput["user_modified"]); ok {
		userModified = b
	} else if b, ok := asBoolValue(toolInput["userModified"]); ok {
		userModified = b
	}

	structuredPatch := []map[string]any{}
	if oldString != "" || newString != "" {
		structuredPatch = append(structuredPatch, map[string]any{
			"oldStart": 1,
			"oldLines": countLines(oldString),
			"newStart": 1,
			"newLines": countLines(newString),
			"lines":    buildPatchLines(oldString, newString),
		})
	}

	return map[string]any{
		"filePath":        filePath,
		"oldString":       oldString,
		"newString":       newString,
		"originalFile":    originalFile,
		"structuredPatch": structuredPatch,
		"userModified":    userModified,
		"replaceAll":      replaceAll,
	}
}

func buildPatchLines(oldString, newString string) []string {
	lines := make([]string, 0, countLines(oldString)+countLines(newString))
	if oldString != "" {
		for _, line := range strings.Split(oldString, "\n") {
			lines = append(lines, "-"+line)
		}
	}
	if newString != "" {
		for _, line := range strings.Split(newString, "\n") {
			lines = append(lines, "+"+line)
		}
	}
	return lines
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func buildBashToolUseResult(output, rawOutput string) map[string]any {
	stdout := output
	stderr := ""
	interrupted := false
	isImage := false
	noOutputExpected := false

	if payload, ok := parseJSONMap(rawOutput); ok {
		if s, ok := payload["stdout"]; ok {
			stdout = asStringValue(s)
		}
		if s, ok := payload["stderr"]; ok {
			stderr = asStringValue(s)
		}
		if b, ok := asBoolValue(payload["interrupted"]); ok {
			interrupted = b
		}
		if b, ok := asBoolValue(payload["isImage"]); ok {
			isImage = b
		}
		if b, ok := asBoolValue(payload["noOutputExpected"]); ok {
			noOutputExpected = b
		}
	}

	return map[string]any{
		"stdout":           stdout,
		"stderr":           stderr,
		"interrupted":      interrupted,
		"isImage":          isImage,
		"noOutputExpected": noOutputExpected,
	}
}

func buildAgentToolUseResult(toolInput map[string]any, output, rawOutput string) map[string]any {
	if toolInput == nil {
		toolInput = map[string]any{}
	}

	payload := map[string]any{}
	if p, ok := parseJSONMap(rawOutput); ok {
		payload = p
	} else if p, ok := parseJSONMap(output); ok {
		payload = p
	}

	agentID := firstNonEmptyString(payload["agent_id"], payload["agentId"], payload["id"])
	status := strings.TrimSpace(asStringValue(payload["status"]))
	if status == "" {
		if agentID != "" {
			status = "completed"
		} else {
			status = "unknown"
		}
	}

	content := output
	if s := strings.TrimSpace(asStringValue(payload["content"])); s != "" {
		content = s
	}

	usage := map[string]any{}
	if u, ok := payload["usage"].(map[string]any); ok {
		usage = u
	}

	totalDurationMs, _ := asIntValue(payload["totalDurationMs"])
	totalTokens, _ := asIntValue(payload["totalTokens"])
	totalToolUseCount, _ := asIntValue(payload["totalToolUseCount"])

	return map[string]any{
		"agentId":           agentID,
		"status":            status,
		"prompt":            firstNonEmptyString(toolInput["prompt"], toolInput["message"]),
		"content":           content,
		"totalDurationMs":   totalDurationMs,
		"totalTokens":       totalTokens,
		"totalToolUseCount": totalToolUseCount,
		"usage":             usage,
	}
}

func buildAskUserQuestionToolUseResult(toolInput map[string]any, output, rawOutput string) any {
	payload := map[string]any{}
	if p, ok := parseJSONMap(rawOutput); ok {
		payload = p
	} else if p, ok := parseJSONMap(output); ok {
		payload = p
	} else {
		return output
	}

	rawAnswers, ok := payload["answers"].(map[string]any)
	if !ok || rawAnswers == nil {
		return output
	}

	questions := []any{}
	questionByID := map[string]string{}
	if toolInput != nil {
		if list, ok := toolInput["questions"].([]any); ok {
			questions = list
			for _, item := range list {
				question, ok := item.(map[string]any)
				if !ok {
					continue
				}
				questionID := strings.TrimSpace(asStringValue(question["id"]))
				questionText := strings.TrimSpace(asStringValue(question["question"]))
				if questionID != "" && questionText != "" {
					questionByID[questionID] = questionText
				}
			}
		}
	}

	answers := map[string]any{}
	for key, value := range rawAnswers {
		answerText := strings.TrimSpace(extractAskUserAnswerText(value))
		if answerText == "" {
			continue
		}
		questionKey := strings.TrimSpace(key)
		if mapped := strings.TrimSpace(questionByID[questionKey]); mapped != "" {
			questionKey = mapped
		}
		if questionKey == "" {
			continue
		}
		answers[questionKey] = answerText
	}
	if len(answers) == 0 {
		return output
	}

	result := map[string]any{
		"questions": questions,
		"answers":   answers,
	}
	if annotations, ok := payload["annotations"].(map[string]any); ok && len(annotations) > 0 {
		result["annotations"] = annotations
	}
	return result
}

func extractAskUserAnswerText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s := strings.TrimSpace(extractAskUserAnswerText(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		if answers, ok := x["answers"]; ok {
			return extractAskUserAnswerText(answers)
		}
		if answer, ok := x["answer"]; ok {
			return extractAskUserAnswerText(answer)
		}
	}
	return strings.TrimSpace(asStringValue(v))
}

func parseJSONMap(raw string) (map[string]any, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload == nil {
		return nil, false
	}
	return payload, true
}

func asBoolValue(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func asIntValue(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case uint:
		return int(x), true
	case uint8:
		return int(x), true
	case uint16:
		return int(x), true
	case uint32:
		return int(x), true
	case uint64:
		return int(x), true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
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

	case "edit":
		out := cloneMap(input)
		filePath := firstNonEmptyString(
			out["file_path"],
			out["filePath"],
			out["path"],
		)
		out["file_path"] = filePath
		delete(out, "filePath")
		return "Edit", out

	case "multiedit":
		out := cloneMap(input)
		filePath := firstNonEmptyString(
			out["file_path"],
			out["filePath"],
			out["path"],
		)
		out["file_path"] = filePath
		delete(out, "filePath")
		return "MultiEdit", out

	case "view_image":
		path := strings.TrimSpace(asStringValue(input["path"]))
		return "Read", map[string]any{"file_path": path}

	case "read":
		out := cloneMap(input)
		filePath := firstNonEmptyString(
			out["file_path"],
			out["filePath"],
			out["path"],
		)
		clean := map[string]any{"file_path": filePath}
		if v, ok := out["offset"]; ok {
			clean["offset"] = v
		}
		if v, ok := out["limit"]; ok {
			clean["limit"] = v
		}
		return "Read", clean

	case "write":
		out := cloneMap(input)
		filePath := firstNonEmptyString(
			out["file_path"],
			out["filePath"],
			out["path"],
		)
		out["file_path"] = filePath
		delete(out, "filePath")
		return "Write", out

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

func normalizeToolResultOutput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	tryParse := func(s string) (map[string]any, bool) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(s), &payload); err != nil || payload == nil {
			return nil, false
		}
		return payload, true
	}

	// Codex custom tool outputs commonly wrap textual stdout in {"output":"...","metadata":{...}}.
	// Unwrap that shape so Claude renders native tool results as plain text.
	if payload, ok := tryParse(raw); ok {
		if _, hasMetadata := payload["metadata"]; hasMetadata {
			if out := strings.TrimSpace(asStringValue(payload["output"])); out != "" {
				return out
			}
		}
		return raw
	}

	// Some captured outputs decode into strings containing literal newlines/tabs,
	// which makes the wrapper JSON invalid for strict parsing. Re-escape controls
	// and retry extraction.
	sanitized := strings.NewReplacer("\r", "\\r", "\n", "\\n", "\t", "\\t").Replace(raw)
	if payload, ok := tryParse(sanitized); ok {
		if _, hasMetadata := payload["metadata"]; hasMetadata {
			if out := strings.TrimSpace(asStringValue(payload["output"])); out != "" {
				return out
			}
		}
	}
	return raw
}

func firstNonEmptyString(vals ...any) string {
	for _, v := range vals {
		if s := strings.TrimSpace(asStringValue(v)); s != "" {
			return s
		}
	}
	return ""
}

func newClaudeToolUseID() string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, claudeToolIDLength)
	if _, err := rand.Read(buf); err != nil {
		// Keep conversion robust even if random source fails.
		return "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	out := make([]byte, claudeToolIDLength)
	for i := range buf {
		out[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return "toolu_" + string(out)
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
