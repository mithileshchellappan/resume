package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

func TestSelectSessionInteractiveWithFolderHint(t *testing.T) {
	sessions := []session.SourceSession{
		{ID: "a1", CWD: "/repo/a", Title: "Fix A", UpdatedAt: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)},
		{ID: "a2", CWD: "/repo/a", Title: "Fix B", UpdatedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)},
		{ID: "b1", CWD: "/repo/b", Title: "Fix C", UpdatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
	}

	in := strings.NewReader("1\n")
	var out bytes.Buffer
	picked, err := SelectSessionInteractive(in, &out, sessions, "/repo/b")
	if err != nil {
		t.Fatalf("select session: %v", err)
	}
	if picked.ID != "b1" {
		t.Fatalf("unexpected selected id: %s", picked.ID)
	}
	if strings.Contains(out.String(), "Select folder") {
		t.Fatalf("did not expect folder prompt with matching folder hint")
	}
}

func TestSelectSessionInteractiveFolderThenSession(t *testing.T) {
	sessions := []session.SourceSession{
		{ID: "a1", CWD: "/repo/a", Title: "Fix A", UpdatedAt: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)},
		{ID: "b1", CWD: "/repo/b", Title: "Fix B", UpdatedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)},
		{ID: "b2", CWD: "/repo/b", Title: "Fix C", UpdatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
	}

	in := strings.NewReader("2\n2\n")
	var out bytes.Buffer
	picked, err := SelectSessionInteractive(in, &out, sessions, "")
	if err != nil {
		t.Fatalf("select session: %v", err)
	}
	if picked.ID != "b1" {
		t.Fatalf("expected second item in /repo/b list, got %s", picked.ID)
	}
	if !strings.Contains(out.String(), "Select folder") {
		t.Fatalf("expected folder prompt output, got: %s", out.String())
	}
}

func TestSelectSessionInteractiveRejectsInvalidInput(t *testing.T) {
	sessions := []session.SourceSession{{ID: "a1", CWD: "/repo/a", Title: "Fix A", UpdatedAt: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}}

	in := strings.NewReader("x\n1\n")
	var out bytes.Buffer
	picked, err := SelectSessionInteractive(in, &out, sessions, "/repo/a")
	if err != nil {
		t.Fatalf("select session: %v", err)
	}
	if picked.ID != "a1" {
		t.Fatalf("unexpected selected id: %s", picked.ID)
	}
	if !strings.Contains(out.String(), "Enter a number") {
		t.Fatalf("expected validation prompt, got: %s", out.String())
	}
}
