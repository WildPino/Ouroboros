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
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// Process exit codes (DESIGN.md §9).
const (
	exitOK       = 0
	exitError    = 1
	exitConfig   = 2
	exitWarnings = 3
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout, stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	code := exitCode(err)
	var ce *codedError
	if !errors.As(err, &ce) || ce.err != nil {
		fmt.Fprintln(stderr, "error:", err)
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
