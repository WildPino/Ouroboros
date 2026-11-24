package config

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// ValidationError lists every problem found in a configuration, so that the
// user can fix them all in one pass.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return "invalid configuration: " + e.Problems[0]
	}
	return "invalid configuration:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Is makes errors.Is(err, ErrInvalid) true for validation errors.
func (e *ValidationError) Is(target error) bool { return target == ErrInvalid }

// Job names end up in restic tags ("job:<name>"), which must not contain
// commas or spaces.
var jobNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Pack sizes accepted by restic --pack-size, in MiB.
const (
	minPackSizeMB = 4
	maxPackSizeMB = 128
)

// Validate checks everything that can be checked without touching the disk.
func (c *Config) Validate() error {
	var p problems

	// [repository]
	switch {
	case c.Repository.Path == "":
		p.add("repository.path is required")
	case !IsAbsPath(c.Repository.Path):
		p.add("repository.path %q must be an absolute path", c.Repository.Path)
	case IsRoot(c.Repository.Path):
		p.add("repository.path %q must be a folder, not the root of a drive or share", c.Repository.Path)
	}
	if c.Repository.ResticPath != "" && !IsAbsPath(c.Repository.ResticPath) {
		p.add("repository.restic_path %q must be an absolute path", c.Repository.ResticPath)
	}
	p.oneOf("repository.compression", c.Repository.Compression, CompressionOff, CompressionAuto, CompressionMax)

	// [space]
	s := c.Space
	p.percentBelow100("space.min_free", s.MinFree)
	if s.MaxRepoSize.IsSet() {
		switch {
		case s.MaxRepoSize.IsPercent():
			p.add("space.max_repo_size must be an absolute size, not a percentage")
		case s.MaxRepoSize.Resolve(0) == 0:
			p.add("space.max_repo_size must be greater than zero; leave it empty for no limit")
		}
	}
	if s.KeepMinSnapshots < 1 {
		p.add("space.keep_min_snapshots must be at least 1")
	}
	if s.GrowthFactor < 1 {
		p.add("space.growth_factor must be at least 1.0")
	}
	if s.FirstRunFactor <= 0 {
		p.add("space.first_run_factor must be greater than zero")
	}
	p.percentBelow100("space.prune_headroom", s.PruneHeadroom)
	if s.ReserveFileSize.IsPercent() {
		p.add("space.reserve_file_size must be an absolute size, not a percentage")
	}
	p.positive("space.prune_interval", s.PruneInterval)

	// [retention]
	r := c.Retention
	for _, k := range []struct {
		name string
		v    int
	}{
		{"keep_hourly", r.KeepHourly}, {"keep_daily", r.KeepDaily}, {"keep_weekly", r.KeepWeekly},
		{"keep_monthly", r.KeepMonthly}, {"keep_yearly", r.KeepYearly},
	} {
		if k.v < 0 {
			p.add("retention.%s must not be negative", k.name)
		}
	}

	// [schedule]
	if c.Schedule.Tick.Std() < time.Minute {
		p.add("schedule.tick must be at least 1m")
	}
	p.positive("schedule.retry_after", c.Schedule.RetryAfter)

	// [integrity]
	p.positive("integrity.check_interval", c.Integrity.CheckInterval)
	p.positive("integrity.check_read_data_interval", c.Integrity.CheckReadDataInterval)
	if pct := c.Integrity.CheckReadDataPercent; pct <= 0 || pct > 100 {
		p.add("integrity.check_read_data_percent must be greater than 0 and at most 100")
	}

	// [performance]
	p.oneOf("performance.memory_profile", c.Performance.MemoryProfile, MemoryProfileLow, MemoryProfileAuto, MemoryProfileHigh)
	if c.Performance.ReadConcurrency < 0 {
		p.add("performance.read_concurrency must not be negative")
	}
	if ps := c.Performance.PackSizeMB; ps != 0 && (ps < minPackSizeMB || ps > maxPackSizeMB) {
		p.add("performance.pack_size_mb must be 0 (from profile) or between %d and %d", minPackSizeMB, maxPackSizeMB)
	}
	p.oneOf("performance.process_priority", c.Performance.ProcessPriority, PriorityIdle, PriorityBelowNormal, PriorityNormal)

	// [logging]
	p.oneOf("logging.level", c.Logging.Level, "debug", "info", "warn", "error")
	if c.Logging.KeepDays < 1 {
		p.add("logging.keep_days must be at least 1")
	}

	// [timeouts]
	p.positive("timeouts.backup", c.Timeouts.Backup)
	p.positive("timeouts.prune", c.Timeouts.Prune)
	p.positive("timeouts.check", c.Timeouts.Check)
	p.positive("timeouts.restore", c.Timeouts.Restore)
	p.positive("timeouts.other", c.Timeouts.Other)

	c.validateJobs(&p)

	return p.err()
}

