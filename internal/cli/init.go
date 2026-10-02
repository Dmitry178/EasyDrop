package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"easydrop/internal/config"
)

func initCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Generate easydrop.toml from the current directory",
		Long: "Scans the directory – Dockerfile EXPOSE, the compose file, package.json,\n" +
			"the language manifests and, as a last resort, the project's own source for a\n" +
			"listen port – and writes a pre-filled easydrop.toml.\n\n" +
			"Every detected value is reported with its source, because a wrong app.port does\n" +
			"not fail here: it fails later, as a healthcheck timeout on the target host.\n" +
			"Refuses to overwrite an existing file; --force replaces it.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := config.Scaffold(".")
			if err != nil {
				return err
			}
			if err := config.WriteConfig("easydrop.toml", res.Config, force); err != nil {
				return err
			}
			printScaffold(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing easydrop.toml")
	return cmd
}

// printScaffold reports what was detected and where it came from, plus the two
// things that reliably need a human afterwards: the target host, and – when no
// port was found – the port itself.
func printScaffold(out io.Writer, res *config.ScaffoldResult) {
	fmt.Fprintf(out, "wrote easydrop.toml\n")
	fmt.Fprintf(out, "  app     %s (%s)\n", res.App.Name, res.AppNameSource)
	if res.PortMissing() {
		fmt.Fprintf(out, "  port    NOT SET – nothing in the project states it\n")
	} else {
		fmt.Fprintf(out, "  port    %d (%s)\n", res.App.Port, res.PortSource)
	}
	if res.Stack != "" {
		fmt.Fprintf(out, "  stack   %s\n", res.Stack)
	}
	fmt.Fprintf(out, "  driver  %s\n", res.Driver.Type)

	if res.PortMissing() {
		fmt.Fprintf(out, "\nACTION REQUIRED: set [app].port in easydrop.toml – the port your app\n")
		fmt.Fprintf(out, "listens on INSIDE the container. Nothing was defaulted on purpose:\n")
		fmt.Fprintf(out, "a wrong port builds fine and only breaks the deploy, which makes for a\n")
		fmt.Fprintf(out, "long debugging session – the single driver times out on the healthcheck,\n")
		if res.Driver.Type == "compose" {
			fmt.Fprintf(out, "and compose/swarm have no HTTP probe at all, so the deploy succeeds and\n")
			fmt.Fprintf(out, "the Nginx proxy simply answers 502.\n")
		} else {
			fmt.Fprintf(out, "and with no domain nothing surfaces it until you try the port.\n")
		}
		fmt.Fprintf(out, "\n  looks for the usual suspects: Dockerfile EXPOSE, compose ports,\n")
		fmt.Fprintf(out, "  an explicit --port flag, PORT= in .env, app.listen()/ListenAndServe().\n")
	}
	if res.Driver.Type == "compose" {
		fmt.Fprintf(out, "      compose: app.port must equal the port %s publishes on the host;\n", res.Driver.ComposeFile)
		fmt.Fprintf(out, "      the Nginx vhost proxies to it.\n")
	}
	fmt.Fprintf(out, "\nnext: set [server] – host and user. init always writes host = \"localhost\".\n")
}
