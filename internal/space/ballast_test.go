package space

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBallastPath(t *testing.T) {
	tests := []struct{ repo, want string }{
		{filepath.Join("B:", "ouroboros"), filepath.Join("B:", "ouroboros.ballast")},
		{filepath.Join("B:", "backups", "repo") + string(filepath.Separator), filepath.Join("B:", "backups", "repo.ballast")},
	}
	for _, tt := range tests {
		if got := BallastPath(tt.repo); got != tt.want {
			t.Errorf("BallastPath(%q) = %q, want %q", tt.repo, got, tt.want)
		}
	}
}

func TestCreateAndDeleteBallast(t *testing.T) {
	p := filepath.Join(t.TempDir(), "repo.ballast")
	const size = 4 << 20

	if err := CreateBallast(p, size); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Size() != size {
		t.Fatalf("stat: %v, size %d", err, fi.Size())
	}
	// Recreating with another size resizes in place.
	if err := CreateBallast(p, size/2); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Size() != size/2 {
		t.Fatalf("resized to %d", fi.Size())
	}

	freed, err := DeleteBallast(p)
	if err != nil || freed != size/2 {
		t.Fatalf("DeleteBallast = %d, %v", freed, err)
	}
	if freed, err := DeleteBallast(p); err != nil || freed != 0 {
		t.Fatalf("second DeleteBallast = %d, %v", freed, err)
	}
	if err := CreateBallast(p, 0); err == nil {
		t.Fatal("zero size: want error")
	}
}

func TestDeleteBallastRefusesDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := DeleteBallast(dir); err == nil {
		t.Fatal("want error for a directory")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("directory was removed")
	}
}

func TestDirSizeLongUnicodePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo Ελληνικά")
	deep := dir
	for len(deep) < 320 {
		deep = filepath.Join(deep, "日本語 𝔸𝔹ℂ "+strings.Repeat("y", 30))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "città.bin"), make([]byte, 12345), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "top"), make([]byte, 5), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DirSize(context.Background(), dir)
	if err != nil || got != 12350 {
		t.Fatalf("DirSize = %d, %v", got, err)
	}
}
