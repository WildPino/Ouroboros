package setup

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v0.2.0", "v0.2.0", 0, true},
		{"0.2.0", "v0.2.0", 0, true},
		{"v0.2.0", "v0.1.9", 1, true},
		{"v0.10.0", "v0.9.0", 1, true},
		{"v1.0.0", "v0.99.99", 1, true},
		{"v0.2.0-rc.1", "v0.2.0", -1, true},
		{"v0.2.0-rc.2", "v0.2.0-rc.1", 1, true},
		{"v0.2.0-3-gabc1234", "v0.2.0", 1, true},
		{"v0.2.0-3-gabc1234-dirty", "v0.2.0-4-gdef5678", -1, true},
		{"v0.2.0-dirty", "v0.2.0", 0, true},
		{"v0.1.0-30-g22e505a", "v0.2.0", -1, true},
		{"dev", "v0.2.0", 0, false},
		{"v0.2.0", "abc1234-dirty", 0, false},
		{"", "v0.2.0", 0, false},
	}
	for _, tt := range tests {
		got, ok := CompareVersions(tt.a, tt.b)
		if got != tt.want || ok != tt.ok {
			t.Errorf("CompareVersions(%q, %q) = %d, %v; want %d, %v", tt.a, tt.b, got, ok, tt.want, tt.ok)
		}
	}
}

func TestPathEntries(t *testing.T) {
	const dir = `C:\Program Files\Ouroboros\bin`
	add := []struct{ in, want string }{
		{``, dir},
		{`C:\Windows`, `C:\Windows;` + dir},
		{`C:\Windows;`, `C:\Windows;` + dir},
		{`C:\Windows;c:\program files\ouroboros\BIN\`, `C:\Windows;c:\program files\ouroboros\BIN\`},
	}
	for _, tt := range add {
		got, changed := addPathEntry(tt.in, dir)
		if got != tt.want || changed != (tt.in != tt.want) {
			t.Errorf("addPathEntry(%q) = %q, %v; want %q", tt.in, got, changed, tt.want)
		}
	}
	remove := []struct{ in, want string }{
		{`C:\Windows;` + dir, `C:\Windows`},
		{dir + `;C:\Windows;%SystemRoot%\system32`, `C:\Windows;%SystemRoot%\system32`},
		{`C:\Windows; C:\PROGRAM FILES\Ouroboros\bin\ ;C:\Tools;` + dir, `C:\Windows;C:\Tools`},
		{`C:\Windows;;C:\Tools`, `C:\Windows;;C:\Tools`},
		{`C:\Program Files\Ouroboros\bin2`, `C:\Program Files\Ouroboros\bin2`},
	}
	for _, tt := range remove {
		got, changed := removePathEntry(tt.in, dir)
		if got != tt.want || changed != (tt.in != tt.want) {
			t.Errorf("removePathEntry(%q) = %q, %v; want %q", tt.in, got, changed, tt.want)
		}
	}
}

func TestCleanLeftovers(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"ouroboros.exe", "ouroboros.exe.old", "Ouroboros.exe.0123abcd.old", "ouroboros.exe.new",
		"restic.exe.old", "LICENSE.old", "LICENSE.new",
		"notes.old", "ouroboros.exe.bak", "restic.exe.12.old", "my-ouroboros.exe.old",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "restic.exe.old.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	left := func() []string {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}

	cleanLeftovers(dir, false)
	want := []string{"LICENSE.new", "my-ouroboros.exe.old", "notes.old", "ouroboros.exe", "ouroboros.exe.bak",
		"ouroboros.exe.new", "restic.exe.12.old", "restic.exe.old.d"}
	if got := left(); !slices.Equal(got, want) {
		t.Fatalf("after cleanLeftovers without new: %v, want %v", got, want)
	}
	if stuck := cleanLeftovers(dir, true); stuck != nil {
		t.Fatalf("stuck = %v", stuck)
	}
	want = []string{"my-ouroboros.exe.old", "notes.old", "ouroboros.exe", "ouroboros.exe.bak", "restic.exe.12.old", "restic.exe.old.d"}
	if got := left(); !slices.Equal(got, want) {
		t.Fatalf("after cleanLeftovers: %v, want %v", got, want)
	}
	if stuck := cleanLeftovers(filepath.Join(dir, "missing"), true); stuck != nil {
		t.Errorf("missing folder: stuck = %v", stuck)
	}
}
