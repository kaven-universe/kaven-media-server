package config

import "testing"

func TestValidateRuntimeSettingsRejectsInvalidDomain(t *testing.T) {
	settings := DefaultRuntimeSettings(t.TempDir())
	settings.AllowedDomainNames = []string{"https://example.com/path"}
	if err := ValidateRuntimeSettings(settings); err == nil {
		t.Fatal("accepted invalid allowed domain")
	}
}
