package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"easydrop/internal/deploy"
)

func teardownCmd() *cobra.Command {
	var configPath string
	var purge bool
	cmd := &cobra.Command{
		Use:   "teardown [app_name]",
		Short: "Remove the deployment (containers, ingress, backups, stack state)",
		Long: "Removes the deployed containers (including the Blue-Green rollback backup) " +
			"and, for Compose/Swarm, the stack and its state directory. When the app has an " +
			"nginx.domain, the managed vhost is removed and nginx reloaded as well – leaving it " +
			"behind would keep the domain answering 502 against a port with no container on it. " +
			"\n\nNamed volumes are NEVER deleted – data outlives the deployment. " +
			"A Let's Encrypt certificate is never deleted either: certbot owns it. " +
			"Idempotent: tearing down an app that is not deployed is not an error.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			res, err := deploy.Teardown(context.Background(), configPath, arg,
				deploy.TeardownOptions{Purge: purge})
			if err != nil {
				return err
			}
			fmt.Printf("easydrop: %s torn down (named volumes kept)\n", res.AppName)
			if res.IngressNote != "" {
				fmt.Printf("easydrop: %s\n", res.IngressNote)
			}
			if res.CertPurged {
				fmt.Println("easydrop: self-signed certificate deleted; the next deploy issues a new one")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "./easydrop.toml", "path to easydrop.toml")
	cmd.Flags().BoolVar(&purge, "purge", false,
		"also delete the self-signed certificate for nginx.domain (a Let's Encrypt certificate is never touched)")
	return cmd
}
