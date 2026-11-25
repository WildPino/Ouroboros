package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Marshal renders c in the layout of the example configuration, with its
// comments. Comments written by hand in an existing file are not kept: the
// output depends only on the values.
func Marshal(c *Config) []byte {
	var w tomlWriter
	w.line("# Ouroboros configuration, written by Ouroboros.")
	w.line("# Sizes: B, KB, MB, GB, TB (powers of 1024). Durations: 30m, 6h, 7d.")
	w.line("# Use single quotes for Windows paths so backslashes are taken literally.")
	w.line("")
	w.line("[repository]")
	w.kv("path", str(c.Repository.Path), "directory on the dedicated partition")
	w.kv("restic_path", str(c.Repository.ResticPath), "empty = find restic.exe on PATH")
	w.kv("compression", str(c.Repository.Compression), "off | auto | max")
	w.line("")
	s := c.Space
	w.line("[space]")
	w.kv("min_free", str(s.MinFree.String()), `always leave this free ("50GB" or "10%")`)
	w.kv("max_repo_size", str(s.MaxRepoSize.String()), `optional hard cap, e.g. "1.5TB"; empty = whole partition`)
	w.kv("keep_min_snapshots", integer(s.KeepMinSnapshots), "per job, newest N are never deleted")
	w.kv("protect_newer_than", str(duration(s.ProtectNewerThan)), "snapshots younger than this are never deleted")
	w.kv("growth_factor", float(s.GrowthFactor), "safety multiplier on predicted growth")
	w.kv("first_run_factor", float(s.FirstRunFactor), "multiplier on dry-run size for a job's first backup")
	w.kv("prune_headroom", str(s.PruneHeadroom.String()), "space reserved for prune itself (min 1GB)")
	w.kv("reserve_file_size", str(sizeOrZero(s.ReserveFileSize)), `emergency ballast file; "0" disables`)
	w.kv("prune_interval", str(duration(s.PruneInterval)), "routine prune when there is no space pressure")
	w.line("")
	r := c.Retention
	w.line("# Optional time-based thinning, applied before capacity checks.")
	w.line("# Leave everything at 0 to let capacity alone decide.")
	w.line("[retention]")
	w.kv("keep_hourly", integer(r.KeepHourly), "")
	w.kv("keep_daily", integer(r.KeepDaily), "")
	w.kv("keep_weekly", integer(r.KeepWeekly), "")
	w.kv("keep_monthly", integer(r.KeepMonthly), "")
	w.kv("keep_yearly", integer(r.KeepYearly), "")
	w.line("")
	w.line("[schedule]")
	w.kv("tick", str(duration(c.Schedule.Tick)), "how often the scheduled task wakes up")
	w.kv("retry_after", str(duration(c.Schedule.RetryAfter)), "retry delay after a failed job")
	w.kv("run_on_battery", boolean(c.Schedule.RunOnBattery), "")
	w.line("")
	in := c.Integrity
	w.line("[integrity]")
	w.kv("check_interval", str(duration(in.CheckInterval)), "")
	w.kv("check_read_data_interval", str(duration(in.CheckReadDataInterval)), "")
	w.kv("check_read_data_percent", float(in.CheckReadDataPercent), "")
	w.line("")
	p := c.Performance
	w.line("[performance]")
	w.kv("memory_profile", str(p.MemoryProfile), "low | auto | high")
	w.kv("read_concurrency", integer(p.ReadConcurrency), "0 = from profile")
	w.kv("pack_size_mb", integer(p.PackSizeMB), "0 = from profile")
	w.kv("process_priority", str(p.ProcessPriority), "idle | below_normal | normal")
	w.line("")
	t := c.Timeouts
	w.line("[timeouts]")
	w.line("# Upper bounds that stop a hung restic process; they do not limit normal work.")
	w.kv("backup", str(duration(t.Backup)), "")
	w.kv("prune", str(duration(t.Prune)), "")
	w.kv("check", str(duration(t.Check)), "")
	w.kv("restore", str(duration(t.Restore)), "")
	w.kv("other", str(duration(t.Other)), "init, snapshots, forget, tag")
	w.line("")
	l := c.Logging
	w.line("[logging]")
	w.kv("level", str(l.Level), "debug | info | warn | error")
	w.kv("keep_days", integer(l.KeepDays), "")
	w.kv("event_log", boolean(l.EventLog), "")
	for _, j := range c.Jobs {
		w.line("")
		w.line("[[job]]")
		w.kv("name", str(j.Name), "")
		w.kv("paths", strList(j.Paths), "")
		w.kv("exclude", strList(j.Exclude), "restic patterns; letter case does not matter")
		w.kv("exclude_file", str(j.ExcludeFile), "optional file with one pattern per line")
		w.kv("interval", str(duration(j.Interval)), "")
		w.kv("vss", boolean(j.UseVSS()), "read through a shadow copy, so open files are included")
	}
	return w.buf.Bytes()
}

type tomlWriter struct{ buf bytes.Buffer }

func (w *tomlWriter) line(s string) {
	w.buf.WriteString(s)
	w.buf.WriteByte('\n')
}

// commentColumn aligns trailing comments as in the example file.
const commentColumn = 33

func (w *tomlWriter) kv(key, value, comment string) {
	s := key + " = " + value
	if comment != "" {
		s += strings.Repeat(" ", max(1, commentColumn-len(s))) + "# " + comment
	}
	w.line(s)
}

// str quotes s as a TOML literal string when it can, so Windows paths keep
// their backslashes readable, and as a basic string otherwise.
func str(s string) string {
	literal := !strings.ContainsAny(s, "'")
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			literal = false
		}
	}
	if literal {
		return "'" + s + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func strList(list []string) string {
	q := make([]string, len(list))
	for i, s := range list {
		q[i] = str(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func integer(n int) string { return strconv.Itoa(n) }

// float always writes a decimal point, so the value stays a TOML float.
func float(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

func boolean(b bool) string { return strconv.FormatBool(b) }

// duration writes whole days as "7d" and otherwise drops zero units:
// "15m" rather than "15m0s".
func duration(d Duration) string {
	td := time.Duration(d)
	if td > 0 && td%day == 0 {
		return strconv.FormatInt(int64(td/day), 10) + "d"
	}
	s := td.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// sizeOrZero writes a disabled size as "0" rather than an empty string.
func sizeOrZero(s Size) string {
	if v := s.String(); v != "" {
		return v
	}
	return "0"
}

// Save validates c and replaces the file at path with Marshal(c). The
// previous file, if any, is kept as path + ".bak", so a hand-written file
// and its comments can be recovered. The new file is written to a temporary
// file and renamed, so a crash leaves the old or the new configuration.
func Save(path string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data := Marshal(c)
	// The output must load back to the same configuration; anything else
	// is a bug in Marshal and must not reach the disk.
	back, err := Parse(data)
	if err != nil {
		return fmt.Errorf("configuration does not load back after writing: %w", err)
	}
	if !bytes.Equal(Marshal(back), data) {
		return errors.New("configuration does not load back after writing")
	}
	old, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(old, data) {
			return nil
		}
		if err := writeFileAtomic(path+".bak", old); err != nil {
			return fmt.Errorf("keep a copy of the previous configuration: %w", err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return writeFileAtomic(path, data)
}

// HandEdited reports whether the file at path differs from what Save would
// write for its own values: it has comments, a layout or spellings that
// saving would lose. A file that does not load counts as hand-edited.
func HandEdited(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	c, err := Parse(data)
	if err != nil {
		return true, nil
	}
	return !bytes.Equal(normalizeNewlines(data), Marshal(c)), nil
}

func normalizeNewlines(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
