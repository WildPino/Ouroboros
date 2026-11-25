package restic

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/WildPino/ouroboros/internal/backend"
)

// parseLsLine decodes one line of "restic ls --json"; ok is false for the
// snapshot line that comes first.
func parseLsLine(line []byte) (backend.Node, bool, error) {
	var n struct {
		MessageType string    `json:"message_type"`
		StructType  string    `json:"struct_type"`
		Name        string    `json:"name"`
		Type        string    `json:"type"`
		Path        string    `json:"path"`
		Size        uint64    `json:"size"`
		Mtime       time.Time `json:"mtime"`
	}
	if err := json.Unmarshal(line, &n); err != nil {
		return backend.Node{}, false, fmt.Errorf("unexpected ls output %q: %w", truncate(line), err)
	}
	if n.MessageType != "node" && n.StructType != "node" {
		return backend.Node{}, false, nil
	}
	return backend.Node{Name: n.Name, Type: n.Type, Path: n.Path, Size: n.Size, ModTime: n.Mtime}, true, nil
}

// SnapshotPath turns a Windows path into the form snapshots store it in:
// C:\Users\me becomes /C/Users/me and C:\ becomes /C. Paths already in that
// form, and "/", keep their form; a trailing slash is dropped.
func SnapshotPath(p string) string {
	p = includePattern(p)
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	if p == "" {
		return "/"
	}
	return p
}

// WindowsPath is the inverse of SnapshotPath for paths that start with a
// drive: /C/Users/me becomes C:\Users\me and /C becomes C:\. Other paths
// only get backslashes.
func WindowsPath(p string) string {
	if len(p) >= 2 && p[0] == '/' && isASCIILetter(p[1]) && (len(p) == 2 || p[2] == '/') {
		rest := strings.ReplaceAll(p[2:], "/", `\`)
		if rest == "" {
			rest = `\`
		}
		return p[1:2] + ":" + rest
	}
	return strings.ReplaceAll(p, "/", `\`)
}
