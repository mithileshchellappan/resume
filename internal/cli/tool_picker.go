package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ErrNoToolsFound = errors.New("no supported tools found")

func SelectToolInteractive(in io.Reader, out io.Writer, title string, tools []string) (string, error) {
	options := normalizeToolOptions(tools)
	if len(options) == 0 {
		return "", ErrNoToolsFound
	}
	if len(options) == 1 {
		return options[0], nil
	}

	lineReader := bufio.NewReader(in)
	if canUseBubbleTeaPicker(in, out) {
		idx, err := runToolBubbleTeaPicker(in, out, title, options)
		if err != nil {
			return "", err
		}
		return options[idx], nil
	}

	idx, err := runToolNumberedPicker(lineReader, out, title, options)
	if err != nil {
		return "", err
	}
	return options[idx], nil
}

func normalizeToolOptions(tools []string) []string {
	out := make([]string, 0, len(tools))
	seen := map[string]bool{}
	for _, raw := range tools {
		tool := strings.TrimSpace(strings.ToLower(raw))
		if tool == "" || seen[tool] {
			continue
		}
		out = append(out, tool)
		seen[tool] = true
	}
	return out
}

func runToolNumberedPicker(r *bufio.Reader, out io.Writer, title string, tools []string) (int, error) {
	fmt.Fprintf(out, "%s:\n", title)
	for i, tool := range tools {
		fmt.Fprintf(out, "  %d) %s\n", i+1, formatToolLabel(tool))
	}
	idx, err := promptNumber(r, out, title, len(tools))
	if err != nil {
		return 0, err
	}
	return idx - 1, nil
}

func runToolBubbleTeaPicker(in io.Reader, out io.Writer, title string, tools []string) (int, error) {
	inFile := in.(*os.File)
	outFile := out.(*os.File)

	model := newToolPickerModel(title, tools)
	program := tea.NewProgram(model, tea.WithInput(inFile), tea.WithOutput(outFile), tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return 0, err
	}
	m, ok := finalModel.(toolPickerModel)
	if !ok {
		return 0, fmt.Errorf("unexpected tool picker model result: %T", finalModel)
	}
	if m.canceled || m.selected < 0 {
		return 0, errSelectionCanceled
	}
	return m.selected, nil
}

type toolPickerModel struct {
	title    string
	tools    []string
	cursor   int
	selected int
	canceled bool
	width    int
}

func newToolPickerModel(title string, tools []string) toolPickerModel {
	return toolPickerModel{
		title:    strings.TrimSpace(title),
		tools:    append([]string(nil), tools...),
		selected: -1,
	}
}

func (m toolPickerModel) Init() tea.Cmd {
	return nil
}

func (m toolPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			if len(m.tools) == 0 {
				return m, nil
			}
			m.selected = m.cursor
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "down", "j":
			if m.cursor+1 < len(m.tools) {
				m.cursor++
			}
			return m, nil
		}
	}
	return m, nil
}

func (m toolPickerModel) View() string {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	titleStyle := lipgloss.NewStyle().Bold(true)
	subtleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	markerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("117"))
	selectedRowStyle := lipgloss.NewStyle().Background(lipgloss.Color("236"))

	width := m.width
	if width <= 0 {
		width = 80
	}
	contentWidth := width - 4
	if contentWidth < 24 {
		contentWidth = 24
	}

	header := headerStyle.Render(m.title)

	var body strings.Builder
	for i, tool := range m.tools {
		name := truncateToWidth(formatToolLabel(tool), contentWidth-4)
		marker := "  "
		row := titleStyle.Render(name)
		if i == m.cursor {
			marker = markerStyle.Render("❯ ")
			row = selectedRowStyle.Render(titleStyle.Render(name))
		}
		body.WriteString(marker + row + "\n")
	}

	footer := subtleStyle.Render("↑/↓ move • Enter select • Esc cancel")
	return strings.Join([]string{
		header,
		"",
		strings.TrimRight(body.String(), "\n"),
		"",
		footer,
	}, "\n")
}

func formatToolLabel(tool string) string {
	switch strings.TrimSpace(strings.ToLower(tool)) {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	default:
		return tool
	}
}
