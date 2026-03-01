package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mithileshchellappan/resume/internal/app"
	"github.com/mithileshchellappan/resume/internal/buildinfo"
	"github.com/mithileshchellappan/resume/internal/claude"
	"github.com/mithileshchellappan/resume/internal/cli"
	"github.com/mithileshchellappan/resume/internal/codex"
	"github.com/mithileshchellappan/resume/internal/session"
)

var lookPath = exec.LookPath

var supportedTools = []string{"claude", "codex"}

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
	if err := resolveToolsFromFlagsOrPicker(&opts, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n\n", err)
		printUsage(os.Stderr)
		return app.ExitCode(err)
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

func resolveToolsFromFlagsOrPicker(opts *cli.Options, in io.Reader, out io.Writer) error {
	installed := detectInstalledTools(lookPath)
	return resolveToolsWithPicker(opts, installed, in, out, cli.SelectToolInteractive)
}

type toolPickerFunc func(in io.Reader, out io.Writer, title string, tools []string) (string, error)

func resolveToolsWithPicker(opts *cli.Options, installed []string, in io.Reader, out io.Writer, picker toolPickerFunc) error {
	opts.From = strings.TrimSpace(strings.ToLower(opts.From))
	opts.To = strings.TrimSpace(strings.ToLower(opts.To))
	if opts.From != "" && opts.To != "" {
		return nil
	}

	installed = normalizeInstalledTools(installed)
	if len(installed) == 0 {
		return cli.UsageError{Msg: "no supported tools found in PATH (expected: claude, codex)"}
	}
	if opts.From != "" && !containsTool(installed, opts.From) {
		return cli.UsageError{Msg: fmt.Sprintf("source tool %q not found in PATH", opts.From)}
	}
	if opts.To != "" && !containsTool(installed, opts.To) {
		return cli.UsageError{Msg: fmt.Sprintf("target tool %q not found in PATH", opts.To)}
	}

	if opts.From == "" {
		candidates := append([]string(nil), installed...)
		if opts.To != "" {
			candidates = excludeTool(candidates, opts.To)
		}
		if len(candidates) == 0 {
			return cli.UsageError{Msg: fmt.Sprintf("no source tool available in PATH for target %q", opts.To)}
		}
		picked, err := picker(in, out, "Select Source Tool", candidates)
		if err != nil {
			return cli.UsageError{Msg: fmt.Sprintf("select source tool: %v", err)}
		}
		opts.From = picked
	}

	if opts.To == "" {
		candidates := excludeTool(installed, opts.From)
		if len(candidates) == 0 {
			return cli.UsageError{Msg: fmt.Sprintf("no target tool available in PATH for source %q", opts.From)}
		}
		picked, err := picker(in, out, "Select Target Tool", candidates)
		if err != nil {
			return cli.UsageError{Msg: fmt.Sprintf("select target tool: %v", err)}
		}
		opts.To = picked
	}

	if opts.From == opts.To {
		return cli.UsageError{Msg: "source and target tools must be different"}
	}
	if !((opts.From == "claude" && opts.To == "codex") || (opts.From == "codex" && opts.To == "claude")) {
		return cli.UsageError{Msg: "supported directions: --from claude --to codex OR --from codex --to claude"}
	}
	return nil
}

func detectInstalledTools(lookPathFn func(file string) (string, error)) []string {
	tools := make([]string, 0, len(supportedTools))
	for _, tool := range supportedTools {
		if _, err := lookPathFn(tool); err == nil {
			tools = append(tools, tool)
		}
	}
	return tools
}

func normalizeInstalledTools(in []string) []string {
	seen := map[string]bool{}
	for _, raw := range in {
		tool := strings.TrimSpace(strings.ToLower(raw))
		if tool == "" {
			continue
		}
		seen[tool] = true
	}
	out := make([]string, 0, len(supportedTools))
	for _, tool := range supportedTools {
		if seen[tool] {
			out = append(out, tool)
		}
	}
	return out
}

func containsTool(tools []string, target string) bool {
	for _, tool := range tools {
		if tool == target {
			return true
		}
	}
	return false
}

func excludeTool(tools []string, excluded string) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == excluded {
			continue
		}
		out = append(out, tool)
	}
	return out
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
	fmt.Fprintln(w, "  resume [--from <claude|codex>] [--to <codex|claude>] [--id <source_id>] [--source-folder <path>] [--claude-home <path>] [--codex-home <path>] [--cwd <target_cwd>] [--title <target_title>] [--dry-run]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --from         Source tool (optional; picker shown when omitted)")
	fmt.Fprintln(w, "  --to           Target tool (optional; picker shown when omitted)")
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
