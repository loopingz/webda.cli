package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

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

func newVersionCmd(baseURL string, serverInfo *ServerInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show CLI and server version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("%s version %s\n", cmd.Root().Use, formatVersion(version, commit, buildDate))
			if serverInfo != nil && serverInfo.ServerVersion != "" {
				fmt.Printf("server: %s (%s)\n", serverInfo.ServerVersion, baseURL)
			}
			return nil
		},
	}
}
