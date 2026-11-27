package gui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ErrNotInstalled reports that the command line is not installed, so no
// repository action can run.
var ErrNotInstalled = errors.New("the command line is not installed")

// CLIError is the error line of a command run with --json.
type CLIError struct {
	Code    string
	Message string
}

func (e *CLIError) Error() string { return e.Message }

// Runner runs the installed command line with --json (docs/JSON.md). The
// arguments are built by the bridge, never taken from the page.
type Runner interface {
	// Run runs a command and waits for its final line. Event lines go to
	// onEvent, which may be nil. stdin may be nil.
	Run(ctx context.Context, args []string, stdin io.Reader, onEvent func(name string, data json.RawMessage)) (json.RawMessage, error)
	// Start starts a command that keeps running if the window closes, and
	// calls done with its final line if the window is still open.
	Start(args []string, done func(json.RawMessage, error)) error
}

// maxLine bounds one line of output; a snapshot list of a large repository
// is the longest.
const maxLine = 64 << 20

type line struct {
	Type  string          `json:"type"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// readLines reads the JSON lines of one command until its final line.
// It returns the data of a result line, or a *CLIError for an error line.
func readLines(r io.Reader, onEvent func(string, json.RawMessage)) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		text := bytes.TrimSpace(sc.Bytes())
		if len(text) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(text, &l); err != nil {
			return nil, fmt.Errorf("unreadable output from the command line: %w", err)
		}
		switch l.Type {
		case "event":
			if onEvent != nil {
				onEvent(l.Event, l.Data)
			}
		case "result":
			if len(l.Data) == 0 {
				return json.RawMessage("null"), nil
			}
			return l.Data, nil
		case "error":
			if l.Error == nil {
				return nil, &CLIError{Code: "failed", Message: "the command line failed without a message"}
			}
			return nil, &CLIError{Code: l.Error.Code, Message: l.Error.Message}
		default:
			return nil, fmt.Errorf("unexpected line type %q from the command line", l.Type)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading the command line output: %w", err)
	}
	return nil, errors.New("the command line ended without a result")
}

// ExecRunner runs the ouroboros.exe at Path.
type ExecRunner struct {
	Path string
}

func (x ExecRunner) command(ctx context.Context, args []string) (*exec.Cmd, error) {
	if fi, err := os.Stat(x.Path); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is missing", ErrNotInstalled, x.Path)
	}
	cmd := exec.CommandContext(ctx, x.Path, args...)
	cmd.SysProcAttr = sysProcAttr()
	return cmd, nil
}

// tail keeps the end of what a command writes to standard error, for the
// message when it ends without a final line.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func (x ExecRunner) Run(ctx context.Context, args []string, stdin io.Reader, onEvent func(string, json.RawMessage)) (json.RawMessage, error) {
	cmd, err := x.command(ctx, args)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = stdin
	var stderr tail
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := readLines(out, onEvent)
	_, _ = io.Copy(io.Discard, out)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil {
		var c *CLIError
		if !errors.As(readErr, &c) && waitErr != nil && stderr.String() != "" {
			return nil, fmt.Errorf("%w: %s", readErr, stderr.String())
		}
		return nil, readErr
	}
	return data, nil
}

func (x ExecRunner) Start(args []string, done func(json.RawMessage, error)) error {
	cmd, err := x.command(context.Background(), args)
	if err != nil {
		return err
	}
	cmd.SysProcAttr = detachedProcAttr()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		// The command ignores write errors on its output, so it keeps
		// running when the window closes and the pipe breaks.
		data, err := readLines(out, nil)
		_, _ = io.Copy(io.Discard, out)
		_ = cmd.Wait()
		if done != nil {
			done(data, err)
		}
	}()
	return nil
}
