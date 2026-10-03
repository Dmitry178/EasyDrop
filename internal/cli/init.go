package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/deploy"
)

func initCmd() *cobra.Command {
	var (
		force bool
		port  int
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate easydrop.toml from the current directory",
		Long: "Scans the directory — Dockerfile EXPOSE, the compose file, package.json,\n" +
			"the language manifests and, as a last resort, the project's own source for a\n" +
			"listen port — and writes a pre-filled easydrop.toml.\n\n" +
			"Every detected value is reported with its source, and nothing is guessed: when\n" +
			"no port can be found, the key is left out and the report says so. A wrong\n" +
			"app.port does not fail here, it fails later on the target host — a healthcheck\n" +
			"timeout for the single driver, or a 502 from the proxy for compose/swarm.\n" +
			"Use --port when you know the port; it overrides detection.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := deploy.Init(".", deploy.InitOptions{Force: force, Port: port})
			if err != nil {
				return err
			}
			// One shared report, so the MCP server tells an agent exactly what
			// the terminal would have shown the human.
			fmt.Fprint(cmd.OutOrStdout(), res.Report())
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing easydrop.toml (regenerates it from scratch, discarding hand edits)")
	cmd.Flags().IntVar(&port, "port", 0, "the port your app listens on INSIDE the container; overrides detection (omit to detect)")
	return cmd
}
