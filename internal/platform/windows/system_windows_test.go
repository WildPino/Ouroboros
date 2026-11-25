//go:build windows

package windows

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests only read system state; they need no privileges.

func TestDiskUsage(t *testing.T) {
	total, free, err := DiskUsage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || free > total {
		t.Fatalf("total=%d free=%d", total, free)
	}
}

func TestIsElevated(t *testing.T) {
	if _, err := IsElevated(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckService(t *testing.T) {
	// The Event Log service is always present and never disabled.
	if err := CheckService("EventLog"); err != nil {
		t.Fatalf("EventLog: %v", err)
	}
	if err := CheckService("OuroborosNoSuchService"); err == nil {
		t.Fatal("missing service: want error")
	}
}

func TestCheckDirWritable(t *testing.T) {
	dir := t.TempDir()
	if err := CheckDirWritable(dir); err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	if err := CheckDirWritable(filepath.Join(dir, "not", "yet")); err != nil {
		t.Fatalf("missing subdir: %v", err)
	}
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDirWritable(file); err == nil {
		t.Fatal("regular file: want error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("check created files: %v", entries)
	}
}

func TestFreeMemory(t *testing.T) {
	free, err := FreeMemory()
	if err != nil || free < 16<<20 {
		t.Fatalf("FreeMemory = %d, %v", free, err)
	}
}

func TestSystemPath(t *testing.T) {
	p, err := SystemPath()
	if err != nil {
		t.Fatal(err)
	}
	// Every Windows installation has System32 on the machine PATH.
	if !strings.Contains(strings.ToLower(p), `\system32`) {
		t.Fatalf("system PATH %q", p)
	}
}
