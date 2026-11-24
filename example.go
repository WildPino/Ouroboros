// Package ouroboros holds files that are embedded into the ouroboros binary.
package ouroboros

import _ "embed"

// ExampleConfig is the annotated example configuration printed by
// "ouroboros config init".
//
//go:embed ouroboros.example.toml
var ExampleConfig []byte
