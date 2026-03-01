package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrUsage = errors.New("usage")

// UsageError marks user input errors that should exit with code 2.
type UsageError struct {
	Msg string
}

func (e UsageError) Error() string {
	return e.Msg
}

func (e UsageError) Unwrap() error {
	return ErrUsage
}

// Options are validated CLI options for the POC.
type Options struct {
	From         string
	To           string
	ID           string
	Interactive  bool
	ClaudeHome   string
	CodexHome    string
	SourceFolder string
	CWD          string
	Title        string
	DryRun       bool
	ShowHelp     bool
	ShowVersion  bool
}

func NewFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func Parse(args []string) (Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Options{}, fmt.Errorf("resolve home dir: %w", err)
	}

	opts := Options{}
	fs := NewFlagSet()

	fs.StringVar(&opts.From, "from", "", "source tool (required)")
	fs.StringVar(&opts.To, "to", "", "target tool (required)")
	fs.StringVar(&opts.ID, "id", "", "source session id")
	fs.BoolVar(&opts.Interactive, "interactive", false, "interactively select source session when --id is not provided")
	fs.StringVar(&opts.ClaudeHome, "claude-home", filepath.Join(home, ".claude"), "Claude home directory")
	fs.StringVar(&opts.CodexHome, "codex-home", filepath.Join(home, ".codex"), "Codex home directory")
	fs.StringVar(&opts.SourceFolder, "source-folder", "", "source folder filter for interactive selection")
	fs.StringVar(&opts.CWD, "cwd", "", "target cwd override")
	fs.StringVar(&opts.Title, "title", "", "thread title override")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "run conversion without writing native stores")
	fs.BoolVar(&opts.ShowVersion, "version", false, "print version")
	fs.BoolVar(&opts.ShowHelp, "help", false, "print help")

	if err := fs.Parse(args); err != nil {
		return Options{}, UsageError{Msg: err.Error()}
	}
	if extras := fs.Args(); len(extras) > 0 {
		return Options{}, UsageError{Msg: fmt.Sprintf("unexpected arguments: %s", strings.Join(extras, " "))}
	}
	if opts.ShowHelp || opts.ShowVersion {
		return opts, nil
	}

	opts.From = strings.TrimSpace(strings.ToLower(opts.From))
	opts.To = strings.TrimSpace(strings.ToLower(opts.To))
	opts.ID = strings.TrimSpace(opts.ID)
	opts.SourceFolder = strings.TrimSpace(opts.SourceFolder)

	if opts.From == "" || opts.To == "" {
		return Options{}, UsageError{Msg: "missing required flags: --from, --to"}
	}
	if !isSupportedDirection(opts.From, opts.To) {
		return Options{}, UsageError{Msg: "supported directions: --from claude --to codex OR --from codex --to claude"}
	}
	// Interactive selection is now the default path when --id is not provided.
	if opts.ID == "" {
		opts.Interactive = true
	}

	opts.ClaudeHome = expandHome(opts.ClaudeHome, home)
	opts.CodexHome = expandHome(opts.CodexHome, home)
	opts.SourceFolder = expandHome(opts.SourceFolder, home)
	opts.CWD = strings.TrimSpace(opts.CWD)
	opts.Title = strings.TrimSpace(opts.Title)

	return opts, nil
}

func expandHome(path, home string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func isSupportedDirection(from, to string) bool {
	return (from == "claude" && to == "codex") || (from == "codex" && to == "claude")
}
