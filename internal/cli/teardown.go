package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/deploy"
)

func teardownCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "teardown [app_name]",
		Short: "Remove the deployment (containers, backups, stack state)",
		Long: "Removes the deployed containers (including the Blue-Green rollback backup) " +
			"and, for Compose/Swarm, the stack and its state directory. " +
			"Named volumes are NEVER deleted – data outlives the deployment. " +
			"Idempotent: tearing down an app that is not deployed is not an error.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			if err := deploy.Teardown(context.Background(), configPath, arg); err != nil {
				return err
			}
			cfg, err := deploy.LoadConfig(configPath)
			if err != nil {
				return err
			}
			name, _ := deploy.ResolveAppName(arg, cfg)
			fmt.Printf("easydrop: %s torn down (volumes kept)\n", name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	return cmd
}
