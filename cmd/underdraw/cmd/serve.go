/*
Copyright © 2026 Peter Rauhut
*/
package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ursakhiin/underdraw/internal/daemon"
)

// version is the Underdraw version reported to MCP clients.
var version = "dev"

var (
	serveAddr string
	serveRepo string
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the Underdraw daemon for this repo",
	Long: `Run the Underdraw daemon for this repo. The daemon is the only writer of
.underdraw/ data and serves the web GUI, the CLI's API and the MCP server.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repo := serveRepo
		if repo == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			repo, err = daemon.FindRepoRoot(cwd)
			if err != nil {
				return err
			}
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return daemon.Run(ctx, daemon.Config{RepoRoot: repo, Addr: serveAddr, Version: version}, func(addr string) {
			fmt.Fprintf(cmd.OutOrStdout(), "Underdraw is serving %s\n  MCP:  http://%s/mcp\n", repo, addr)
		})
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", daemon.DefaultAddr, "address to listen on")
	serveCmd.Flags().StringVar(&serveRepo, "repo", "", "repo root (default: the git repo containing the current directory)")
	rootCmd.AddCommand(serveCmd)
}
