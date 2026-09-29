package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"easydrop/internal/mcp"
)

func mcpServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp-server",
		Short: "Serve the Model Context Protocol over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return mcp.Run(ctx)
		},
	}
}
