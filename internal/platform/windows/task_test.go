package windows

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestISODuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Minute, "PT1M"},
		{15 * time.Minute, "PT15M"},
		{time.Hour, "PT1H"},
		{90 * time.Minute, "PT1H30M"},
		{24 * time.Hour, "P1D"},
		{25*time.Hour + time.Minute, "P1DT1H1M"},
		{31 * 24 * time.Hour, "P31D"},
	}
	for _, tt := range tests {
		got, err := isoDuration(tt.d)
		if err != nil || got != tt.want {
			t.Errorf("isoDuration(%s) = %q, %v; want %q", tt.d, got, err, tt.want)
		}
	}
	for _, d := range []time.Duration{0, 30 * time.Second, 90 * time.Second, 32 * 24 * time.Hour} {
		if _, err := isoDuration(d); err == nil {
			t.Errorf("isoDuration(%s): want error", d)
		}
	}
}

func spec() TaskSpec {
	return TaskSpec{
		Command:     `C:\Program Files\Ouroboros\ouroboros.exe`,
		Arguments:   `run --config "C:\ProgramData\Ouroboros\ouroboros.toml"`,
		Interval:    15 * time.Minute,
		Start:       time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		Description: "Backups <&> maintenance",
	}
}

func TestTaskXML(t *testing.T) {
	raw, err := taskXMLUTF8(spec())
	if err != nil {
		t.Fatal(err)
	}
	var doc taskDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	s := doc.Settings
	switch {
	case doc.Registration.URI != TaskName:
		t.Error("URI")
	case doc.Registration.Description != "Backups <&> maintenance":
		t.Error("description not escaped and restored")
	case doc.Principals.Principal.UserID != "S-1-5-18" || doc.Principals.Principal.RunLevel != "HighestAvailable":
		t.Error("principal")
	case doc.Triggers.Time.Repetition.Interval != "PT15M" || doc.Triggers.Time.StartBoundary != "2026-05-01T10:00:00":
		t.Errorf("trigger %+v", doc.Triggers.Time)
	case !s.StartWhenAvailable || s.ExecutionTimeLimit != "PT0S" || s.MultipleInstancesPolicy != "IgnoreNew":
		t.Errorf("settings %+v", s)
	case !s.DisallowStartIfOnBatteries || s.StopIfGoingOnBatteries:
		t.Errorf("battery settings %+v", s)
	case doc.Actions.Exec.Command != spec().Command || doc.Actions.Exec.Arguments != spec().Arguments:
		t.Errorf("action %+v", doc.Actions.Exec)
	}

	sp := spec()
	sp.RunOnBattery = true
	raw, _ = taskXMLUTF8(sp)
	doc = taskDocument{}
	xml.Unmarshal(raw, &doc)
	if doc.Settings.DisallowStartIfOnBatteries || doc.Settings.StopIfGoingOnBatteries {
		t.Errorf("run_on_battery: %+v", doc.Settings)
	}

	if _, err := TaskXML(TaskSpec{Interval: time.Minute}); err == nil {
		t.Error("missing command accepted")
	}
}

func TestTaskXMLIsUTF16(t *testing.T) {
	b, err := TaskXML(spec())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte{0xFF, 0xFE}) || len(b)%2 != 0 {
		t.Fatal("not UTF-16LE with BOM")
	}
	u := make([]uint16, (len(b)-2)/2)
	for i := range u {
		u[i] = uint16(b[2+2*i]) | uint16(b[3+2*i])<<8
	}
	s := string(utf16.Decode(u))
	if !strings.HasPrefix(s, `<?xml version="1.0" encoding="UTF-16"?>`) || !strings.Contains(s, `<URI>\Ouroboros\Tick</URI>`) {
		t.Fatalf("decoded:\n%s", s)
	}
}
