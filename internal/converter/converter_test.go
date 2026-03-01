package converter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

type fakeGen struct {
	ids []string
	i   int
}

func (g *fakeGen) NewCallID() (string, error) {
	id := g.ids[g.i]
	g.i++
	return id, nil
}

func TestConvertMappingPairingAndOrphans(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ir := session.SessionIR{
		SourceID:  "sess-1",
		CWD:       "/repo",
		StartedAt: ts,
		OrderedEvents: []session.Event{
			{Kind: session.EventUserMessage, Msg: &session.Message{Role: "user", Content: "hello", Timestamp: ts}},
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "toolu_1", Name: "Bash", Input: map[string]any{"command": "ls -la"}, Timestamp: ts.Add(time.Second), Index: 1}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "toolu_1", Output: "ok", Timestamp: ts.Add(2 * time.Second)}},
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "toolu_2", Name: "Read", Input: map[string]any{"path": "a.go"}, Timestamp: ts.Add(3 * time.Second), Index: 2}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "missing", Output: "oops", Timestamp: ts.Add(4 * time.Second)}},
		},
	}

	conv := &Converter{IDGen: &fakeGen{ids: []string{"call_aaaaaaaaaaaaaaaaaaaaaaaa", "call_bbbbbbbbbbbbbbbbbbbbbbbb"}}, Now: func() time.Time { return ts }}
	out, err := conv.Convert(context.Background(), ir)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if !out.HasUserEvent || out.FirstUserMessage != "hello" {
		t.Fatalf("bad user metadata: %+v", out)
	}

	// Check Bash mapping and rewritten call IDs.
	var sawShellCommand bool
	var sawOutput bool
	var sawSynthetic bool
	var sawOrphan bool
	for _, it := range out.Items {
		switch it.Kind {
		case session.CodexItemFunctionCall:
			if it.Name == "shell_command" && it.CallID == "call_aaaaaaaaaaaaaaaaaaaaaaaa" {
				sawShellCommand = true
				cmd, ok := it.Arguments["command"].(string)
				if !ok || cmd != "ls -la" {
					t.Fatalf("bad shell args: %#v", it.Arguments)
				}
			}
		case session.CodexItemFunctionOut:
			if it.CallID == "call_aaaaaaaaaaaaaaaaaaaaaaaa" && it.Output == "ok" {
				sawOutput = true
			}
			if it.CallID == "call_bbbbbbbbbbbbbbbbbbbbbbbb" && it.Output == "[no output recorded]" {
				sawSynthetic = true
			}
		case session.CodexItemAssistantText:
			if strings.HasPrefix(it.Text, orphanPrefix) {
				sawOrphan = true
			}
		}
	}

	if !sawShellCommand || !sawOutput || !sawSynthetic || !sawOrphan {
		t.Fatalf("missing expected conversion artifacts: shell_command=%v output=%v synthetic=%v orphan=%v\nitems=%+v", sawShellCommand, sawOutput, sawSynthetic, sawOrphan, out.Items)
	}
}

func TestConvertNormalizesClaudeToolCallNamesAndArgs(t *testing.T) {
	ts := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	ir := session.SessionIR{
		SourceID:  "sess-tools",
		CWD:       "/repo",
		StartedAt: ts,
		OrderedEvents: []session.Event{
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "toolu_glob", Name: "Glob", Input: map[string]any{"pattern": "**/*.go", "path": "/repo"}, Timestamp: ts.Add(time.Second), Index: 1}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "toolu_glob", Output: "a.go\nb.go", Timestamp: ts.Add(2 * time.Second)}},
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "toolu_read", Name: "Read", Input: map[string]any{"file_path": "/repo/go.mod"}, Timestamp: ts.Add(3 * time.Second), Index: 2}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "toolu_read", Output: "module example", Timestamp: ts.Add(4 * time.Second)}},
			{Kind: session.EventToolCall, Call: &session.ToolCall{SourceID: "toolu_bash", Name: "Bash", Input: map[string]any{"command": "git status --short", "description": "show status"}, Timestamp: ts.Add(5 * time.Second), Index: 3}},
			{Kind: session.EventToolResult, Result: &session.ToolResult{CallSourceID: "toolu_bash", Output: "", Timestamp: ts.Add(6 * time.Second)}},
		},
	}

	conv := &Converter{
		IDGen: &fakeGen{ids: []string{
			"call_111111111111111111111111",
			"call_222222222222222222222222",
			"call_333333333333333333333333",
		}},
		Now: func() time.Time { return ts },
	}
	out, err := conv.Convert(context.Background(), ir)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	calls := map[string]session.CodexItem{}
	for _, it := range out.Items {
		if it.Kind == session.CodexItemFunctionCall {
			calls[it.CallID] = it
		}
	}

	globCall, ok := calls["call_111111111111111111111111"]
	if !ok {
		t.Fatalf("missing glob call: %+v", out.Items)
	}
	if globCall.Name != "shell_command" {
		t.Fatalf("glob name mismatch: %q", globCall.Name)
	}
	if globCall.Arguments["command"] != "rg --files -g '**/*.go' '/repo'" {
		t.Fatalf("glob args mismatch: %#v", globCall.Arguments)
	}

	readCall, ok := calls["call_222222222222222222222222"]
	if !ok {
		t.Fatalf("missing read call: %+v", out.Items)
	}
	if readCall.Name != "shell_command" {
		t.Fatalf("read name mismatch: %q", readCall.Name)
	}
	if readCall.Arguments["command"] != "sed -n '1,250p' '/repo/go.mod'" {
		t.Fatalf("read args mismatch: %#v", readCall.Arguments)
	}

	bashCall, ok := calls["call_333333333333333333333333"]
	if !ok {
		t.Fatalf("missing bash call: %+v", out.Items)
	}
	if bashCall.Name != "shell_command" {
		t.Fatalf("bash name mismatch: %q", bashCall.Name)
	}
	cmd, ok := bashCall.Arguments["command"].(string)
	if !ok || cmd != "git status --short" {
		t.Fatalf("bash command args mismatch: %#v", bashCall.Arguments)
	}
	if bashCall.Arguments["description"] != "show status" {
		t.Fatalf("bash description missing: %#v", bashCall.Arguments)
	}
}
