// Command ouroboros keeps space-aware, versioned restic backups on Windows.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/setup"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// Process exit codes (DESIGN.md §9).
const (
	exitOK       = 0
	exitError    = 1
	exitConfig   = 2
	exitWarnings = 3
	// exitCannotReclaim: a backup was skipped because not enough space
	// could be freed without touching protected snapshots.
	exitCannotReclaim = 4
)

// codedError carries a specific exit code. A nil err with a code means the
// command already reported its outcome and only the code must be returned.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *codedError) Unwrap() error { return e.err }

func main() {
	// An update leaves the replaced ouroboros.exe as ouroboros.exe.old when
	// it was running (DESIGN.md §12.8, question 4).
	if exe, err := os.Executable(); err == nil {
		setup.CleanOld(exe)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root, opts := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	code := exitCode(err)
	var ce *codedError
	reported := errors.As(err, &ce) && ce.err == nil
	if !reported {
		fmt.Fprintln(stderr, "error:", err)
	}
	if opts.json {
		// Errors from flag parsing or a refused --json come before the
		// command could set up its output.
		if opts.out == nil {
			opts.out = &jsonLines{w: stdout, command: "ouroboros"}
		}
		opts.out.fail(err)
	}
	return code
}

func exitCode(err error) int {
	var ce *codedError
	switch {
	case errors.As(err, &ce):
		return ce.code
	case errors.Is(err, config.ErrInvalid):
		return exitConfig
	default:
		return exitError
	}
}
