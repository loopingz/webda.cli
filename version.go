package main

import "fmt"

// Set at build time via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

// formatVersion returns the formatted version string.
func formatVersion(ver, comm, date string) string {
	return fmt.Sprintf("%s (commit: %s, built: %s)", ver, comm, date)
}
