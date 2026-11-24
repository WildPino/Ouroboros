package windows

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// NearestExisting returns path itself if it exists, otherwise its closest
// existing ancestor. It lets disk queries work for a repository directory
// that has not been created yet.
func NearestExisting(path string) (string, error) {
	p := filepath.Clean(path)
	for {
		_, err := os.Stat(p)
		if err == nil {
			return p, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("no existing ancestor of %s: %w", path, fs.ErrNotExist)
		}
		p = parent
	}
}
