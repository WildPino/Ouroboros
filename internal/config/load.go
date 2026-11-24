package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/BurntSushi/toml"
)

// ErrInvalid is matched by every error caused by the configuration file
// itself (missing, unreadable, malformed or failing validation), as opposed
// to errors of the environment.
var ErrInvalid = errors.New("invalid configuration")

// Load reads, decodes and validates the configuration file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates a configuration document.
func Parse(data []byte) (*Config, error) {
	text, err := decodeText(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	cfg := Default()
	md, err := toml.Decode(text, &cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		slices.Sort(keys)
		keys = slices.Compact(keys)
		return nil, &ValidationError{Problems: []string{"unknown keys: " + strings.Join(keys, ", ")}}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// decodeText returns the document as a string, accepting UTF-8 with or
// without BOM and UTF-16 with BOM. Windows PowerShell 5.1 writes UTF-16 when
// output is redirected with ">", which is how the README creates the file.
func decodeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return string(data[3:]), nil
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return decodeUTF16(data[2:], false)
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeUTF16(data[2:], true)
	}
	return string(data), nil
}

func decodeUTF16(b []byte, bigEndian bool) (string, error) {
	if len(b)%2 != 0 {
		return "", errors.New("truncated UTF-16 text")
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		lo, hi := b[2*i], b[2*i+1]
		if bigEndian {
			lo, hi = hi, lo
		}
		u[i] = uint16(lo) | uint16(hi)<<8
	}
	return string(utf16.Decode(u)), nil
}
