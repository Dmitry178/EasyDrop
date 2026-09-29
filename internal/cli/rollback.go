package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/deploy"
)

func rollbackCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "rollback [app_name]",
		Short: "Restore the backup kept by the last Blue-Green deploy",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			if err := deploy.Rollback(context.Background(), configPath, arg); err != nil {
				return err
			}
			cfg, err := deploy.LoadConfig(configPath)
			if err != nil {
				return err
			}
			name, _ := deploy.ResolveAppName(arg, cfg)
			fmt.Printf("easydrop: %s rolled back\n", name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	return cmd
}
