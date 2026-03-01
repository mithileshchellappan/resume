package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

var ErrNoSourceSessions = errors.New("no source sessions found")

func SelectSessionInteractive(in io.Reader, out io.Writer, sessions []session.SourceSession, folderHint string) (session.SourceSession, error) {
	prepared := normalizeSourceSessions(sessions)
	if len(prepared) == 0 {
		return session.SourceSession{}, ErrNoSourceSessions
	}

	groups := map[string][]session.SourceSession{}
	folders := make([]string, 0)
	for _, s := range prepared {
		if _, ok := groups[s.CWD]; !ok {
			folders = append(folders, s.CWD)
		}
		groups[s.CWD] = append(groups[s.CWD], s)
	}
	sort.Strings(folders)

	r := bufio.NewReader(in)
	selectedFolder := chooseFolder(folderHint, folders)
	if selectedFolder == "" {
		if len(folders) == 1 {
			selectedFolder = folders[0]
		} else {
			fmt.Fprintln(out, "Select folder:")
			for i, folder := range folders {
				fmt.Fprintf(out, "  %d) %s (%d sessions)\n", i+1, folder, len(groups[folder]))
			}
			idx, err := promptNumber(r, out, "Select folder", len(folders))
			if err != nil {
				return session.SourceSession{}, err
			}
			selectedFolder = folders[idx-1]
		}
	}

	candidates := groups[selectedFolder]
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})

	fmt.Fprintf(out, "Select session in %s:\n", selectedFolder)
	for i, s := range candidates {
		fmt.Fprintf(out, "  %d) %s [%s] %s\n", i+1, formatDisplayTitle(s.Title, 140), s.ID, formatUpdatedAt(s.UpdatedAt))
	}
	idx, err := promptNumber(r, out, "Select session", len(candidates))
	if err != nil {
		return session.SourceSession{}, err
	}
	return candidates[idx-1], nil
}

func normalizeSourceSessions(in []session.SourceSession) []session.SourceSession {
	out := make([]session.SourceSession, 0, len(in))
	for _, raw := range in {
		s := raw
		s.ID = strings.TrimSpace(s.ID)
		if s.ID == "" {
			continue
		}
		s.CWD = normalizeFolder(s.CWD)
		s.Title = normalizeDisplayWhitespace(s.Title)
		if s.Title == "" {
			s.Title = s.ID
		}
		out = append(out, s)
	}
	return out
}

func chooseFolder(folderHint string, folders []string) string {
	folderHint = normalizeFolder(folderHint)
	if folderHint == "" {
		return ""
	}

	for _, folder := range folders {
		if folder == folderHint {
			return folder
		}
	}
	for _, folder := range folders {
		if strings.HasPrefix(folderHint, folder+string(filepath.Separator)) {
			return folder
		}
	}
	for _, folder := range folders {
		if strings.HasPrefix(folder, folderHint+string(filepath.Separator)) {
			return folder
		}
	}
	return ""
}

func normalizeFolder(folder string) string {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "."
	}
	if folder == "." {
		return folder
	}
	return filepath.Clean(folder)
}

func promptNumber(r *bufio.Reader, out io.Writer, label string, max int) (int, error) {
	for {
		fmt.Fprintf(out, "%s [1-%d]: ", label, max)
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if errors.Is(err, io.EOF) {
				return 0, fmt.Errorf("%s: input ended", strings.ToLower(label))
			}
			fmt.Fprintln(out, "Enter a number.")
			continue
		}
		n, convErr := strconv.Atoi(trimmed)
		if convErr != nil || n < 1 || n > max {
			fmt.Fprintf(out, "Enter a number between 1 and %d.\n", max)
			if errors.Is(err, io.EOF) {
				return 0, fmt.Errorf("%s: invalid selection", strings.ToLower(label))
			}
			continue
		}
		return n, nil
	}
}

func formatUpdatedAt(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.Local().Format("2006-01-02 15:04")
}

func formatDisplayTitle(s string, max int) string {
	s = normalizeDisplayWhitespace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

func normalizeDisplayWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}
