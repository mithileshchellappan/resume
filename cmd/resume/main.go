package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mithileshchellappan/resume/internal/app"
	"github.com/mithileshchellappan/resume/internal/buildinfo"
	"github.com/mithileshchellappan/resume/internal/cli"
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
	return app.ExitOK
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  resume --from claude --to codex --id <claude_session_id> [--claude-home <path>] [--codex-home <path>] [--cwd <target_cwd>] [--title <thread_title>] [--dry-run]")
	fmt.Fprintln(w, "  resume --from codex --to claude --id <codex_thread_id> [--claude-home <path>] [--codex-home <path>] [--cwd <target_cwd>] [--title <session_title>] [--dry-run]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --from         Source tool (required)")
	fmt.Fprintln(w, "  --to           Target tool (required)")
	fmt.Fprintln(w, "  --id           Source session/thread id (required)")
	fmt.Fprintln(w, "  --claude-home  Claude home directory (default ~/.claude)")
	fmt.Fprintln(w, "  --codex-home   Codex home directory (default ~/.codex)")
	fmt.Fprintln(w, "  --cwd          Override target cwd")
	fmt.Fprintln(w, "  --title        Override thread title")
	fmt.Fprintln(w, "  --dry-run      Convert only; do not write native stores")
	fmt.Fprintln(w, "  --version      Print version")
	fmt.Fprintln(w, "  --help         Print help")
}
