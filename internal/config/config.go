package config

import "time"

// Accepted values of enumerated options.
const (
	CompressionOff  = "off"
	CompressionAuto = "auto"
	CompressionMax  = "max"

	MemoryProfileLow  = "low"
	MemoryProfileAuto = "auto"
	MemoryProfileHigh = "high"

	PriorityIdle        = "idle"
	PriorityBelowNormal = "below_normal"
	PriorityNormal      = "normal"
)

// Config is the full contents of ouroboros.toml.
type Config struct {
	Repository  Repository  `toml:"repository"`
	Space       Space       `toml:"space"`
	Retention   Retention   `toml:"retention"`
	Schedule    Schedule    `toml:"schedule"`
	Integrity   Integrity   `toml:"integrity"`
	Performance Performance `toml:"performance"`
	Logging     Logging     `toml:"logging"`
	Jobs        []Job       `toml:"job"`
}

// Repository locates the restic repository and the restic executable.
type Repository struct {
	Path        string `toml:"path"`
	ResticPath  string `toml:"restic_path"`
	Compression string `toml:"compression"`
}

// Space holds the capacity-based retention settings (DESIGN.md §3.2).
type Space struct {
	MinFree          Size     `toml:"min_free"`
	MaxRepoSize      Size     `toml:"max_repo_size"`
	KeepMinSnapshots int      `toml:"keep_min_snapshots"`
	ProtectNewerThan Duration `toml:"protect_newer_than"`
	GrowthFactor     float64  `toml:"growth_factor"`
	FirstRunFactor   float64  `toml:"first_run_factor"`
	PruneHeadroom    Size     `toml:"prune_headroom"`
	ReserveFileSize  Size     `toml:"reserve_file_size"`
	PruneInterval    Duration `toml:"prune_interval"`
}

// Retention is the optional time-based thinning (DESIGN.md §3.7).
// All zero means disabled.
type Retention struct {
	KeepHourly  int `toml:"keep_hourly"`
	KeepDaily   int `toml:"keep_daily"`
	KeepWeekly  int `toml:"keep_weekly"`
	KeepMonthly int `toml:"keep_monthly"`
	KeepYearly  int `toml:"keep_yearly"`
}

// Enabled reports whether any keep rule is set.
func (r Retention) Enabled() bool {
	return r.KeepHourly+r.KeepDaily+r.KeepWeekly+r.KeepMonthly+r.KeepYearly > 0
}

// Schedule controls the scheduled task and retries.
type Schedule struct {
	Tick         Duration `toml:"tick"`
	RetryAfter   Duration `toml:"retry_after"`
	RunOnBattery bool     `toml:"run_on_battery"`
}

// Integrity controls periodic repository checks.
type Integrity struct {
	CheckInterval         Duration `toml:"check_interval"`
	CheckReadDataInterval Duration `toml:"check_read_data_interval"`
	CheckReadDataPercent  float64  `toml:"check_read_data_percent"`
}

// Performance tunes restic resource usage (DESIGN.md §7).
type Performance struct {
	MemoryProfile   string `toml:"memory_profile"`
	ReadConcurrency int    `toml:"read_concurrency"`
	PackSizeMB      int    `toml:"pack_size_mb"`
	ProcessPriority string `toml:"process_priority"`
}

// Logging controls log output.
type Logging struct {
	Level    string `toml:"level"`
	KeepDays int    `toml:"keep_days"`
	EventLog bool   `toml:"event_log"`
}

// Job is one set of paths backed up on its own interval.
type Job struct {
	Name        string   `toml:"name"`
	Paths       []string `toml:"paths"`
	Exclude     []string `toml:"exclude"`
	ExcludeFile string   `toml:"exclude_file"`
	Interval    Duration `toml:"interval"`
	// VSS is a pointer so that an omitted key can default to true.
	VSS *bool `toml:"vss"`
}

// UseVSS reports whether the job reads sources through a shadow copy.
func (j Job) UseVSS() bool { return j.VSS == nil || *j.VSS }

// Default returns a configuration with every default from DESIGN.md applied
// and no repository or jobs.
func Default() Config {
	return Config{
		Repository: Repository{Compression: CompressionMax},
		Space: Space{
			MinFree:          Percent(10),
			KeepMinSnapshots: 3,
			ProtectNewerThan: Duration(24 * time.Hour),
			GrowthFactor:     1.5,
			FirstRunFactor:   1.0,
			PruneHeadroom:    Percent(2),
			ReserveFileSize:  Bytes(2 << 30),
			PruneInterval:    Duration(7 * day),
		},
		Schedule: Schedule{
			Tick:       Duration(15 * time.Minute),
			RetryAfter: Duration(30 * time.Minute),
		},
		Integrity: Integrity{
			CheckInterval:         Duration(7 * day),
			CheckReadDataInterval: Duration(30 * day),
			CheckReadDataPercent:  5,
		},
		Performance: Performance{
			MemoryProfile:   MemoryProfileAuto,
			ProcessPriority: PriorityBelowNormal,
		},
		Logging: Logging{
			Level:    "info",
			KeepDays: 30,
			EventLog: true,
		},
	}
}
