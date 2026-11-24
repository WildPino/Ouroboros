//go:build !windows

package windows

import "errors"

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
