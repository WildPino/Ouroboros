package restic

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// stderrLimit bounds the stderr kept in memory; only the tail matters for
// error messages, and a backup with many unreadable files can print a lot.
const stderrLimit = 256 << 10

// waitDelay is how long Wait waits for output pipes after the process was
// killed, in case a grandchild still holds them open.
const waitDelay = 10 * time.Second

// runner starts restic processes.
type runner struct {
	exe      string
	global   []string // arguments placed before every subcommand
	priority string   // config.Priority* value
}

// call is one restic invocation.
type call struct {
	args    []string // subcommand and its arguments; args[0] names the command
	timeout time.Duration
	// onLine, if set, receives each stdout line (without the line ending)
	// as it arrives; otherwise stdout is returned in full.
	onLine func(line []byte) error
}

type output struct {
	stdout []byte
	stderr []byte
}

func (r *runner) run(ctx context.Context, c call) (output, error) {
	parent := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	args := append(slices.Clone(r.global), c.args...)
	cmd := exec.CommandContext(ctx, r.exe, args...)
	cmd.Env = resticEnv(os.Environ())
	cmd.SysProcAttr = sysProcAttr(r.priority)
	cmd.WaitDelay = waitDelay
	stderr := &tailBuffer{limit: stderrLimit}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return output{}, err
	}
	if err := cmd.Start(); err != nil {
		return output{}, fmt.Errorf("start restic: %w", err)
	}
	if err := afterStart(cmd.Process); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return output{}, err
	}

	var out output
	readErr := readLines(stdout, c.onLine, &out.stdout)
	if readErr != nil {
		// Stop restic if the caller rejected its output.
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	out.stderr = stderr.Bytes()

	name := ""
	if len(c.args) > 0 {
		name = c.args[0]
	}
	switch {
	case parent.Err() != nil:
		return out, fmt.Errorf("restic %s: %w", name, parent.Err())
	case ctx.Err() != nil:
		return out, fmt.Errorf("restic %s: %w after %s", name, ErrTimeout, c.timeout)
	case readErr != nil:
		return out, fmt.Errorf("restic %s: %w", name, readErr)
	case waitErr != nil:
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			return out, &ExitError{Command: name, Code: ee.ExitCode(), Message: parseStderr(out.stderr).message}
		}
		return out, fmt.Errorf("restic %s: %w", name, waitErr)
	}
	return out, nil
}

// readLines feeds stdout to onLine line by line, or collects it into all when
// onLine is nil. It always drains the reader so that restic never blocks.
func readLines(r io.Reader, onLine func([]byte) error, all *[]byte) error {
	if onLine == nil {
		b, err := io.ReadAll(r)
		*all = b
		return err
	}
	br := bufio.NewReaderSize(r, 64<<10)
	var firstErr error
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimRight(line, "\r\n")
		if len(line) > 0 && firstErr == nil {
			firstErr = onLine(line)
		}
		if err == io.EOF {
			return firstErr
		}
		if err != nil {
			return err
		}
	}
}

// resticEnv removes RESTIC_* variables from the inherited environment, so
// that a stray RESTIC_PASSWORD or RESTIC_REPOSITORY on the machine can never
// override the explicit flags.
func resticEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "RESTIC_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.limit; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }
