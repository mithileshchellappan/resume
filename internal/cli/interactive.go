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

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"github.com/mithileshchellappan/resume/internal/session"
)

var ErrNoSourceSessions = errors.New("no source sessions found")
var errSelectionCanceled = errors.New("selection canceled")

type pickerOption struct {
	Primary   string
	Secondary string
	Search    string
}

type pickerConfig struct {
	Title       string
	Prompt      string
	Placeholder string
	Options     []pickerOption
}

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
			folderOptions := make([]pickerOption, 0, len(folders))
			for _, folder := range folders {
				meta := fmt.Sprintf("%d sessions", len(groups[folder]))
				folderOptions = append(folderOptions, pickerOption{
					Primary:   folder,
					Secondary: meta,
					Search:    strings.ToLower(folder + " " + meta),
				})
			}
			idx, err := pickListIndex(in, lineReader, out, pickerConfig{
				Title:       "Select Folder",
				Prompt:      "Select folder",
				Placeholder: "Search folders...",
				Options:     folderOptions,
			})
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

	now := time.Now()
	sessionOptions := make([]pickerOption, 0, len(candidates))
	for _, s := range candidates {
		meta := sessionMetaLine(s, now)
		sessionOptions = append(sessionOptions, pickerOption{
			Primary:   formatDisplayTitle(s.Title, 140),
			Secondary: meta,
			Search: strings.ToLower(strings.Join([]string{
				s.Title, s.ID, s.CWD, s.GitBranch, meta,
			}, " ")),
		})
	}
	idx, err := pickListIndex(in, lineReader, out, pickerConfig{
		Title:       "Resume Session",
		Prompt:      "Select session",
		Placeholder: "Search...",
		Options:     sessionOptions,
	})
	if err != nil {
		return session.SourceSession{}, err
	}
	return candidates[idx], nil
}

func pickListIndex(in io.Reader, lineReader *bufio.Reader, out io.Writer, cfg pickerConfig) (int, error) {
	if len(cfg.Options) == 0 {
		return 0, ErrNoSourceSessions
	}
	if len(cfg.Options) == 1 {
		return 0, nil
	}
	if canUseBubbleTeaPicker(in, out) {
		return runBubbleTeaPicker(in, out, cfg)
	}
	return runNumberedPicker(lineReader, out, cfg)
}

func runNumberedPicker(r *bufio.Reader, out io.Writer, cfg pickerConfig) (int, error) {
	fmt.Fprintf(out, "%s:\n", cfg.Prompt)
	for i, option := range cfg.Options {
		line := option.Primary
		if option.Secondary != "" {
			line += " • " + option.Secondary
		}
		fmt.Fprintf(out, "  %d) %s\n", i+1, line)
	}
	idx, err := promptNumber(r, out, cfg.Prompt, len(cfg.Options))
	if err != nil {
		return 0, err
	}
	return idx - 1, nil
}

func runBubbleTeaPicker(in io.Reader, out io.Writer, cfg pickerConfig) (int, error) {
	inFile := in.(*os.File)
	outFile := out.(*os.File)

	model := newPickerModel(cfg)
	program := tea.NewProgram(model, tea.WithInput(inFile), tea.WithOutput(outFile), tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return 0, err
	}
	m, ok := finalModel.(pickerModel)
	if !ok {
		return 0, fmt.Errorf("unexpected picker model result: %T", finalModel)
	}
	if m.canceled {
		return 0, errSelectionCanceled
	}
	if m.selected < 0 {
		return 0, errSelectionCanceled
	}
	return m.selected, nil
}

func canUseBubbleTeaPicker(in io.Reader, out io.Writer) bool {
	inFile, ok := in.(*os.File)
	if !ok {
		return false
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return false
	}
	term := strings.TrimSpace(strings.ToLower(os.Getenv("TERM")))
	if term == "" || term == "dumb" {
		return false
	}
	return isatty.IsTerminal(inFile.Fd()) && isatty.IsTerminal(outFile.Fd())
}

type pickerModel struct {
	title string

	search textinput.Model
	all    []pickerOption

	filtered []int
	cursor   int
	offset   int

	width  int
	height int

	selected int
	canceled bool
}

func newPickerModel(cfg pickerConfig) pickerModel {
	input := textinput.New()
	input.Placeholder = cfg.Placeholder
	input.CharLimit = 200
	input.Prompt = "⌕ "
	input.Focus()

	m := pickerModel{
		title:    cfg.Title,
		search:   input,
		all:      cfg.Options,
		selected: -1,
	}
	m.applyFilter()
	return m
}

