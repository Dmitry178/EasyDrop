package templates

import _ "embed"

// NginxConf is the embedded reverse-proxy blueprint rendered by
// internal/core/infra with Domain + Port. Embedded (not read from disk) so
// the single binary (NFR-03) carries it everywhere.
var (
	//go:embed nginx.conf.tmpl
	NginxConf string
)
