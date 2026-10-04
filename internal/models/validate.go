package models

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// appNamePattern enforces docker-compatible lowercase names up front so a
// bad `app.name` fails fast with a clear error instead of a cryptic daemon
// rejection deep inside drivers or builders.
var appNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// DomainPattern allows hostnames and single-level wildcards (`*.example.com`).
// It is the canonical shape for anything that reaches an nginx `server_name` or
// a shell command, so the config parser and the infra layer share it rather
// than each keeping a copy that can drift.
var DomainPattern = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]+(?:[.-][A-Za-z0-9]+)*$`)

// ValidateCertifiableDomain accepts only what a certificate can meaningfully
// name: a hostname or an IP literal.
//
// It is deliberately narrower than DomainPattern. A wildcard passes nginx's
// server_name check but is useless in a self-signed leaf – `*.example.com` does
// not match the bare `example.com` a developer actually types, and browsers
// reject a wildcard outside the subject's own domain. Refusing beats issuing a
// certificate that cannot be used.
func ValidateCertifiableDomain(domain string) error {
	d := strings.TrimSpace(domain)
	if d == "" {
		return fmt.Errorf("empty domain: nothing to certify")
	}
	if strings.HasPrefix(d, "*.") {
		return fmt.Errorf("wildcard domain %q cannot be self-signed: a wildcard matches only "+
			"one label deep and browsers reject it outside the issuing domain's own domain; "+
			"use the bare hostname", d)
	}
	if net.ParseIP(d) != nil {
		return nil
	}
	if !DomainPattern.MatchString(d) {
		return fmt.Errorf("invalid domain %q: must be a hostname (letters, digits, dots, hyphens) "+
			"or an IP literal", d)
	}
	return nil
}

// ValidateAppName rejects empty names and anything outside the
// docker-compatible lowercase set.
func ValidateAppName(name string) error {
	if !appNamePattern.MatchString(name) {
		return fmt.Errorf("invalid app.name %q: must match %s (lowercase, docker-compatible)", name, appNamePattern)
	}
	return nil
}
