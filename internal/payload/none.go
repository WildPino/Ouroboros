//go:build !payload

package payload

// Embedded returns ErrNotEmbedded: this build carries no files.
func Embedded() (*Payload, error) {
	return nil, ErrNotEmbedded
}
