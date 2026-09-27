package models

import (
	"fmt"
	"regexp"
)

// appNamePattern enforces docker-compatible lowercase names up front so a
// bad `app.name` fails fast with a clear error instead of a cryptic daemon
// rejection deep inside drivers or builders.
var appNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// ValidateAppName rejects empty names and anything outside the
// docker-compatible lowercase set.
func ValidateAppName(name string) error {
	if !appNamePattern.MatchString(name) {
		return fmt.Errorf("invalid app.name %q: must match %s (lowercase, docker-compatible)", name, appNamePattern)
	}
	return nil
}
