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
	var sawShell bool
	var sawOutput bool
	var sawSynthetic bool
	var sawOrphan bool
	for _, it := range out.Items {
		switch it.Kind {
		case session.CodexItemFunctionCall:
			if it.Name == "shell" {
				sawShell = true
				cmd, ok := it.Arguments["command"].([]any)
				if !ok || len(cmd) != 3 {
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

	if !sawShell || !sawOutput || !sawSynthetic || !sawOrphan {
		t.Fatalf("missing expected conversion artifacts: shell=%v output=%v synthetic=%v orphan=%v\nitems=%+v", sawShell, sawOutput, sawSynthetic, sawOrphan, out.Items)
	}
}
