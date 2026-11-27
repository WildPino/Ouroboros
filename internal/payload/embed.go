//go:build payload

package payload

import (
	"embed"
	"io/fs"
)

//go:embed files
var embedded embed.FS

// Embedded returns the files built into this executable.
func Embedded() (*Payload, error) {
	sub, err := fs.Sub(embedded, "files")
	if err != nil {
		return nil, err
	}
	return Load(sub)
}
