package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mithileshchellappan/resume/internal/session"
)

var ErrNoSourceSessions = errors.New("no source sessions found")
var errSelectionCanceled = errors.New("selection canceled")

func SelectSessionInteractive(in io.Reader, out io.Writer, sessions []session.SourceSession, folderHint string) (session.SourceSession, error) {
	prepared := normalizeSourceSessions(sessions)
	if len(prepared) == 0 {
		return session.SourceSession{}, ErrNoSourceSessions
	}
	lineReader := bufio.NewReader(in)

	groups := map[string][]session.SourceSession{}
	folders := make([]string, 0)
	for _, s := range prepared {
		if _, ok := groups[s.CWD]; !ok {
			folders = append(folders, s.CWD)
		}
		groups[s.CWD] = append(groups[s.CWD], s)
	}
	sort.Strings(folders)

	selectedFolder := chooseFolder(folderHint, folders)
	if selectedFolder == "" {
		if len(folders) == 1 {
			selectedFolder = folders[0]
		} else {
			folderOptions := make([]string, 0, len(folders))
			for _, folder := range folders {
				folderOptions = append(folderOptions, fmt.Sprintf("%s (%d sessions)", folder, len(groups[folder])))
			}
			idx, err := pickListIndex(in, lineReader, out, "Select Folder", "Select folder", folderOptions)
			if err != nil {
				return session.SourceSession{}, err
			}
			selectedFolder = folders[idx]
		}
	}

	candidates := groups[selectedFolder]
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})

	sessionOptions := make([]string, 0, len(candidates))
	for i, s := range candidates {
		sessionOptions = append(sessionOptions, fmt.Sprintf("%s [%s] %s", formatDisplayTitle(s.Title, 140), s.ID, formatUpdatedAt(s.UpdatedAt)))
		_ = i
	}
	idx, err := pickListIndex(in, lineReader, out, "Select Session", "Select session", sessionOptions)
	if err != nil {
		return session.SourceSession{}, err
	}
	return candidates[idx], nil
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

func pickListIndex(in io.Reader, lineReader *bufio.Reader, out io.Writer, title, prompt string, options []string) (int, error) {
	if len(options) == 0 {
		return 0, ErrNoSourceSessions
	}
	if len(options) == 1 {
		return 0, nil
	}
	if canUseTerminalPicker(in, out) {
		return runTerminalPicker(in, out, title, options)
	}
	return runNumberedPicker(lineReader, out, prompt, options)
}

func runNumberedPicker(r *bufio.Reader, out io.Writer, prompt string, options []string) (int, error) {
	fmt.Fprintf(out, "%s:\n", prompt)
	for i, option := range options {
		fmt.Fprintf(out, "  %d) %s\n", i+1, option)
	}
	idx, err := promptNumber(r, out, prompt, len(options))
	if err != nil {
		return 0, err
	}
	return idx - 1, nil
}

func canUseTerminalPicker(in io.Reader, out io.Writer) bool {
	inFile, ok := in.(*os.File)
	if !ok {
		return false
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return false
	}
	if strings.TrimSpace(strings.ToLower(os.Getenv("TERM"))) == "dumb" {
		return false
	}
	return isTerminalFile(inFile) && isTerminalFile(outFile)
}

func runTerminalPicker(in io.Reader, out io.Writer, title string, options []string) (int, error) {
	inFile := in.(*os.File)
	outFile := out.(*os.File)

	restore, err := setInputRawMode(inFile)
	if err != nil {
		return runNumberedPicker(bufio.NewReader(in), out, title, options)
	}
	defer restore()

	_, _ = fmt.Fprint(outFile, "\x1b[?25l")
	defer func() {
		_, _ = fmt.Fprint(outFile, "\x1b[?25h\x1b[0m\n")
	}()

	reader := bufio.NewReader(inFile)
	selected := 0
	for {
		renderPickerFrame(outFile, title, options, selected)
		key, readErr := readPickerKey(reader)
		if readErr != nil {
			return 0, readErr
		}
		switch key {
		case pickerKeyUp:
			if selected > 0 {
				selected--
			}
		case pickerKeyDown:
			if selected < len(options)-1 {
				selected++
			}
		case pickerKeyEnter:
			return selected, nil
		case pickerKeyCancel:
			return 0, errSelectionCanceled
		}
	}
}

func renderPickerFrame(out *os.File, title string, options []string, selected int) {
	const maxVisible = 14
	start := 0
	if len(options) > maxVisible {
		start = selected - (maxVisible / 2)
		if start < 0 {
			start = 0
		}
		maxStart := len(options) - maxVisible
		if start > maxStart {
			start = maxStart
		}
	}
	end := len(options)
	if len(options) > maxVisible {
		end = start + maxVisible
	}

	_, _ = fmt.Fprint(out, "\x1b[2J\x1b[H")
	_, _ = fmt.Fprintf(out, "%s\n", title)
	_, _ = fmt.Fprintln(out, "Use ↑/↓ (or j/k), Enter to select.")
	if start > 0 {
		_, _ = fmt.Fprintf(out, "  ... %d above\n", start)
	}
	for i := start; i < end; i++ {
		prefix := "  "
		if i == selected {
			prefix = "> "
		}
		_, _ = fmt.Fprintf(out, "%s%s\n", prefix, options[i])
	}
	if end < len(options) {
		_, _ = fmt.Fprintf(out, "  ... %d more\n", len(options)-end)
	}
}

type pickerKey int

const (
	pickerKeyUnknown pickerKey = iota
	pickerKeyUp
	pickerKeyDown
	pickerKeyEnter
	pickerKeyCancel
)

func readPickerKey(r *bufio.Reader) (pickerKey, error) {
	b, err := r.ReadByte()
	if err != nil {
		return pickerKeyUnknown, err
	}
	switch b {
	case 3, 'q':
		return pickerKeyCancel, nil
	case '\r', '\n':
		return pickerKeyEnter, nil
	case 'k':
		return pickerKeyUp, nil
	case 'j':
		return pickerKeyDown, nil
	case 27:
		b2, err := r.ReadByte()
		if err != nil {
			return pickerKeyUnknown, nil
		}
		if b2 != '[' {
			return pickerKeyUnknown, nil
		}
		b3, err := r.ReadByte()
		if err != nil {
			return pickerKeyUnknown, nil
		}
		switch b3 {
		case 'A':
			return pickerKeyUp, nil
		case 'B':
			return pickerKeyDown, nil
		}
	}
	return pickerKeyUnknown, nil
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
