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
	var grep string
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
			res, err := deploy.Logs(ctx, configPath, arg, deploy.LogOptions{
				Lines: tail, Follow: follow, Filter: grep,
			})
			if err != nil {
				return err
			}
			for _, line := range res.Lines {
				fmt.Println(line)
			}
			// Silence is ambiguous: an empty read must say whether the container
			// was quiet or the filter simply matched nothing.
			if len(res.Lines) == 0 {
				if res.Scanned == 0 {
					fmt.Fprintln(os.Stderr, "no log lines available (container may not be running, or its logs are empty)")
				} else {
					fmt.Fprintf(os.Stderr, "no lines matched (scanned %d lines – widen --tail or loosen --grep)\n", res.Scanned)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream live logs")
	cmd.Flags().IntVarP(&tail, "tail", "n", 100, "lines of history to scan")
	cmd.Flags().StringVarP(&grep, "grep", "g", "", "case-insensitive regular expression; print only matching lines (e.g. -g 'error|panic|fatal')")
	return cmd
}
