package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestSelectToolInteractiveNumberedFallback(t *testing.T) {
	in := strings.NewReader("2\n")
	var out bytes.Buffer

	picked, err := SelectToolInteractive(in, &out, "Select Source Tool", []string{"claude", "codex"})
	if err != nil {
		t.Fatalf("select tool: %v", err)
	}
	if picked != "codex" {
		t.Fatalf("unexpected selected tool: %s", picked)
	}
	if !strings.Contains(out.String(), "Select Source Tool:") {
		t.Fatalf("expected numbered prompt output, got: %s", out.String())
	}
}

func TestSelectToolInteractiveRejectsInvalidNumber(t *testing.T) {
	in := strings.NewReader("9\n1\n")
	var out bytes.Buffer

	picked, err := SelectToolInteractive(in, &out, "Select Target Tool", []string{"claude", "codex"})
	if err != nil {
		t.Fatalf("select tool: %v", err)
	}
	if picked != "claude" {
		t.Fatalf("unexpected selected tool: %s", picked)
	}
	if !strings.Contains(out.String(), "Enter a number between 1 and 2.") {
		t.Fatalf("expected validation output, got: %s", out.String())
	}
}

func TestSelectToolInteractiveSingleToolReturnsImmediately(t *testing.T) {
	in := strings.NewReader("")
	var out bytes.Buffer

	picked, err := SelectToolInteractive(in, &out, "Select Source Tool", []string{"claude"})
	if err != nil {
		t.Fatalf("select tool: %v", err)
	}
	if picked != "claude" {
		t.Fatalf("unexpected selected tool: %s", picked)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no prompt output for single option")
	}
}

func TestSelectToolInteractiveNoTools(t *testing.T) {
	_, err := SelectToolInteractive(strings.NewReader(""), &bytes.Buffer{}, "Select Source Tool", nil)
	if !errors.Is(err, ErrNoToolsFound) {
		t.Fatalf("expected ErrNoToolsFound, got %v", err)
	}
}
