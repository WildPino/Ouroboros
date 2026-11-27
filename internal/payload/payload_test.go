package payload

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

func gz(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// build returns a file system holding content under each path, with a
// manifest describing it; edit may change the manifest entries first.
func build(t *testing.T, content map[string]string, edit func([]File)) fstest.MapFS {
	t.Helper()
	fsys := fstest.MapFS{}
	var files []File
	for _, p := range []string{"bin/ouroboros.exe", "restic/restic.exe", "LICENSE"} {
		data, ok := content[p]
		if !ok {
			continue
		}
		fsys[p+".gz"] = &fstest.MapFile{Data: gz(t, []byte(data))}
		files = append(files, File{Path: p, Version: "v1", Size: int64(len(data)), SHA256: sum([]byte(data))})
	}
	if edit != nil {
		edit(files)
	}
	m, err := json.Marshal(manifest{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	fsys[ManifestName] = &fstest.MapFile{Data: m}
	return fsys
}

var sample = map[string]string{
	"bin/ouroboros.exe": "command line",
	"restic/restic.exe": strings.Repeat("restic ", 10000),
	"LICENSE":           "MIT",
}

func TestLoadAndVerify(t *testing.T) {
	p, err := Load(build(t, sample, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 3 {
		t.Fatalf("files = %v", p.Files)
	}
	for _, f := range p.Files {
		if err := p.Verify(f); err != nil {
			t.Errorf("Verify(%s) = %v", f.Path, err)
		}
	}
	f, ok := p.Find(`BIN/Ouroboros.EXE`)
	if !ok || f.Path != "bin/ouroboros.exe" {
		t.Fatalf("Find = %v, %v", f, ok)
	}
	r, err := p.Open(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "command line" {
		t.Fatalf("content = %q, %v", got, err)
	}
	if _, ok := p.Find("restic.exe"); ok {
		t.Error("Find matched a file in another folder")
	}
}

func TestVerifyMismatch(t *testing.T) {
	tests := []struct {
		name string
		edit func([]File)
	}{
		{"wrong hash", func(f []File) { f[0].SHA256 = sum([]byte("other")) }},
		{"shorter than recorded", func(f []File) { f[0].Size++ }},
		{"longer than recorded", func(f []File) { f[0].Size-- }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Load(build(t, sample, tt.edit))
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Verify(p.Files[0]); !errors.Is(err, ErrChecksum) {
				t.Fatalf("Verify = %v, want ErrChecksum", err)
			}
		})
	}
}

func TestVerifyCorrupt(t *testing.T) {
	fsys := build(t, sample, nil)
	fsys["LICENSE.gz"] = &fstest.MapFile{Data: []byte("not gzip")}
	p, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := p.Find("LICENSE")
	if err := p.Verify(f); err == nil {
		t.Fatal("Verify accepted a corrupt file")
	}
	delete(fsys, "LICENSE.gz")
	if err := p.Verify(f); err == nil {
		t.Fatal("Verify accepted a missing file")
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name string
		edit func([]File)
	}{
		{"parent path", func(f []File) { f[0].Path = "../ouroboros.exe" }},
		{"absolute path", func(f []File) { f[0].Path = "/bin/ouroboros.exe" }},
		{"backslash", func(f []File) { f[0].Path = `bin\ouroboros.exe` }},
		{"drive", func(f []File) { f[0].Path = "C:/ouroboros.exe" }},
		{"unclean", func(f []File) { f[0].Path = "bin/./ouroboros.exe" }},
		{"empty", func(f []File) { f[0].Path = "" }},
		{"duplicate ignoring case", func(f []File) { f[1].Path = "BIN/ouroboros.exe" }},
		{"short hash", func(f []File) { f[0].SHA256 = "abcd" }},
		{"upper case hash", func(f []File) { f[0].SHA256 = strings.ToUpper(f[0].SHA256) }},
		{"negative size", func(f []File) { f[0].Size = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(build(t, sample, tt.edit)); err == nil {
				t.Fatal("Load accepted the manifest")
			}
		})
	}
}

func TestLoadManifestErrors(t *testing.T) {
	for name, data := range map[string]string{
		"missing":       "",
		"not json":      "{",
		"unknown field": `{"files": [], "extra": 1}`,
		"no files":      `{"files": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			if data != "" {
				fsys[ManifestName] = &fstest.MapFile{Data: []byte(data)}
			}
			if _, err := Load(fsys); err == nil {
				t.Fatal("Load accepted the manifest")
			}
		})
	}
}

func TestNotEmbedded(t *testing.T) {
	if _, err := Embedded(); !errors.Is(err, ErrNotEmbedded) {
		t.Fatalf("Embedded = %v, want ErrNotEmbedded in a test build", err)
	}
}
