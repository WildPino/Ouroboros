package secret

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotFound is returned by Load when no password has been stored yet.
var ErrNotFound = errors.New("repository password not stored; run \"ouroboros init\"")

// FileName is the name of the password file inside the configuration
// directory.
const FileName = "secret.bin"

// DefaultSDDL grants full access to SYSTEM and Administrators only and blocks
// inheritance from the parent directory (DESIGN.md §5, §6).
const DefaultSDDL = "D:P(A;;FA;;;SY)(A;;FA;;;BA)"

// entropy binds the encrypted blob to this application: another program
// using DPAPI on the same machine cannot decrypt it by accident.
var entropy = []byte("ouroboros repository password v1")

// Store reads and writes the encrypted repository password.
type Store struct {
	path string
	sddl string
}

// NewStore returns a Store for the file at path with the default ACL.
func NewStore(path string) *Store { return &Store{path: path, sddl: DefaultSDDL} }

// NewStoreWithSDDL returns a Store that applies a custom security descriptor
// to the file. It exists for tests, which run without Administrator rights
// and would otherwise be unable to read back what they wrote.
func NewStoreWithSDDL(path, sddl string) *Store { return &Store{path: path, sddl: sddl} }

// Path returns the location of the password file.
func (s *Store) Path() string { return s.path }

// Exists reports whether a password file is present.
func (s *Store) Exists() (bool, error) {
	_, err := os.Stat(s.path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// Save encrypts password with DPAPI (machine scope) and atomically replaces
// the password file. The caller should clear password afterwards.
func (s *Store) Save(password []byte) error {
	if len(password) == 0 {
		return errors.New("empty password")
	}
	blob, err := protect(password)
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, blob, s.sddl); err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	return nil
}

// Load decrypts and returns the stored password. The caller should clear the
// returned slice when done.
func (s *Store) Load() ([]byte, error) {
	blob, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	pw, err := unprotect(blob)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", s.path, err)
	}
	return pw, nil
}

// Remove deletes the password file; a missing file is not an error.
func (s *Store) Remove() error {
	err := os.Remove(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Clear overwrites b with zeros.
func Clear(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