func (m pickerModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ensureCursorVisible()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			if len(m.filtered) > 0 {
				m.selected = m.filtered[m.cursor]
				return m, tea.Quit
			}
			return m, nil
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.ensureCursorVisible()
			}
			return m, nil
		case "down", "j":
			if m.cursor+1 < len(m.filtered) {
				m.cursor++
				m.ensureCursorVisible()
			}
			return m, nil
		case "pgup":
			step := max(1, m.visibleEntries()/2)
			m.cursor -= step
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.ensureCursorVisible()
			return m, nil
		case "pgdown":
			step := max(1, m.visibleEntries()/2)
			m.cursor += step
			if m.cursor >= len(m.filtered) {
				m.cursor = len(m.filtered) - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
			m.ensureCursorVisible()
			return m, nil
		}
	}

	prev := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != prev {
		m.applyFilter()
	}
	return m, cmd
}

func (m pickerModel) View() string {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	selectedTitleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	selectedMarkerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	selectedRowStyle := lipgloss.NewStyle().Background(lipgloss.Color("236"))
	titleStyle := lipgloss.NewStyle().Bold(true)

	width := m.width
	if width <= 0 {
		width = 120
	}
	contentWidth := width - 4
	if contentWidth < 30 {
		contentWidth = 30
	}

	position := 0
	if len(m.filtered) > 0 {
		position = m.cursor + 1
	}
	header := headerStyle.Render(fmt.Sprintf("%s (%d of %d)", m.title, position, len(m.filtered)))

	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Width(contentWidth).
		Render(m.search.View())

	var body strings.Builder
	entries := m.visibleEntries()
	if entries <= 0 {
		entries = 5
	}

	if len(m.filtered) == 0 {
		body.WriteString(subtleStyle.Render("No sessions match your search."))
	} else {
		start := m.offset
		end := min(len(m.filtered), start+entries)
		for i := start; i < end; i++ {
			option := m.all[m.filtered[i]]
			marker := "  "
			primaryText := truncateToWidth(option.Primary, contentWidth-6)
			primary := titleStyle.Render(primaryText)
			if i == m.cursor {
				marker = selectedMarkerStyle.Render("❯ ")
				primary = selectedRowStyle.Render(selectedTitleStyle.Render(primaryText))
			}
			body.WriteString(marker + primary + "\n")
			if option.Secondary != "" {
				secondaryText := truncateToWidth(option.Secondary, contentWidth-6)
				secondary := subtleStyle.Render(secondaryText)
				if i == m.cursor {
					secondary = selectedRowStyle.Render(subtleStyle.Render(secondaryText))
				}
				body.WriteString("  " + secondary + "\n")
			}
		}
	}

	footer := subtleStyle.Render("↑/↓ move • Enter select • Esc cancel")
	return strings.Join([]string{
		header,
		searchBox,
		"",
		strings.TrimRight(body.String(), "\n"),
		"",
		footer,
	}, "\n")
}

func (m *pickerModel) applyFilter() {
	query := strings.ToLower(strings.TrimSpace(m.search.Value()))
	filtered := make([]int, 0, len(m.all))
	for i, option := range m.all {
		if query == "" || strings.Contains(option.Search, query) || strings.Contains(strings.ToLower(option.Primary), query) {
			filtered = append(filtered, i)
		}
	}
	m.filtered = filtered
	if len(m.filtered) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureCursorVisible()
}

func (m *pickerModel) visibleEntries() int {
	if m.height <= 0 {
		return 6
	}
	// Header + search box + spacing + footer ~= 7 rows.
	usableLines := m.height - 7
	if usableLines < 2 {
		return 1
	}
	// Two lines per entry: title + metadata.
	return max(1, usableLines/2)
}

func (m *pickerModel) ensureCursorVisible() {
	if len(m.filtered) == 0 {
		m.offset = 0
		return
	}
	entries := m.visibleEntries()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+entries {
		m.offset = m.cursor - entries + 1
	}
	maxOffset := len(m.filtered) - entries
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func sessionMetaLine(s session.SourceSession, now time.Time) string {
	parts := make([]string, 0, 3)
	if rel := formatRelativeTime(s.UpdatedAt, now); rel != "" {
		parts = append(parts, rel)
	}
	if branch := strings.TrimSpace(s.GitBranch); branch != "" {
		parts = append(parts, branch)
	}
	if sz := formatSizeBytes(s.SizeBytes); sz != "" {
		parts = append(parts, sz)
	}
	return strings.Join(parts, " • ")
}

func formatRelativeTime(ts, now time.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := now.Sub(ts)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

func formatSizeBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%dKB", (n+512)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(n)/float64(1024*1024))
	}
	return fmt.Sprintf("%.1fGB", float64(n)/float64(1024*1024*1024))
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
		s.GitBranch = normalizeDisplayWhitespace(s.GitBranch)
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

func truncateToWidth(s string, max int) string {
	s = normalizeDisplayWhitespace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
