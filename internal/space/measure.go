package space

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	platform "github.com/WildPino/ouroboros/internal/platform/windows"
)

// Usage is the size and free space of a partition, in bytes.
type Usage struct {
	Total uint64
	Free  uint64
}

// FreeSpaceProvider measures the partition that holds a path.
type FreeSpaceProvider interface {
	Usage(path string) (Usage, error)
}

// RepoSizer measures the size of the repository directory.
type RepoSizer interface {
	RepoSize(ctx context.Context, dir string) (uint64, error)
}

// Clock returns the current time.
type Clock interface {
	Now() time.Time
}

// SystemDisk is the FreeSpaceProvider of the real machine.
type SystemDisk struct{}

// Usage implements FreeSpaceProvider with GetDiskFreeSpaceExW.
func (SystemDisk) Usage(path string) (Usage, error) {
	total, free, err := platform.DiskUsage(path)
	return Usage{Total: total, Free: free}, err
}

// DirSizer is the RepoSizer of the real machine.
type DirSizer struct{}

// RepoSize implements RepoSizer with DirSize.
func (DirSizer) RepoSize(ctx context.Context, dir string) (uint64, error) { return DirSize(ctx, dir) }

// SystemClock is the Clock of the real machine.
type SystemClock struct{}

// Now returns time.Now().
func (SystemClock) Now() time.Time { return time.Now() }

// DirSize returns the total size of the regular files under dir. It is much
// faster than "restic stats" on large repositories (DESIGN.md §3.1). A
// missing directory has size zero.
func DirSize(ctx context.Context, dir string) (uint64, error) {
	var total uint64
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			// Files may disappear while restic is pruning; skip them.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if n++; n%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measure %s: %w", dir, err)
	}
	return total, nil
}

// Ballast creates and removes the emergency ballast file.
type Ballast interface {
	// Exists reports whether the ballast is present.
	Exists() (bool, error)
	// Create reserves the ballast; Delete removes it and returns the bytes
	// it occupied.
	Create(size uint64) error
	Delete() (uint64, error)
}

// BallastFile is a Ballast backed by a file on disk.
type BallastFile struct{ Path string }

// Exists implements Ballast.
func (b BallastFile) Exists() (bool, error) {
	_, err := os.Stat(b.Path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// Create implements Ballast.
func (b BallastFile) Create(size uint64) error { return CreateBallast(b.Path, size) }

// Delete implements Ballast.
func (b BallastFile) Delete() (uint64, error) { return DeleteBallast(b.Path) }
