package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Size is either an absolute byte count or a percentage of the partition
// size. The zero value means "not set".
type Size struct {
	bytes     uint64
	percent   float64
	isPercent bool
	set       bool
}

var sizeUnits = []struct {
	suffix string
	mult   uint64
}{
	// Longest suffixes first so that "KB" is not matched as "B".
	{"TB", 1 << 40},
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"B", 1},
}

// Bytes returns a Size of n bytes.
func Bytes(n uint64) Size { return Size{bytes: n, set: true} }

// Percent returns a Size of p percent of the partition.
func Percent(p float64) Size { return Size{percent: p, isPercent: true, set: true} }

// ParseSize parses "50GB", "1.5TB", "0", "10%". Units are powers of 1024 and
// case-insensitive; a bare number is a byte count. The empty string yields an
// unset Size.
func ParseSize(text string) (Size, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return Size{}, nil
	}
	if num, ok := strings.CutSuffix(s, "%"); ok {
		p, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
		if err != nil || math.IsNaN(p) || p < 0 || p > 100 {
			return Size{}, fmt.Errorf("invalid percentage %q: must be between 0%% and 100%%", text)
		}
		return Percent(p), nil
	}

	upper := strings.ToUpper(s)
	mult := uint64(1)
	for _, u := range sizeUnits {
		if num, ok := strings.CutSuffix(upper, u.suffix); ok {
			upper, mult = strings.TrimSpace(num), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(upper, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return Size{}, fmt.Errorf("invalid size %q: expected a number with an optional unit B, KB, MB, GB, TB or %%", text)
	}
	b := f * float64(mult)
	if b >= math.MaxUint64 {
		return Size{}, fmt.Errorf("invalid size %q: too large", text)
	}
	return Bytes(uint64(math.Round(b))), nil
}

// IsSet reports whether a value was given.
func (s Size) IsSet() bool { return s.set }

// IsPercent reports whether the size is relative to the partition size.
func (s Size) IsPercent() bool { return s.isPercent }

// Resolve returns the size in bytes for a partition of total bytes.
func (s Size) Resolve(total uint64) uint64 {
	if !s.isPercent {
		return s.bytes
	}
	return uint64(math.Round(float64(total) * s.percent / 100))
}

// PercentValue returns the percentage, valid only when IsPercent is true.
func (s Size) PercentValue() float64 { return s.percent }

func (s Size) String() string {
	switch {
	case !s.set:
		return ""
	case s.isPercent:
		return strconv.FormatFloat(s.percent, 'f', -1, 64) + "%"
	}
	for _, u := range sizeUnits {
		if s.bytes >= u.mult && s.bytes%u.mult == 0 {
			return strconv.FormatUint(s.bytes/u.mult, 10) + u.suffix
		}
	}
	return strconv.FormatUint(s.bytes, 10) + "B"
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Size) UnmarshalText(b []byte) error {
	v, err := ParseSize(string(b))
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// UnmarshalTOML accepts both strings ("2GB") and bare integers (bytes), so
// that reserve_file_size = 0 works with or without quotes.
func (s *Size) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case string:
		return s.UnmarshalText([]byte(x))
	case int64:
		if x < 0 {
			return fmt.Errorf("invalid size %d: must not be negative", x)
		}
		*s = Bytes(uint64(x))
		return nil
	default:
		return fmt.Errorf("invalid size %v: expected a string such as \"50GB\" or \"10%%\"", v)
	}
}
