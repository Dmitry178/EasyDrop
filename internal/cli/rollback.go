package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/core"
	"easydrop/internal/core/drivers"
	"easydrop/internal/models"
)

func rollbackCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "rollback [app_name]",
		Short: "Restore the backup kept by the last Blue-Green deploy",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			name, err := resolveAppName(arg, cfg)
			if err != nil {
				return err
			}
			cfg.App.Name = name // operate on the requested app
			ex, err := core.NewExecutor(&cfg.Server)
			if err != nil {
				return err
			}
			defer ex.Close()
			app := &models.Application{Config: cfg}
			if err := drivers.NewSingleDriver(ex).Rollback(context.Background(), app); err != nil {
				return err
			}
			fmt.Printf("easydrop: %s rolled back\n", cfg.App.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	return cmd
}
