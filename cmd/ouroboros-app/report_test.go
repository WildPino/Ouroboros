package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/WildPino/ouroboros/internal/payload"
)

func testPayload(t *testing.T, licenseHash string) *payload.Payload {
	t.Helper()
	fsys := fstest.MapFS{}
	var entries []string
	for _, f := range []struct{ path, version, data string }{
		{"bin/ouroboros.exe", "v0.2.0", "cli"},
		{"LICENSE", "", "MIT"},
	} {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		w.Write([]byte(f.data))
		w.Close()
		fsys[f.path+".gz"] = &fstest.MapFile{Data: b.Bytes()}
		s := sha256.Sum256([]byte(f.data))
		h := hex.EncodeToString(s[:])
		if f.path == "LICENSE" && licenseHash != "" {
			h = licenseHash
		}
		entries = append(entries, fmt.Sprintf(`{"path":%q,"version":%q,"size":%d,"sha256":%q}`, f.path, f.version, len(f.data), h))
	}
	fsys[payload.ManifestName] = &fstest.MapFile{Data: []byte(`{"files":[` + strings.Join(entries, ",") + `]}`)}
	p, err := payload.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWriteEmbedded(t *testing.T) {
	var b bytes.Buffer
	if !writeEmbedded(&b, testPayload(t, ""), nil) {
		t.Fatalf("check failed:\n%s", b.String())
	}
	out := b.String()
	for _, want := range []string{"Ouroboros dev\n", "bin/ouroboros.exe  v0.2.0  3 bytes", "LICENSE            -       3 bytes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "  ok\n") != 2 {
		t.Errorf("want two ok lines:\n%s", out)
	}
}

func TestWriteEmbeddedFailures(t *testing.T) {
	var b bytes.Buffer
	if writeEmbedded(&b, testPayload(t, strings.Repeat("0", 64)), nil) {
		t.Fatalf("check passed with a wrong checksum:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "LICENSE") || !strings.Contains(b.String(), "FAILED: ") {
		t.Errorf("output does not report the failure:\n%s", b.String())
	}

	b.Reset()
	if writeEmbedded(&b, nil, payload.ErrNotEmbedded) {
		t.Fatal("check passed without embedded files")
	}
	if !strings.Contains(b.String(), "error: this build embeds no files") {
		t.Errorf("output = %q", b.String())
	}
}
