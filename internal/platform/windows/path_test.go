package windows

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNearestExisting(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ in, want string }{
		{dir, dir},
		{sub, sub},
		{filepath.Join(sub, "missing"), sub},
		{filepath.Join(sub, "missing", "deeper"), sub},
	}
	for _, tt := range tests {
		got, err := NearestExisting(tt.in)
		if err != nil {
			t.Fatalf("NearestExisting(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("NearestExisting(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
