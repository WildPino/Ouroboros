//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Ouroboros.exe runs only on Windows")
	os.Exit(1)
}
