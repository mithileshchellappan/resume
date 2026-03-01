package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mithileshchellappan/resume/internal/claude"
	"github.com/mithileshchellappan/resume/internal/cli"
	"github.com/mithileshchellappan/resume/internal/codex"
	"github.com/mithileshchellappan/resume/internal/converter"
	"github.com/mithileshchellappan/resume/internal/session"
)

const (
	ExitOK         = 0
	ExitUsage      = 2
	ExitConversion = 3
	ExitWrite      = 4
)

// Result is the successful conversion output.
type Result struct {
	ThreadID    string `json:"thread_id"`
	RolloutPath string `json:"rollout_path"`
	DryRun      bool   `json:"dry_run"`
}

// CodedError maps failures to required CLI exit codes.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *CodedError) Unwrap() error {
	return e.Err
}

func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	if errors.Is(err, cli.ErrUsage) {
		return ExitUsage
	}
	return 1
}

// Deps allows tests to inject loader/converter/writer.
type Deps struct {
	Loader    session.ClaudeLoader
	Converter session.Converter
	Writer    session.CodexWriter
	Now       func() time.Time
}

func Run(ctx context.Context, opts cli.Options, cliVersion string) (Result, error) {
	deps := Deps{
		Loader:    claude.NewLoader(opts.ClaudeHome),
		Converter: converter.New(),
		Writer:    codex.NewWriter(opts.CodexHome),
		Now:       func() time.Time { return time.Now().UTC() },
	}
	return runWithDeps(ctx, opts, cliVersion, deps)
}

func runWithDeps(ctx context.Context, opts cli.Options, cliVersion string, deps Deps) (Result, error) {
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	ir, err := deps.Loader.LoadBySessionID(ctx, opts.ID)
	if err != nil {
		return Result{}, &CodedError{Code: ExitConversion, Err: fmt.Errorf("load claude session: %w", err)}
	}

	converted, err := deps.Converter.Convert(ctx, ir)
	if err != nil {
		return Result{}, &CodedError{Code: ExitConversion, Err: fmt.Errorf("convert session: %w", err)}
	}

	meta := session.CodexThreadMeta{
		CWD:               choose(opts.CWD, converted.CWD, ir.CWD),
		Title:             strings.TrimSpace(opts.Title),
		CLIVersion:        cliVersion,
		ApprovalMode:      "on-request",
		SandboxPolicyJSON: "",
		FirstUserMessage:  converted.FirstUserMessage,
	}

	if opts.DryRun {
		now := deps.Now().UTC()
		threadID := uuid.NewString()
		previewPath := fmt.Sprintf("%s/sessions/%s/%s/%s/rollout-%s-%s.jsonl",
			strings.TrimRight(opts.CodexHome, "/"),
			now.Format("2006"),
			now.Format("01"),
			now.Format("02"),
			now.Format("2006-01-02T15-04-05"),
			threadID,
		)
		return Result{ThreadID: threadID, RolloutPath: previewPath, DryRun: true}, nil
	}

	threadID, rolloutPath, err := deps.Writer.Write(ctx, converted, meta)
	if err != nil {
		return Result{}, &CodedError{Code: ExitWrite, Err: fmt.Errorf("persist codex session: %w", err)}
	}

	return Result{ThreadID: threadID, RolloutPath: rolloutPath, DryRun: false}, nil
}

func choose(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}
