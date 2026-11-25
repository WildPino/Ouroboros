package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WildPino/ouroboros"
)

func exampleConfig(t *testing.T) *Config {
	t.Helper()
	c, err := Parse(ouroboros.ExampleConfig)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMarshalRoundTrip(t *testing.T) {
	c := exampleConfig(t)
	c.Space.MaxRepoSize = Bytes(1536 << 30)
	c.Space.GrowthFactor = 2
	c.Schedule.Tick = Duration(90 * time.Minute)
	c.Jobs[0].Paths = append(c.Jobs[0].Paths, `C:\Users\me\Bob's files`, "C:\\tab\there", `C:\quote"d`)
	c.Jobs[1].Exclude = nil
	off := false
	c.Jobs[1].VSS = &off

	data := Marshal(c)
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("%v\n%s", err, data)
	}
	if !bytes.Equal(Marshal(back), data) {
		t.Fatalf("not stable:\n%s\n---\n%s", data, Marshal(back))
	}
	if got := back.Jobs[0].Paths; got[2] != `C:\Users\me\Bob's files` || got[3] != "C:\\tab\there" || got[4] != `C:\quote"d` {
		t.Errorf("paths %q", got)
	}
	if back.Space.MaxRepoSize.String() != "1536GB" || back.Space.GrowthFactor != 2 ||
		back.Schedule.Tick != Duration(90*time.Minute) || back.Jobs[1].UseVSS() || len(back.Jobs[1].Exclude) != 0 {
		t.Errorf("values %+v", back)
	}
	for _, want := range []string{
		`path = 'B:\ouroboros'            # directory on the dedicated partition`,
		`tick = '1h30m'`,
		`growth_factor = 2.0`,
		`reserve_file_size = '2GB'`,
		`paths = ['C:\Users\me\Documents', 'D:\Projects', "C:\\Users\\me\\Bob's files", "C:\\tab\u0009here", 'C:\quote"d']`,
		`max_repo_size = '1536GB'`,
		`prune_interval = '7d'`,
	} {
		if !strings.Contains(string(data), want+"\n") && !strings.Contains(string(data), want+" ") {
			t.Errorf("output lacks %q:\n%s", want, data)
		}
	}
}

func TestDurationText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		15 * time.Minute: "15m", 6 * time.Hour: "6h", 90 * time.Minute: "1h30m",
		90 * time.Second: "1m30s", 48 * time.Hour: "2d", 0: "0s",
	} {
		if got := duration(Duration(d)); got != want {
			t.Errorf("duration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestSaveAndHandEdited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ouroboros.toml")
	if err := os.WriteFile(path, ouroboros.ExampleConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if edited, err := HandEdited(path); err != nil || !edited {
		t.Fatalf("example with comments: edited=%v err=%v", edited, err)
	}

	c := exampleConfig(t)
	c.Space.KeepMinSnapshots = 5
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if bak, _ := os.ReadFile(path + ".bak"); !bytes.Equal(bak, ouroboros.ExampleConfig) {
		t.Fatal("previous file not kept as .bak")
	}
	if edited, err := HandEdited(path); err != nil || edited {
		t.Fatalf("after Save: edited=%v err=%v", edited, err)
	}
	if got, err := Load(path); err != nil || got.Space.KeepMinSnapshots != 5 {
		t.Fatalf("load after save: %v", err)
	}

	// CRLF line endings, as an editor may write them, are not an edit.
	data, _ := os.ReadFile(path)
	os.WriteFile(path, bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")), 0o600)
	if edited, _ := HandEdited(path); edited {
		t.Fatal("CRLF counted as a hand edit")
	}

	// An invalid configuration is refused and nothing changes on disk.
	before, _ := os.ReadFile(path)
	c.Jobs = nil
	if err := Save(path, c); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid config: %v", err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("file changed by a refused save")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestEditJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ouroboros.toml")
	if err := os.WriteFile(path, ouroboros.ExampleConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	err := Edit(path, func(c *Config) error {
		c.SetJob(Job{Name: "Photos", Paths: []string{`D:\Photos`}, Interval: Duration(24 * time.Hour)})
		c.SetJob(Job{Name: "DOCUMENTS", Paths: []string{`C:\Docs`}, Interval: Duration(time.Hour)})
		return c.RemoveJob("Data-Volume")
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Jobs) != 2 || c.Jobs[0].Name != "DOCUMENTS" || c.Jobs[0].Paths[0] != `C:\Docs` || c.Jobs[1].Name != "Photos" {
		t.Fatalf("jobs %+v", c.Jobs)
	}

	if err := Edit(path, func(c *Config) error { return c.RemoveJob("nope") }); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("missing job: %v", err)
	}
	err = Edit(path, func(c *Config) error {
		c.SetJob(Job{Name: "bad name", Paths: []string{"relative"}})
		return nil
	})
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 3 {
		t.Fatalf("invalid job: %v", err)
	}
	if c2, _ := Load(path); len(c2.Jobs) != 2 {
		t.Fatal("invalid edit was saved")
	}
}
