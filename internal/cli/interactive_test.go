package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	sessions := []session.SourceSession{
		{ID: "a1", CWD: "/repo/a", Title: "Fix A", UpdatedAt: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)},
		{ID: "a2", CWD: "/repo/a", Title: "Fix B", UpdatedAt: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)},
	}

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

func TestSessionMetaLineIncludesTimeBranchAndSize(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s := session.SourceSession{
		UpdatedAt: now.Add(-2 * time.Hour),
		GitBranch: "main",
		SizeBytes: 386 * 1024,
		Title:     "x",
		ID:        "id",
		CWD:       "/repo",
	}
	got := sessionMetaLine(s, now)
	if !strings.Contains(got, "2h ago") {
		t.Fatalf("expected relative time in %q", got)
	}
	if !strings.Contains(got, "main") {
		t.Fatalf("expected branch in %q", got)
	}
	if !strings.Contains(got, "386KB") {
		t.Fatalf("expected size in %q", got)
	}
}

func TestPickerVisibleEntriesAndViewport(t *testing.T) {
	opts := make([]pickerOption, 0, 20)
	for i := 0; i < 20; i++ {
		opts = append(opts, pickerOption{
			Primary:   fmt.Sprintf("row-%02d", i),
			Secondary: "meta",
			Search:    fmt.Sprintf("row-%02d", i),
		})
	}
	m := newPickerModel(pickerConfig{
		Title:       "Resume Session",
		Placeholder: "Search...",
		Options:     opts,
	})

	model, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	pm := model.(pickerModel)
	if got := pm.visibleEntries(); got != 6 {
		t.Fatalf("visibleEntries mismatch: got %d want 6", got)
	}

	pm.cursor = 10
	pm.ensureCursorVisible()
	if pm.offset != 5 {
		t.Fatalf("offset mismatch: got %d want 5", pm.offset)
	}
}

func TestPickerViewShowsMarkerForSelectedItem(t *testing.T) {
	m := newPickerModel(pickerConfig{
		Title:       "Resume Session",
		Placeholder: "Search...",
		Options: []pickerOption{
			{Primary: "first", Secondary: "meta", Search: "first"},
			{Primary: "second", Secondary: "meta", Search: "second"},
		},
	})
	model, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	pm := model.(pickerModel)
	view := pm.View()
	if !strings.Contains(view, "❯") {
		t.Fatalf("expected selection marker in view, got:\n%s", view)
	}
}
