// Package version carries the single EasyDrop version shared by the CLI
// (--version) and the MCP server (Implementation.Version).
package version

// Version is the fallback stamped into the binary when no ldflags override is
// passed, which is what plain `go build` and the test suite see. A release build
// overrides it:
// -ldflags "-X easydrop/internal/version.Version=x.y.z".
//
// `make build` derives VERSION from `git describe --tags`, so a tagged tree
// wins over this value; until a tag exists it stamps the commit hash instead.
var Version = "1.0.0"
