// Package version carries the single EasyDrop version shared by the CLI
// (--version) and the MCP server (Implementation.Version).
package version

// Version is overridden at release time via:
// -ldflags "-X easydrop/internal/version.Version=x.y.z".
var Version = "0.1.0"
