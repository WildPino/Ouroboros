package space

import (
	"os"
	"path/filepath"
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
