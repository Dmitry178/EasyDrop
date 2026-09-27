package models

import "testing"

func TestValidateAppName(t *testing.T) {
	for _, good := range []string{"my-api", "api", "a1", "my.awesome_api-2"} {
		if err := ValidateAppName(good); err != nil {
			t.Errorf("ValidateAppName(%q) unexpected error: %v", good, err)
		}
	}
	for _, bad := range []string{"", "My-API", "my api", "a/b", "-lead", "trail-", "UPPER", "a..b"} {
		if err := ValidateAppName(bad); err == nil {
			t.Errorf("ValidateAppName(%q) expected error, got nil", bad)
		}
	}
}
