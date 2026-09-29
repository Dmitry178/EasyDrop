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

func logsCmd() *cobra.Command {
	var configPath string
	var follow bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs [app_name]",
		Short: "Show or stream container logs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			ctx := context.Background()
			if follow {
				var stop context.CancelFunc
				ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
				defer stop()
			}
			lines, err := deploy.Logs(ctx, configPath, arg, tail, follow, 0)
			if err != nil {
				return err
			}
			for _, line := range lines {
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream live logs")
	cmd.Flags().IntVarP(&tail, "tail", "n", 100, "lines of history to show")
	return cmd
}
