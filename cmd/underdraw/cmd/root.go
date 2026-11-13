/*
Copyright © 2026 Peter Rauhut
*/
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "underdraw",
	// Runtime errors shouldn't print the usage text.
	SilenceUsage: true,
	Short:        "Plan and track projects with your AI agents",
	Long: `Underdraw turns rough ideas into agent-ready work. Projects, features and
tasks live in .underdraw/ in your repo; you manage them through the web GUI or
this CLI, and agents work with them through Underdraw's MCP server.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
