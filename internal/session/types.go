package session

import (
	"context"
	"time"
)

// Message is a normalized chat message across source/target tools.
type Message struct {
	Role      string
	Content   string
	Timestamp time.Time
	Reasoning string
}

// ToolCall is a normalized tool invocation.
type ToolCall struct {
	SourceID  string
	TargetID  string
	Name      string
	Input     map[string]any
	Index     int
	Timestamp time.Time
}

// ToolResult is a normalized tool result payload.
type ToolResult struct {
	CallSourceID string
	CallTargetID string
	Output       string
	Timestamp    time.Time
}

// EventKind identifies a normalized event.
type EventKind string

const (
	EventUserMessage      EventKind = "user_message"
	EventAssistantMessage EventKind = "assistant_message"
	EventToolCall         EventKind = "tool_call"
	EventToolResult       EventKind = "tool_result"
)

// Event is a normalized union of session events.
type Event struct {
	Kind   EventKind
	Msg    *Message
	Call   *ToolCall
	Result *ToolResult
}

// SessionIR is the unified intermediate representation.
type SessionIR struct {
	SourceID      string
	CWD           string
	StartedAt     time.Time
	Messages      []Message
	Calls         []ToolCall
	Results       []ToolResult
	OrderedEvents []Event
}

// CodexItemKind identifies the logical item that will be encoded to Codex JSONL.
type CodexItemKind string

const (
	CodexItemUserMessage   CodexItemKind = "user_message"
	CodexItemAssistantText CodexItemKind = "assistant_message"
	CodexItemFunctionCall  CodexItemKind = "function_call"
	CodexItemFunctionOut   CodexItemKind = "function_call_output"
)

// CodexItem is a target-tool event before JSONL serialization.
type CodexItem struct {
	Kind      CodexItemKind
	Role      string
	Text      string
	CallID    string
	Name      string
	Arguments map[string]any
	Output    string
	Timestamp time.Time
}

// CodexSession is a normalized target session before persisting to Codex store.
type CodexSession struct {
	SessionID        string
	SourceSessionID  string
	CWD              string
	StartedAt        time.Time
	Items            []CodexItem
	HasUserEvent     bool
	FirstUserMessage string
}

// CodexThreadMeta is metadata used to populate Codex thread indices.
type CodexThreadMeta struct {
	CWD               string
	Title             string
	CLIVersion        string
	ApprovalMode      string
	SandboxPolicyJSON string
	FirstUserMessage  string
}

// ClaudeSessionMeta is metadata used when persisting a Claude-native session.
type ClaudeSessionMeta struct {
	CWD       string
	Title     string
	GitBranch string
}

// Converter converts IR into a target Codex session.
type Converter interface {
	Convert(ctx context.Context, in SessionIR) (CodexSession, error)
}

// ClaudeLoader loads Claude native session into IR.
type ClaudeLoader interface {
	LoadBySessionID(ctx context.Context, id string) (SessionIR, error)
}

// CodexWriter writes a Codex session into native Codex stores.
type CodexWriter interface {
	Write(ctx context.Context, s CodexSession, meta CodexThreadMeta) (threadID string, rolloutPath string, err error)
}

// CodexLoader loads Codex native session into IR.
type CodexLoader interface {
	LoadByThreadID(ctx context.Context, id string) (SessionIR, error)
}

// ClaudeWriter writes a Claude session into native Claude stores.
type ClaudeWriter interface {
	Write(ctx context.Context, in SessionIR, meta ClaudeSessionMeta) (sessionID string, sessionPath string, err error)
}
