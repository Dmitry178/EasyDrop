package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/config"
)

func initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate easydrop.toml from the current directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Scaffold(".")
			if err != nil {
				return err
			}
			if err := config.WriteConfig("easydrop.toml", cfg, force); err != nil {
				return err
			}
			fmt.Printf("wrote easydrop.toml (app=%q port=%d driver=%s)\n",
				cfg.App.Name, cfg.App.Port, cfg.Driver.Type)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing easydrop.toml")
	return cmd
}
