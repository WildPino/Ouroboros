// Command ouroboros keeps space-aware, versioned restic backups on Windows.
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("ouroboros", version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: ouroboros <command>")
	os.Exit(1)
}
