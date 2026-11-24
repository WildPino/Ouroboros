package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Duration is a time.Duration that also accepts a "d" (24h) unit.
type Duration time.Duration

const day = 24 * time.Hour

var durationPart = regexp.MustCompile(`^([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(ns|us|µs|ms|s|m|h|d)`)

// ParseDuration parses Go duration syntax extended with days: "30m", "6h",
// "7d", "1d12h". Negative values are rejected.
func ParseDuration(text string) (Duration, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0, fmt.Errorf("invalid duration %q: empty", text)
	}
	if s == "0" {
		return 0, nil
	}
	var total float64
	for rest := s; rest != ""; {
		m := durationPart.FindStringSubmatch(rest)
		if m == nil {
			return 0, fmt.Errorf("invalid duration %q: expected values such as 30m, 6h or 7d", text)
		}
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", text, err)
		}
		if m[2] == "d" {
			total += n * float64(day)
		} else {
			unit, _ := time.ParseDuration("1" + m[2])
			total += n * float64(unit)
		}
		rest = rest[len(m[0]):]
	}
	if total > math.MaxInt64 {
		return 0, fmt.Errorf("invalid duration %q: too large", text)
	}
	return Duration(math.Round(total)), nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string {
	td := time.Duration(d)
	if td > 0 && td%day == 0 {
		return strconv.FormatInt(int64(td/day), 10) + "d"
	}
	return td.String()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}
