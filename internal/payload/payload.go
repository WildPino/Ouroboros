// Package payload reads the files that Ouroboros.exe carries for
// installation: the command line, restic and the licenses, each stored
// gzip-compressed next to a manifest of their sizes and SHA-256 (DESIGN.md
// §12.8, question 3).
//
// build.ps1 stages the files in the files directory and builds with the
// payload tag; other builds embed nothing and Embedded reports
// ErrNotEmbedded.
package payload

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"path"
	"strings"
)

// ManifestName is the manifest's name in the payload file system.
const ManifestName = "manifest.json"

// ErrNotEmbedded reports a build that carries no files, such as a test
// build.
var ErrNotEmbedded = errors.New("this build embeds no files")

// ErrChecksum reports a file whose content does not match the manifest.
var ErrChecksum = errors.New("embedded file does not match its checksum")

// File describes one embedded file. Its compressed content is stored at
// Path + ".gz".
type File struct {
	// Path is slash-separated and relative to the installation folder,
	// such as "bin/ouroboros.exe".
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type manifest struct {
	Files []File `json:"files"`
}

// Payload is a set of embedded files.
type Payload struct {
	fsys  fs.FS
	Files []File
}

// Load reads and validates the manifest of fsys.
func Load(fsys fs.FS) (*Payload, error) {
	data, err := fs.ReadFile(fsys, ManifestName)
	if err != nil {
		return nil, fmt.Errorf("read payload manifest: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse payload manifest: %w", err)
	}
	if len(m.Files) == 0 {
		return nil, errors.New("payload manifest lists no files")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if err := checkFile(f); err != nil {
			return nil, err
		}
		// Installation folders ignore case, like every Windows path.
		key := strings.ToLower(f.Path)
		if seen[key] {
			return nil, fmt.Errorf("payload manifest lists %s twice", f.Path)
		}
		seen[key] = true
	}
	return &Payload{fsys: fsys, Files: m.Files}, nil
}

func checkFile(f File) error {
	if !fs.ValidPath(f.Path) || f.Path == "." || path.Clean(f.Path) != f.Path ||
		strings.ContainsAny(f.Path, `\:`) {
		return fmt.Errorf("payload manifest: invalid path %q", f.Path)
	}
	if f.Size < 0 {
		return fmt.Errorf("payload manifest: %s has a negative size", f.Path)
	}
	if b, err := hex.DecodeString(f.SHA256); err != nil || len(b) != sha256.Size || strings.ToLower(f.SHA256) != f.SHA256 {
		return fmt.Errorf("payload manifest: %s has an invalid SHA-256 %q", f.Path, f.SHA256)
	}
	return nil
}

// Find returns the file installed at p, compared case-insensitively.
func (p *Payload) Find(name string) (File, bool) {
	for _, f := range p.Files {
		if strings.EqualFold(f.Path, name) {
			return f, true
		}
	}
	return File{}, false
}

// Open returns the decompressed content of f. The reader returns
// ErrChecksum, instead of io.EOF, if the content differs from the
// manifest, and stops as soon as it exceeds the expected size.
func (p *Payload) Open(f File) (io.ReadCloser, error) {
	raw, err := p.fsys.Open(f.Path + ".gz")
	if err != nil {
		return nil, fmt.Errorf("open embedded %s: %w", f.Path, err)
	}
	zr, err := gzip.NewReader(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("open embedded %s: %w", f.Path, err)
	}
	return &checkedReader{f: f, raw: raw, zr: zr, h: sha256.New()}, nil
}

// Verify reads the whole of f and checks it against the manifest.
func (p *Payload) Verify(f File) error {
	r, err := p.Open(f)
	if err != nil {
		return err
	}
	defer r.Close()
	_, err = io.Copy(io.Discard, r)
	return err
}

type checkedReader struct {
	f   File
	raw fs.File
	zr  *gzip.Reader
	h   hash.Hash
	n   int64
	err error
}

func (r *checkedReader) Read(b []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.zr.Read(b)
	r.h.Write(b[:n])
	r.n += int64(n)
	if r.n > r.f.Size {
		r.err = fmt.Errorf("%s: %w (larger than %d bytes)", r.f.Path, ErrChecksum, r.f.Size)
		return 0, r.err
	}
	if err == io.EOF {
		if r.n != r.f.Size || hex.EncodeToString(r.h.Sum(nil)) != r.f.SHA256 {
			r.err = fmt.Errorf("%s: %w", r.f.Path, ErrChecksum)
			return n, r.err
		}
		r.err = io.EOF
		return n, io.EOF
	}
	if err != nil {
		r.err = fmt.Errorf("read embedded %s: %w", r.f.Path, err)
		return n, r.err
	}
	return n, nil
}

func (r *checkedReader) Close() error {
	r.zr.Close()
	return r.raw.Close()
}