func (c *Config) validateJobs(p *problems) {
	if len(c.Jobs) == 0 {
		p.add("at least one [[job]] is required")
		return
	}
	seen := make(map[string]bool)
	repoAbs := c.Repository.Path != "" && IsAbsPath(c.Repository.Path)
	for i, j := range c.Jobs {
		label := fmt.Sprintf("job #%d", i+1)
		switch {
		case j.Name == "":
			p.add("%s: name is required", label)
		case !jobNamePattern.MatchString(j.Name):
			p.add("%s: name %q may only contain letters, digits, '.', '_' and '-', and must start with a letter or digit", label, j.Name)
		default:
			label = fmt.Sprintf("job %q", j.Name)
			key := strings.ToLower(j.Name)
			if seen[key] {
				p.add("%s: duplicate job name", label)
			}
			seen[key] = true
		}

		if len(j.Paths) == 0 {
			p.add("%s: paths must not be empty", label)
		}
		for _, src := range j.Paths {
			switch {
			case strings.TrimSpace(src) == "":
				p.add("%s: paths must not contain empty entries", label)
			case !IsAbsPath(src):
				p.add("%s: path %q must be absolute", label, src)
			case repoAbs && IsWithin(c.Repository.Path, src):
				p.add("%s: repository.path %q is inside backed-up path %q", label, c.Repository.Path, src)
			}
		}
		for _, ex := range j.Exclude {
			if strings.TrimSpace(ex) == "" {
				p.add("%s: exclude must not contain empty patterns", label)
			}
		}
		if j.ExcludeFile != "" && !IsAbsPath(j.ExcludeFile) {
			p.add("%s: exclude_file %q must be an absolute path", label, j.ExcludeFile)
		}
		if j.Interval <= 0 {
			p.add("%s: interval is required and must be greater than zero", label)
		}
	}
}

// ValidateDisk checks the rules that depend on the size of the partition
// holding the repository.
func (c *Config) ValidateDisk(partitionSize uint64) error {
	var p problems
	if minFree := c.Space.MinFree.Resolve(partitionSize); minFree >= partitionSize {
		p.add("space.min_free (%s = %d bytes) is not smaller than the partition (%d bytes)",
			c.Space.MinFree, minFree, partitionSize)
	}
	return p.err()
}

// IsAbsPath reports whether p is an absolute Windows path: a drive letter
// followed by a separator, or a UNC path. It is implemented without
// path/filepath so that validation behaves the same on every platform.
func IsAbsPath(p string) bool {
	if len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && isSep(p[2]) {
		return true
	}
	if len(p) >= 5 && isSep(p[0]) && isSep(p[1]) {
		// \\server\share: both components must be non-empty.
		rest := strings.FieldsFunc(normalize(p[2:]), func(r rune) bool { return r == '/' })
		return len(rest) >= 2
	}
	return false
}

// IsRoot reports whether p is the root of a drive (`B:\`) or of a UNC share
// (`\\server\share`).
func IsRoot(p string) bool {
	n := normalize(p)
	if len(n) == 3 && n[1] == ':' && n[2] == '/' {
		return true
	}
	if strings.HasPrefix(n, "//") {
		return len(strings.FieldsFunc(n, func(r rune) bool { return r == '/' })) == 2
	}
	return false
}

// IsWithin reports whether path inner is equal to or below path outer.
// Comparison is case-insensitive and ignores the separator style.
func IsWithin(inner, outer string) bool {
	in, out := normalize(inner), normalize(outer)
	if in == out {
		return true
	}
	if !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return strings.HasPrefix(in, out)
}

// normalize lowercases, converts separators to '/', and removes "." and ".."
// elements and trailing separators except after a drive root ("c:/").
func normalize(p string) string {
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	unc := strings.HasPrefix(p, "//")
	p = path.Clean(p)
	if unc {
		p = "/" + p
	}
	if len(p) == 2 && p[1] == ':' {
		p += "/"
	}
	return p
}

func isDriveLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
func isSep(b byte) bool         { return b == '\\' || b == '/' }

type problems struct{ list []string }

func (p *problems) add(format string, args ...any) {
	p.list = append(p.list, fmt.Sprintf(format, args...))
}

func (p *problems) oneOf(key, value string, allowed ...string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	p.add("%s must be one of %s, got %q", key, strings.Join(allowed, ", "), value)
}

func (p *problems) positive(key string, d Duration) {
	if d <= 0 {
		p.add("%s must be greater than zero", key)
	}
}

func (p *problems) percentBelow100(key string, s Size) {
	if !s.IsSet() {
		p.add("%s is required", key)
		return
	}
	if s.IsPercent() && s.PercentValue() >= 100 {
		p.add("%s must be below 100%%", key)
	}
}

func (p *problems) err() error {
	if len(p.list) == 0 {
		return nil
	}
	return &ValidationError{Problems: p.list}
}
