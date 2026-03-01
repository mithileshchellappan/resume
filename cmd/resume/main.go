package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/mithileshchellappan/resume/internal/app"
	"github.com/mithileshchellappan/resume/internal/buildinfo"
	"github.com/mithileshchellappan/resume/internal/claude"
	"github.com/mithileshchellappan/resume/internal/cli"
	"github.com/mithileshchellappan/resume/internal/codex"
	"github.com/mithileshchellappan/resume/internal/session"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
		printUsage(os.Stderr)
		return app.ExitCode(err)
	}

	if opts.ShowHelp {
		printUsage(os.Stdout)
		return app.ExitOK
	}
	if opts.ShowVersion {
		fmt.Fprintln(os.Stdout, buildinfo.Version)
		return app.ExitOK
	}

	selectedViaPicker := false
	if opts.ID == "" {
		sessions, err := listSourceSessions(context.Background(), opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: list source sessions: %v\n", err)
			return app.ExitConversion
		}

		folderHint := opts.SourceFolder
		if folderHint == "" {
			if wd, wdErr := os.Getwd(); wdErr == nil {
				folderHint = wd
			}
		}
		picked, err := cli.SelectSessionInteractive(os.Stdin, os.Stdout, sessions, folderHint)
		if err != nil {
			if errors.Is(err, cli.ErrNoSourceSessions) {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return app.ExitConversion
			}
			fmt.Fprintf(os.Stderr, "error: interactive selection failed: %v\n", err)
			return app.ExitUsage
		}
		opts.ID = picked.ID
		selectedViaPicker = true
		fmt.Fprintf(os.Stdout, "selected source session: %s [%s]\n", picked.Title, picked.ID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := app.Run(ctx, opts, buildinfo.Version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return app.ExitCode(err)
	}

	b, _ := json.Marshal(result)
	fmt.Fprintln(os.Stdout, string(b))
	if result.SessionID != "" {
		fmt.Fprintf(os.Stdout, "session created: %s\nsession path: %s\n", result.SessionID, result.SessionPath)
	} else {
		fmt.Fprintf(os.Stdout, "session created: %s\nrollout: %s\n", result.ThreadID, result.RolloutPath)
	}

	if selectedViaPicker && !result.DryRun {
		if openErr := launchTargetResume(opts.To, result); openErr != nil {
			fmt.Fprintf(os.Stderr, "warning: migrated session created but failed to open target CLI: %v\n", openErr)
		}
	}
	return app.ExitOK
}

func listSourceSessions(ctx context.Context, opts cli.Options) ([]session.SourceSession, error) {
	switch opts.From {
	case "claude":
		return claude.NewLoader(opts.ClaudeHome).ListSessions(ctx)
	case "codex":
		return codex.NewLoader(opts.CodexHome).ListSessions(ctx)
	default:
		return nil, fmt.Errorf("unsupported source tool: %s", opts.From)
	}
}

func launchTargetResume(target string, result app.Result) error {
	cmd := buildTargetResumeCommand(target, result)
	if cmd == nil {
		return nil
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func buildTargetResumeCommand(target string, result app.Result) *exec.Cmd {
	switch target {
	case "codex":
		if result.ThreadID == "" {
			return nil
		}
		return exec.Command("codex", "resume", result.ThreadID)
	case "claude":
		if result.SessionID == "" {
			return nil
		}
		return exec.Command("claude", "--resume", result.SessionID)
	default:
		return nil
	}
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  resume --from claude --to codex --id <claude_session_id> [--claude-home <path>] [--codex-home <path>] [--cwd <target_cwd>] [--title <thread_title>] [--dry-run]")
	fmt.Fprintln(w, "  resume --from codex --to claude --id <codex_thread_id> [--claude-home <path>] [--codex-home <path>] [--cwd <target_cwd>] [--title <session_title>] [--dry-run]")
	fmt.Fprintln(w, "  resume --from <claude|codex> --to <codex|claude> [--source-folder <path>] [--claude-home <path>] [--codex-home <path>] [--cwd <target_cwd>] [--title <target_title>] [--dry-run]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --from         Source tool (required)")
	fmt.Fprintln(w, "  --to           Target tool (required)")
	fmt.Fprintln(w, "  --id           Source session/thread id (optional; when omitted, interactive picker is used)")
	fmt.Fprintln(w, "  --interactive  Force interactive source session picker (kept for compatibility; use ↑/↓ + Enter)")
	fmt.Fprintln(w, "  --source-folder  Source folder filter for interactive picker (defaults to current directory)")
	fmt.Fprintln(w, "  --claude-home  Claude home directory (default ~/.claude)")
	fmt.Fprintln(w, "  --codex-home   Codex home directory (default ~/.codex)")
	fmt.Fprintln(w, "  --cwd          Override target cwd")
	fmt.Fprintln(w, "  --title        Override thread title")
	fmt.Fprintln(w, "  --dry-run      Convert only; do not write native stores")
	fmt.Fprintln(w, "  --version      Print version")
	fmt.Fprintln(w, "  --help         Print help")
}
