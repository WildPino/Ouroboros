//go:build !windows

package windows

import (
	"context"
	"errors"
)

// ErrServiceDisabled is returned by CheckService for a disabled service.
var ErrServiceDisabled = errors.New("service is disabled")

// DiskUsage is only implemented on Windows.
func DiskUsage(path string) (total, free uint64, err error) {
	return 0, 0, errors.ErrUnsupported
}

// IsElevated is only implemented on Windows.
func IsElevated() (bool, error) { return false, errors.ErrUnsupported }

// CheckService is only implemented on Windows.
func CheckService(name string) error { return errors.ErrUnsupported }

// CheckDirWritable is only implemented on Windows.
func CheckDirWritable(dir string) error { return errors.ErrUnsupported }

// AssignToKillOnExitJob is only implemented on Windows.
func AssignToKillOnExitJob(pid int) error { return errors.ErrUnsupported }

// CreateTask is only implemented on Windows.
func CreateTask(ctx context.Context, name string, def []byte) error { return errors.ErrUnsupported }

// DeleteTask is only implemented on Windows.
func DeleteTask(ctx context.Context, name string) error { return errors.ErrUnsupported }

// TaskExists is only implemented on Windows.
func TaskExists(ctx context.Context, name string) (bool, error) { return false, errors.ErrUnsupported }

// UntrustedWriters is only implemented on Windows.
func UntrustedWriters(path string) ([]string, error) { return nil, errors.ErrUnsupported }

// FreeMemory is only implemented on Windows.
func FreeMemory() (uint64, error) { return 0, errors.ErrUnsupported }

// SystemPath is only implemented on Windows.
func SystemPath() (string, error) { return "", errors.ErrUnsupported }
