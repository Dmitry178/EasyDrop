package core

import (
	"os"
	"strings"
)

// StagingBase returns the host staging root shared by builder and infra.
// EASYDROP_STAGING_BASE overrides the default /tmp/easydrop for hosts where
// /tmp is unsuitable (tiny tmpfs, noexec, or container-confined daemons such
// as snap-docker that cannot see the host /tmp). Trailing slashes are trimmed.
func StagingBase() string {
	if base := os.Getenv("EASYDROP_STAGING_BASE"); base != "" {
		return strings.TrimRight(base, "/")
	}
	return "/tmp/easydrop"
}
