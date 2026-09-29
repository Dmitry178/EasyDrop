package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"easydrop/internal/core"
	"easydrop/internal/core/bootstrapper"
	"easydrop/internal/core/builder"
	"easydrop/internal/core/drivers"
	"easydrop/internal/core/infra"
	"easydrop/internal/models"
)

func deployCmd() *cobra.Command {
	var configPath string
	var noCache, blueGreen, skipBootstrap bool
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build and deploy the app to the target host",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDeploy(configPath, noCache, blueGreen, skipBootstrap)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "bypass the Docker build cache")
	cmd.Flags().BoolVar(&blueGreen, "blue-green", false, "zero-downtime Blue-Green swap (keeps a rollback backup)")
	cmd.Flags().BoolVar(&skipBootstrap, "skip-bootstrap", false, "skip host provisioning (docker already installed, privileges arranged)")
	return cmd
}

// runDeploy wires the full pipeline: parse → executor → bootstrap → build →
// single deploy (+ ingress) → SSL. SrcDir is the config file's directory.
func runDeploy(configPath string, noCache, blueGreen, skipBootstrap bool) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	applyDeployFlags(cfg, noCache, blueGreen)
	if cfg.Driver.Type != "single" {
		return fmt.Errorf("driver type %q is not implemented yet (single only)", cfg.Driver.Type)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ex, err := core.NewExecutor(&cfg.Server)
	if err != nil {
		return err
	}
	defer ex.Close()

	app := &models.Application{Config: cfg}

	fmt.Println("easydrop: bootstrapping host...")
	if !skipBootstrap {
		if err := bootstrapper.New(ex).Bootstrap(ctx); err != nil {
			return err
		}
	} else {
		fmt.Println("easydrop: skipping host bootstrap (--skip-bootstrap)")
	}

	abs, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	rb := builder.NewRemoteBuilder(ex)
	rb.SrcDir = filepath.Dir(abs)
	if err := rb.Build(ctx, app); err != nil {
		return err
	}

	sd := drivers.NewSingleDriver(ex)
	sd.BlueGreen = cfg.Driver.BlueGreen
	if domain := cfg.Nginx.Domain; domain != "" {
		sd.Ingress = infra.NewNginxManager(ex)
	}
	if err := sd.Deploy(ctx, app); err != nil {
		return err
	}

	if cfg.Nginx.SSL && cfg.Nginx.Domain != "" {
		cm := infra.NewCertbotManager(ex, cfg.Server.Host)
		if err := cm.EnableSSL(ctx, cfg.Nginx.Domain, cfg.Nginx.Email); err != nil {
			return err
		}
	}

	fmt.Printf("easydrop: %s deployed\n", cfg.App.Name)
	return nil
}
