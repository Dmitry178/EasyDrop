package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/core"
	"easydrop/internal/core/drivers"
)

func statusCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the deployed app status",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := loadConfig(configPath)
			if err != nil {
				return err
			}
			ex, err := core.NewExecutor(&cfg.Server)
			if err != nil {
				return err
			}
			defer ex.Close()
			st, err := drivers.NewSingleDriver(ex).Status(context.Background(), cfg.App.Name)
			if err != nil {
				return err
			}
			fmt.Printf("App:    %s\nStatus: %s\nUptime: %s\n", cfg.App.Name, st.Status, displayOrDash(st.Uptime))
			if st.SSLStatus != "" {
				fmt.Printf("SSL:    %s\n", st.SSLStatus)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	return cmd
}

func displayOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
