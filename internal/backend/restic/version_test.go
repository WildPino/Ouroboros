package restic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseVersionFixtures(t *testing.T) {
	tests := []struct {
		file string
		want Version
		ok   bool
	}{
		{"version-0.20.0.txt", Version{0, 20, 0}, true},
		{"version-0.17.3-dev.txt", Version{0, 17, 3}, true},
		{"version-0.16.4.txt", Version{0, 16, 4}, false},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseVersion(data)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if got.AtLeast(MinVersion) != tt.ok {
				t.Fatalf("%v.AtLeast(%v) = %v, want %v", got, MinVersion, !tt.ok, tt.ok)
			}
		})
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, in := range []string{"", "restic", "rustic 0.7.0", "restic version unknown"} {
		if _, err := ParseVersion([]byte(in)); err == nil {
			t.Errorf("ParseVersion(%q): want error", in)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		v, o Version
		want bool
	}{
		{Version{0, 17, 0}, Version{0, 17, 0}, true},
		{Version{0, 17, 1}, Version{0, 17, 0}, true},
		{Version{0, 18, 0}, Version{0, 17, 9}, true},
		{Version{1, 0, 0}, Version{0, 99, 99}, true},
		{Version{0, 16, 9}, Version{0, 17, 0}, false},
		{Version{0, 17, 0}, Version{0, 17, 1}, false},
		{Version{0, 99, 0}, Version{1, 0, 0}, false},
	}
	for _, tt := range tests {
		if got := tt.v.AtLeast(tt.o); got != tt.want {
			t.Errorf("%v.AtLeast(%v) = %v, want %v", tt.v, tt.o, got, tt.want)
		}
	}
}

func TestFindExecutableConfigured(t *testing.T) {
	got, err := FindExecutable(`C:\Tools\restic.exe`)
	if err != nil || got != `C:\Tools\restic.exe` {
		t.Fatalf("got %q, %v", got, err)
	}
}

// TestExecutableVersion runs the real restic binary; it is skipped when
// restic is not installed. "restic version" does not access any repository.
func TestExecutableVersion(t *testing.T) {
	exe, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic not installed")
	}
	v, err := ExecutableVersion(context.Background(), exe)
	if err != nil {
		t.Fatal(err)
	}
	if v == (Version{}) {
		t.Fatal("empty version")
	}
}

func TestExecutableVersionMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "restic.exe")
	if _, err := ExecutableVersion(context.Background(), missing); err == nil {
		t.Fatal("want error for missing executable")
	}
}
