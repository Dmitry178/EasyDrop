package cli

import (
	"testing"
)

func TestRootHasAllCommands(t *testing.T) {
	root := rootCmd()
	want := map[string]bool{"init": false, "deploy": false, "status": false, "logs": false, "rollback": false, "teardown": false, "mcp-server": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root command missing %q", name)
		}
	}
}
