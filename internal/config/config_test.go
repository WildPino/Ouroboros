package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/WildPino/ouroboros"
)

const minimalTOML = `
[repository]
path = 'B:\ouroboros'

[[job]]
name = "docs"
paths = ['C:\Users\me\Documents']
interval = "6h"
`

func validConfig() Config {
	c := Default()
	c.Repository.Path = `B:\ouroboros`
	c.Jobs = []Job{{
		Name:     "docs",
		Paths:    []string{`C:\Users\me\Documents`},
		Interval: Duration(6 * time.Hour),
	}}
	return c
}

func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := Parse(ouroboros.ExampleConfig)
	if err != nil {
		t.Fatalf("example config: %v", err)
	}
	if len(cfg.Jobs) != 2 {
		t.Fatalf("example config: got %d jobs, want 2", len(cfg.Jobs))
	}
	// The example must spell out the defaults, not diverge from them.
	def := Default()
	if cfg.Space != def.Space || cfg.Schedule != def.Schedule || cfg.Integrity != def.Integrity ||
		cfg.Performance != def.Performance || cfg.Logging != def.Logging || cfg.Retention != def.Retention ||
		cfg.Repository.Compression != def.Repository.Compression {
		t.Errorf("example config values differ from Default():\n got %+v\nwant %+v", *cfg, def)
	}
}

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalTOML))
	if err != nil {
		t.Fatal(err)
	}
	def := Default()
	if cfg.Space != def.Space {
		t.Errorf("space = %+v, want defaults %+v", cfg.Space, def.Space)
	}
	if cfg.Repository.Compression != CompressionMax {
		t.Errorf("compression = %q, want %q", cfg.Repository.Compression, CompressionMax)
	}
	if !cfg.Jobs[0].UseVSS() {
		t.Error("vss should default to true when omitted")
	}
	if cfg.Retention.Enabled() {
		t.Error("retention should be disabled by default")
	}
}

