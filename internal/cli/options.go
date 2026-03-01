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
	From        string
	To          string
	ID          string
	ClaudeHome  string
	CodexHome   string
	CWD         string
	Title       string
	DryRun      bool
	ShowHelp    bool
	ShowVersion bool
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

	fs.StringVar(&opts.From, "from", "", "source tool (required: claude)")
	fs.StringVar(&opts.To, "to", "", "target tool (required: codex)")
	fs.StringVar(&opts.ID, "id", "", "source session id (required)")
	fs.StringVar(&opts.ClaudeHome, "claude-home", filepath.Join(home, ".claude"), "Claude home directory")
	fs.StringVar(&opts.CodexHome, "codex-home", filepath.Join(home, ".codex"), "Codex home directory")
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

	if opts.From == "" || opts.To == "" || opts.ID == "" {
		return Options{}, UsageError{Msg: "missing required flags: --from, --to, --id"}
	}
	if opts.From != "claude" || opts.To != "codex" {
		return Options{}, UsageError{Msg: "POC currently supports only --from claude --to codex"}
	}

	opts.ClaudeHome = expandHome(opts.ClaudeHome, home)
	opts.CodexHome = expandHome(opts.CodexHome, home)
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
