//go:build !windows

package secret

import "errors"

func protect([]byte) ([]byte, error)   { return nil, errors.ErrUnsupported }
func unprotect([]byte) ([]byte, error) { return nil, errors.ErrUnsupported }

func writeFileAtomic(string, []byte, string) error { return errors.ErrUnsupported }
