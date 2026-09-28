package infra

import (
	"fmt"
	"regexp"
)

// domainPattern allows hostnames (and single-level wildcards like
// *.example.com for future use); anything else is rejected before it can
// reach a shell command.
var domainPattern = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*$`)

func validateDomain(domain string) error {
	if !domainPattern.MatchString(domain) {
		return fmt.Errorf("invalid domain %q: must be a hostname (letters, digits, dots, hyphens, optional leading *.)", domain)
	}
	return nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port %d: must be 1-65535", port)
	}
	return nil
}

// isLocalHost reports whether managed-host shortcuts apply (no Certbot,
// no firewall assumptions): localhost deployments.
func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1"
}
