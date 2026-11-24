package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/logging"
)

type memSink struct{ msgs []string }

func (m *memSink) Warning(_ uint32, msg string) error { m.msgs = append(m.msgs, "W "+msg); return nil }
func (m *memSink) Error(_ uint32, msg string) error   { m.msgs = append(m.msgs, "E "+msg); return nil }

func TestOpenLog(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ouroboros.toml")
	cfg := config.Default()
	cfg.Logging.Level = "warn"
	sink := &memSink{}
	var console bytes.Buffer
	log, closeLog := openLog(&cfg, cfgPath, &console, func() (logging.EventSink, func() error, error) {
		return sink, func() error { return nil }, nil
	})
	log.Info("hidden")
	log.Warn("careful", "job", "docs")
	log.Error("broken")
	closeLog()

	if strings.Contains(console.String(), "hidden") || !strings.Contains(console.String(), "careful") {
		t.Fatalf("console %q", console.String())
	}
	entries, err := os.ReadDir(logDir(cfgPath))
	if err != nil || len(entries) != 1 {
		t.Fatalf("log dir: %v %v", entries, err)
	}
	data, _ := os.ReadFile(filepath.Join(logDir(cfgPath), entries[0].Name()))
	if !strings.Contains(string(data), "level=ERROR msg=broken") {
		t.Fatalf("file %q", data)
	}
	if len(sink.msgs) != 2 || !strings.HasPrefix(sink.msgs[0], "W msg=careful") || sink.msgs[1] != "E msg=broken" {
		t.Fatalf("events %q", sink.msgs)
	}

	// Event Log off in the configuration, or unavailable: no failure.
	cfg.Logging.EventLog = false
	log, closeLog = openLog(&cfg, cfgPath, &console, func() (logging.EventSink, func() error, error) {
		t.Fatal("opened although disabled")
		return nil, nil, nil
	})
	log.Error("x")
	closeLog()
	cfg.Logging.EventLog = true
	console.Reset()
	log, closeLog = openLog(&cfg, cfgPath, &console, func() (logging.EventSink, func() error, error) {
		return nil, nil, errors.New("no access")
	})
	log.Error("y")
	closeLog()
	if !strings.Contains(console.String(), "Event Log disabled: no access") {
		t.Fatalf("console %q", console.String())
	}
}
