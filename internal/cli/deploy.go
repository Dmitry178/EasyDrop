package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"easydrop/internal/deploy"
)

func deployCmd() *cobra.Command {
	var configPath string
	var noCache, blueGreen, skipBootstrap bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build and deploy the app to the target host",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return deploy.Run(ctx, deploy.Options{
				ConfigPath:    configPath,
				NoCache:       noCache,
				BlueGreen:     blueGreen,
				SkipBootstrap: skipBootstrap,
			}, func(s string) { fmt.Println("easydrop: " + s) })
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "bypass the Docker build cache")
	cmd.Flags().BoolVar(&blueGreen, "blue-green", false, "zero-downtime Blue-Green swap (keeps a rollback backup)")
	cmd.Flags().BoolVar(&skipBootstrap, "skip-bootstrap", false, "skip host provisioning (docker already installed, privileges arranged)")
	return cmd
}
