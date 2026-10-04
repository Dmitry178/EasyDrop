package templates

import _ "embed"

// NginxConf is the embedded reverse-proxy blueprint rendered by
// internal/core/infra with Domain + Port. Embedded (not read from disk) so
// the single binary (NFR-03) carries it everywhere.
var (
	//go:embed nginx.conf.tmpl
	NginxConf string
)

// NginxTLSConf is the self-signed variant (M18), rendered with Domain + Port +
// CertPath + KeyPath. It exists as a separate template rather than a conditional
// inside NginxConf because the two have different contracts: the plain one must
// never reference a certificate (certbot writes the 443 block itself), and a
// branch in the shared template is how that invariant would eventually be
// broken by an innocuous edit.
var (
	//go:embed nginx-tls.conf.tmpl
	NginxTLSConf string
)
