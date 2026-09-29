package cli

import (
	"github.com/spf13/cobra"

	"easydrop/internal/version"
)

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "easydrop",
		Short:   "PaaS-like Docker deploys to your own Linux hosts",
		Version: version.Version,
	}
	root.AddCommand(initCmd(), deployCmd(), statusCmd(), logsCmd(), rollbackCmd(), mcpServerCmd())
	return root
}

// Execute runs the CLI tree.
func Execute() error {
	return rootCmd().Execute()
}
