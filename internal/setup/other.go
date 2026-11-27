//go:build !windows

package setup

// CleanOld does nothing: Ouroboros is installed only on Windows.
func CleanOld(exe string) {}
