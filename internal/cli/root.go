package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/config"
	"easydrop/internal/models"
)

// version is the CLI version (overridable via ldflags: -X easydrop/internal/cli.version=...).
var version = "0.1.0"

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "easydrop",
		Short:   "PaaS-like Docker deploys to your own Linux hosts",
		Version: version,
	}
	root.AddCommand(initCmd(), deployCmd(), statusCmd(), logsCmd(), rollbackCmd())
	return root
}

// Execute runs the CLI tree.
func Execute() error {
	return rootCmd().Execute()
}

// loadConfig parses the TOML at path (cobra --config default handled by callers).
func loadConfig(path string) (*models.Config, error) {
	cfg, err := config.ParseConfig(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveAppName returns the CLI arg or falls back to app.name from config.
func resolveAppName(arg string, cfg *models.Config) (string, error) {
	if arg != "" {
		if err := models.ValidateAppName(arg); err != nil {
			return "", err
		}
		return arg, nil
	}
	if cfg == nil || cfg.App.Name == "" {
		return "", fmt.Errorf("no app name: pass [app_name] or set app.name in easydrop.toml")
	}
	return cfg.App.Name, nil
}

// applyDeployFlags overlays per-run CLI flags onto the parsed config.
func applyDeployFlags(cfg *models.Config, noCache, blueGreen bool) {
	if noCache {
		cfg.Build.NoCache = true
	}
	if blueGreen {
		cfg.Driver.BlueGreen = true
	}
}
