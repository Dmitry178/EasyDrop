// Command easydrop is the single EasyDrop binary: CLI by default,
// MCP stdio server via `easydrop mcp-server` (Milestone 8).
package main

import (
	"fmt"
	"os"

	"easydrop/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "easydrop: error: %v\n", err)
		os.Exit(1)
	}
}
