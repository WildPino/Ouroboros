package restic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// Version is a restic release number.
type Version struct {
	Major, Minor, Patch int
}

// MinVersion is the oldest restic release Ouroboros supports (DESIGN.md §2.2).
var MinVersion = Version{0, 17, 0}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v is the same as or newer than o.
func (v Version) AtLeast(o Version) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor > o.Minor
	}
	return v.Patch >= o.Patch
}

var versionLine = regexp.MustCompile(`(?m)^restic (\d+)\.(\d+)\.(\d+)`)

// ParseVersion extracts the version from the output of "restic version",
// e.g. "restic 0.17.3 compiled with go1.23.4 on windows/amd64". Pre-release
// suffixes such as "-dev" are ignored.
func ParseVersion(out []byte) (Version, error) {
	m := versionLine.FindSubmatch(out)
	if m == nil {
		return Version{}, fmt.Errorf("unrecognized restic version output: %q", bytes.TrimSpace(out))
	}
	var n [3]int
	for i := range n {
		v, err := strconv.Atoi(string(m[i+1]))
		if err != nil {
			return Version{}, fmt.Errorf("unrecognized restic version output: %w", err)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// FindExecutable returns configured if set, otherwise restic.exe from PATH.
func FindExecutable(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	p, err := exec.LookPath("restic")
	if err != nil {
		return "", fmt.Errorf("restic not found on PATH: %w", err)
	}
	return p, nil
}

const versionTimeout = 30 * time.Second

// ExecutableVersion runs "<exe> version" and parses its output.
func ExecutableVersion(ctx context.Context, exe string) (Version, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, "version")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s: %w", versionTimeout, err)
		}
		return Version{}, fmt.Errorf("run %s version: %w", exe, err)
	}
	return ParseVersion(stdout.Bytes())
}
