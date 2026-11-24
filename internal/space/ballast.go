package space

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BallastPath returns the location of the ballast file for a repository: a
// sibling of the repository directory, so it is on the same partition but
// never inside the repository (DESIGN.md §3.8).
func BallastPath(repo string) string {
	return filepath.Clean(repo) + ".ballast"
}

// CreateBallast creates the ballast file, or resizes an existing one, so that
// it occupies size bytes of real disk space. The file is extended without
// writing data: on NTFS and ReFS setting the end of file allocates clusters
// (the file is not sparse), so the space is reserved immediately.
func CreateBallast(path string, size uint64) error {
	if size == 0 {
		return errors.New("ballast size is zero")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(size)); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("allocate ballast %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// DeleteBallast removes the ballast file and returns its size. A missing file
// is not an error and frees nothing.
func DeleteBallast(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", path)
	}
	if err := os.Remove(path); err != nil {
		return 0, err
	}
	return uint64(fi.Size()), nil
}
