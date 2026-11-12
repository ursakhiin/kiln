/*
Copyright © 2026 Peter Rauhut
*/
package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the Kiln daemon for this repo",
	Long: `Run the Kiln daemon for this repo. The daemon is the only writer of
.kiln/ data and serves the web GUI, the CLI's API and the MCP server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("serve: not implemented yet")
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
