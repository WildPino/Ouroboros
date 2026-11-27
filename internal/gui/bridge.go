// Package gui is the window of Ouroboros.exe: the embedded web page, the
// WebView2 host that shows it, and the bridge, the only way the page reaches
// Go (docs/DESIGN.md §12.3 and §12.8, question 7).
package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Error codes of the bridge contract.
const (
	CodeInvalidArgument       = "invalid_argument"
	CodeLocked                = "locked"
	CodeNotInstalled          = "not_installed"
	CodeWrongPassword         = "wrong_password"
	CodeRepositoryUnavailable = "repository_unavailable"
	CodeValidation            = "validation"
	CodeCancelled             = "cancelled"
	CodeFailed                = "failed"
)

// maxMessage bounds a message from the page; the largest legitimate one,
// a restore selection, is far smaller.
const maxMessage = 1 << 20

// Error is the error a call rejects with. Fields lists the invalid fields
// of a validation error.
type Error struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func invalid(format string, a ...any) *Error {
	return &Error{Code: CodeInvalidArgument, Message: fmt.Sprintf(format, a...)}
}

// Event is a message from Go to the page that is not a reply to a call.
type Event struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

type request struct {
	ID   int64           `json:"id"`
	Call string          `json:"call"`
	Args json.RawMessage `json:"args"`
}

type reply struct {
	ID     int64  `json:"id"`
	Result any    `json:"result"`
	Error  *Error `json:"error,omitempty"`
}

// handler runs one call with its raw arguments.
type handler func(ctx context.Context, args json.RawMessage) (any, error)

// Bridge decodes the calls the page makes and runs them. Every call has
// fixed arguments, decoded strictly; names that are not in the table do not
// exist.
type Bridge struct {
	deps  Deps
	calls map[string]handler
}

// Deps is what the bridge works with. Tests replace all of it.
type Deps struct {
	// CLI runs the installed command line.
	CLI Runner
	// Info reports the versions and whether the first setup is needed.
	Info func() (AppInfo, error)
	// PickFolder shows the folder picker, starting in start when it is not
	// empty, and returns the chosen folder or "" when cancelled.
	PickFolder func(ctx context.Context, start string) (string, error)
	// Emit sends an event to the page. It may be called from any goroutine.
	Emit func(Event)
}

// NewBridge returns a bridge with the calls of the contract.
func NewBridge(deps Deps) *Bridge {
	b := &Bridge{deps: deps}
	b.calls = b.table()
	return b
}

// Calls returns the names the page may call.
func (b *Bridge) Calls() []string {
	names := make([]string, 0, len(b.calls))
	for n := range b.calls {
		names = append(names, n)
	}
	return names
}

// Handle runs one message from the page and returns the reply to post
// back. A message that is not a call with a positive id gets no reply,
// since there is no promise to settle; ok is then false.
func (b *Bridge) Handle(ctx context.Context, message string) (out []byte, ok bool) {
	if len(message) > maxMessage {
		return nil, false
	}
	var req request
	if err := decodeStrict([]byte(message), &req); err != nil || req.ID <= 0 {
		return nil, false
	}
	result, err := b.call(ctx, req)
	r := reply{ID: req.ID, Result: result}
	if err != nil {
		r.Result = nil
		r.Error = toError(err)
	}
	out, mErr := json.Marshal(r)
	if mErr != nil {
		out, _ = json.Marshal(reply{ID: req.ID, Error: &Error{Code: CodeFailed, Message: "the result could not be encoded"}})
	}
	return out, true
}

func (b *Bridge) call(ctx context.Context, req request) (any, error) {
	h, ok := b.calls[req.Call]
	if !ok {
		return nil, invalid("there is no call named %q", req.Call)
	}
	return h(ctx, req.Args)
}

// toError maps any error to the contract's codes.
func toError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var c *CLIError
	if errors.As(err, &c) {
		code := CodeFailed
		switch c.Code {
		case "locked":
			code = CodeLocked
		case "wrong_password":
			code = CodeWrongPassword
		case "repository_unavailable":
			code = CodeRepositoryUnavailable
		case "cancelled":
			code = CodeCancelled
		}
		return &Error{Code: code, Message: c.Message}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Code: CodeCancelled, Message: "cancelled"}
	}
	if errors.Is(err, ErrNotInstalled) {
		return &Error{Code: CodeNotInstalled, Message: err.Error()}
	}
	return &Error{Code: CodeFailed, Message: err.Error()}
}

// decodeStrict decodes exactly one JSON value into v, refusing unknown
// fields and trailing data.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

// args decodes the arguments of a call into T. Missing or null arguments
// decode to the zero value; the handler checks what is required.
func args[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return v, nil
	}
	if err := decodeStrict(raw, &v); err != nil {
		return v, invalid("invalid arguments: %v", err)
	}
	return v, nil
}

// noArgs is the argument type of calls that take none: only an empty
// object, null or nothing decodes into it.
type noArgs struct{}
