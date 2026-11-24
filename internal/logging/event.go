package logging

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
)

// Source is the Windows Event Log source name (DESIGN.md §9).
const Source = "Ouroboros"

// Event IDs. The source is registered with EventCreate.exe as message file,
// which accepts IDs 1 to 1000 and shows the message text as is.
const (
	EventWarning = 2
	EventError   = 3
)

// EventSink receives the warnings and errors to report.
type EventSink interface {
	Warning(eid uint32, msg string) error
	Error(eid uint32, msg string) error
}

// eventHandler forwards records at warning level and above to an
// EventSink, formatted as one line of key=value text.
type eventHandler struct {
	sink EventSink
	mu   *sync.Mutex
	buf  *bytes.Buffer
	text slog.Handler
}

// NewEventHandler returns a handler reporting warnings and errors to sink.
func NewEventHandler(sink EventSink) slog.Handler {
	buf := &bytes.Buffer{}
	text := slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		// The Event Log records time and severity itself.
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey) {
				return slog.Attr{}
			}
			return a
		},
	})
	return &eventHandler{sink: sink, mu: &sync.Mutex{}, buf: buf, text: text}
}

func (h *eventHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }

func (h *eventHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.Reset()
	if err := h.text.Handle(ctx, r); err != nil {
		return err
	}
	msg := string(bytes.TrimSpace(h.buf.Bytes()))
	if r.Level >= slog.LevelError {
		return h.sink.Error(EventError, msg)
	}
	return h.sink.Warning(EventWarning, msg)
}

func (h *eventHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.text = h.text.WithAttrs(attrs)
	return &c
}

func (h *eventHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.text = h.text.WithGroup(name)
	return &c
}