func TestParseOverrides(t *testing.T) {
	doc := minimalTOML + `
vss = false

[space]
min_free = "50GB"
max_repo_size = "1.5TB"
keep_min_snapshots = 5
growth_factor = 2
reserve_file_size = 0

[integrity]
check_read_data_percent = 2.5

[retention]
keep_daily = 7
`
	cfg, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Space
	if s.MinFree != Bytes(50<<30) || s.MaxRepoSize != Bytes(3<<39) || s.KeepMinSnapshots != 5 ||
		s.GrowthFactor != 2 || s.ReserveFileSize != Bytes(0) {
		t.Errorf("space = %+v", s)
	}
	if cfg.Integrity.CheckReadDataPercent != 2.5 {
		t.Errorf("check_read_data_percent = %v", cfg.Integrity.CheckReadDataPercent)
	}
	if cfg.Jobs[0].UseVSS() {
		t.Error("vss = false was ignored")
	}
	if !cfg.Retention.Enabled() {
		t.Error("retention should be enabled")
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	tests := []struct {
		name, doc, key string
	}{
		{"top level", "colour = 1\n" + minimalTOML, "colour"},
		{"section", minimalTOML + "\n[space]\nmin_fre = \"5%\"\n", "space.min_fre"},
		{"unknown section", minimalTOML + "\n[spaces]\nx = 1\n", "spaces"},
		{"job key", minimalTOML + "intervl = \"1h\"\n", "job.intervl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.doc))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("err = %v, want mention of %q", err, tt.key)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, doc string }{
		{"syntax", "[repository\npath = 'B:\\x'"},
		{"bad size", minimalTOML + "\n[space]\nmin_free = \"lots\"\n"},
		{"bad duration", minimalTOML + "\n[schedule]\ntick = \"often\"\n"},
		{"wrong type", minimalTOML + "\n[space]\nkeep_min_snapshots = \"three\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.doc)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseEncodings(t *testing.T) {
	utf16le := func(s string, bom []byte, big bool) []byte {
		out := append([]byte{}, bom...)
		for _, u := range utf16.Encode([]rune(s)) {
			if big {
				out = append(out, byte(u>>8), byte(u))
			} else {
				out = append(out, byte(u), byte(u>>8))
			}
		}
		return out
	}
	tests := []struct {
		name string
		data []byte
	}{
		{"utf8", []byte(minimalTOML)},
		{"utf8 bom", append([]byte{0xEF, 0xBB, 0xBF}, minimalTOML...)},
		{"utf16le bom", utf16le(minimalTOML, []byte{0xFF, 0xFE}, false)},
		{"utf16be bom", utf16le(minimalTOML, []byte{0xFE, 0xFF}, true)},
		{"crlf", []byte(strings.ReplaceAll(minimalTOML, "\n", "\r\n"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Repository.Path != `B:\ouroboros` {
				t.Fatalf("path = %q", cfg.Repository.Path)
			}
		})
	}
	if _, err := Parse([]byte{0xFF, 0xFE, 'a'}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("truncated utf16: err = %v, want ErrInvalid", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.toml")
	if err := os.WriteFile(good, []byte(minimalTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(good); err != nil {
		t.Fatalf("Load(good): %v", err)
	}
	_, err := Load(filepath.Join(dir, "missing.toml"))
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(missing): err = %v, want ErrInvalid wrapping ErrNotExist", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *Config)
		want   string // substring of the problem; empty means valid
	}{
		{"valid", func(c *Config) {}, ""},

		// repository
		{"repo path missing", func(c *Config) { c.Repository.Path = "" }, "repository.path is required"},
		{"repo path relative", func(c *Config) { c.Repository.Path = `backup\repo` }, "repository.path"},
		{"repo path drive relative", func(c *Config) { c.Repository.Path = `B:repo` }, "must be an absolute path"},
		{"repo path unc", func(c *Config) { c.Repository.Path = `\\nas\backup\repo` }, ""},
		{"restic path relative", func(c *Config) { c.Repository.ResticPath = `restic.exe` }, "repository.restic_path"},
		{"restic path absolute", func(c *Config) { c.Repository.ResticPath = `C:\Tools\restic.exe` }, ""},
		{"compression invalid", func(c *Config) { c.Repository.Compression = "best" }, "repository.compression"},
		{"compression off", func(c *Config) { c.Repository.Compression = CompressionOff }, ""},

		// space
		{"min_free 100%", func(c *Config) { c.Space.MinFree = Percent(100) }, "space.min_free must be below 100%"},
		{"min_free unset", func(c *Config) { c.Space.MinFree = Size{} }, "space.min_free is required"},
		{"min_free absolute", func(c *Config) { c.Space.MinFree = Bytes(50 << 30) }, ""},
		{"max_repo_size percent", func(c *Config) { c.Space.MaxRepoSize = Percent(50) }, "space.max_repo_size must be an absolute size"},
		{"max_repo_size zero", func(c *Config) { c.Space.MaxRepoSize = Bytes(0) }, "space.max_repo_size must be greater than zero"},
		{"max_repo_size set", func(c *Config) { c.Space.MaxRepoSize = Bytes(1 << 40) }, ""},
		{"keep_min_snapshots zero", func(c *Config) { c.Space.KeepMinSnapshots = 0 }, "space.keep_min_snapshots"},
		{"growth_factor below 1", func(c *Config) { c.Space.GrowthFactor = 0.9 }, "space.growth_factor"},
		{"first_run_factor zero", func(c *Config) { c.Space.FirstRunFactor = 0 }, "space.first_run_factor"},
		{"prune_headroom 100%", func(c *Config) { c.Space.PruneHeadroom = Percent(100) }, "space.prune_headroom must be below 100%"},
		{"prune_headroom unset", func(c *Config) { c.Space.PruneHeadroom = Size{} }, "space.prune_headroom is required"},
		{"reserve_file_size percent", func(c *Config) { c.Space.ReserveFileSize = Percent(1) }, "space.reserve_file_size"},
		{"reserve_file_size disabled", func(c *Config) { c.Space.ReserveFileSize = Bytes(0) }, ""},
		{"prune_interval zero", func(c *Config) { c.Space.PruneInterval = 0 }, "space.prune_interval"},
		{"protect_newer_than zero", func(c *Config) { c.Space.ProtectNewerThan = 0 }, ""},

		// retention
		{"retention negative", func(c *Config) { c.Retention.KeepWeekly = -1 }, "retention.keep_weekly"},

		// schedule
		{"tick below 1m", func(c *Config) { c.Schedule.Tick = Duration(30 * time.Second) }, "schedule.tick"},
		{"retry_after zero", func(c *Config) { c.Schedule.RetryAfter = 0 }, "schedule.retry_after"},

		// integrity
		{"check_interval zero", func(c *Config) { c.Integrity.CheckInterval = 0 }, "integrity.check_interval"},
		{"read_data_interval zero", func(c *Config) { c.Integrity.CheckReadDataInterval = 0 }, "integrity.check_read_data_interval"},
		{"read_data_percent zero", func(c *Config) { c.Integrity.CheckReadDataPercent = 0 }, "integrity.check_read_data_percent"},
		{"read_data_percent over 100", func(c *Config) { c.Integrity.CheckReadDataPercent = 101 }, "integrity.check_read_data_percent"},

		// performance
		{"memory_profile invalid", func(c *Config) { c.Performance.MemoryProfile = "huge" }, "performance.memory_profile"},
		{"read_concurrency negative", func(c *Config) { c.Performance.ReadConcurrency = -1 }, "performance.read_concurrency"},
		{"pack_size too small", func(c *Config) { c.Performance.PackSizeMB = 2 }, "performance.pack_size_mb"},
		{"pack_size too large", func(c *Config) { c.Performance.PackSizeMB = 256 }, "performance.pack_size_mb"},
		{"pack_size valid", func(c *Config) { c.Performance.PackSizeMB = 64 }, ""},
		{"priority invalid", func(c *Config) { c.Performance.ProcessPriority = "high" }, "performance.process_priority"},

		// logging
		{"level invalid", func(c *Config) { c.Logging.Level = "verbose" }, "logging.level"},
		{"keep_days zero", func(c *Config) { c.Logging.KeepDays = 0 }, "logging.keep_days"},

		// jobs
		{"no jobs", func(c *Config) { c.Jobs = nil }, "at least one [[job]]"},
		{"job name missing", func(c *Config) { c.Jobs[0].Name = "" }, "job #1: name is required"},
		{"job name with space", func(c *Config) { c.Jobs[0].Name = "my docs" }, "may only contain"},
		{"job name with comma", func(c *Config) { c.Jobs[0].Name = "a,b" }, "may only contain"},
		{"duplicate job names", func(c *Config) {
			c.Jobs = append(c.Jobs, Job{Name: "Docs", Paths: []string{`D:\x`}, Interval: Duration(time.Hour)})
		}, "duplicate job name"},
		{"empty paths", func(c *Config) { c.Jobs[0].Paths = nil }, "paths must not be empty"},
		{"empty path entry", func(c *Config) { c.Jobs[0].Paths = []string{" "} }, "empty entries"},
		{"relative path", func(c *Config) { c.Jobs[0].Paths = []string{`Documents`} }, "must be absolute"},
		{"empty exclude", func(c *Config) { c.Jobs[0].Exclude = []string{""} }, "empty patterns"},
		{"exclude_file relative", func(c *Config) { c.Jobs[0].ExcludeFile = "excludes.txt" }, "exclude_file"},
		{"interval missing", func(c *Config) { c.Jobs[0].Interval = 0 }, "interval is required"},
		{"repo inside source", func(c *Config) { c.Jobs[0].Paths = []string{`B:\`} }, "is inside backed-up path"},
		{"repo inside source mixed case", func(c *Config) {
			c.Repository.Path = `b:/Backups/Repo/`
			c.Jobs[0].Paths = []string{`B:\backups`}
		}, "is inside backed-up path"},
		{"repo equals source", func(c *Config) { c.Jobs[0].Paths = []string{`B:\ouroboros`} }, "is inside backed-up path"},
		{"source sibling with common prefix", func(c *Config) { c.Jobs[0].Paths = []string{`B:\ouro`} }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(&c)
			err := c.Validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.want)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v does not match ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := validConfig()
	c.Repository.Path = ""
	c.Logging.Level = "loud"
	c.Jobs[0].Interval = 0
	var ve *ValidationError
	if err := c.Validate(); !errors.As(err, &ve) || len(ve.Problems) != 3 {
		t.Fatalf("want 3 problems, got %v", err)
	}
}

func TestValidateDisk(t *testing.T) {
	tests := []struct {
		name    string
		minFree Size
		size    uint64
		wantErr bool
	}{
		{"percent ok", Percent(10), 1 << 40, false},
		{"absolute ok", Bytes(50 << 30), 100 << 30, false},
		{"absolute equal", Bytes(100 << 30), 100 << 30, true},
		{"absolute larger", Bytes(2 << 40), 1 << 40, true},
		{"zero partition", Percent(10), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			c.Space.MinFree = tt.minFree
			err := c.ValidateDisk(tt.size)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestIsAbsPath(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{`C:\`, true},
		{`C:\Users`, true},
		{`c:/users`, true},
		{`\\server\share`, true},
		{`\\server\share\dir`, true},
		{`\\server`, false},
		{`\\server\`, false},
		{`C:`, false},
		{`C:dir`, false},
		{`\dir`, false},
		{`dir\sub`, false},
		{``, false},
	}
	for _, tt := range tests {
		if got := IsAbsPath(tt.in); got != tt.want {
			t.Errorf("IsAbsPath(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestIsWithin(t *testing.T) {
	tests := []struct {
		inner, outer string
		want         bool
	}{
		{`E:\repo`, `E:\`, true},
		{`E:\repo`, `e:`, true},
		{`E:\repo`, `E:\repo`, true},
		{`E:\repo\`, `E:\Repo`, true},
		{`E:\repo\sub`, `E:\repo`, true},
		{`E:\repository`, `E:\repo`, false},
		{`E:\repo`, `E:\repo\sub`, false},
		{`F:\repo`, `E:\`, false},
		{`E:\a\..\repo`, `E:\repo`, true},
		{`\\nas\share\repo`, `\\nas\share`, true},
		{`\\nas\share2\repo`, `\\nas\share`, false},
	}
	for _, tt := range tests {
		if got := IsWithin(tt.inner, tt.outer); got != tt.want {
			t.Errorf("IsWithin(%q, %q) = %v, want %v", tt.inner, tt.outer, got, tt.want)
		}
	}
}
