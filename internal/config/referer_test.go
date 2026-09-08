package config

import "testing"

func TestAllowedDomainNamesFromEnvironment(t *testing.T) {
	t.Setenv("KAVEN_ALLOWED_DOMAIN_NAMES", `["EXAMPLE.com"]`)
	domains, err := AllowedDomainNamesFromEnvironment()
	if err != nil || len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("domains = %v, error = %v", domains, err)
	}
	t.Setenv("KAVEN_ALLOWED_DOMAIN_NAMES", `"example.com"`)
	if _, err := AllowedDomainNamesFromEnvironment(); err == nil {
		t.Fatal("accepted non-array setting")
	}
}
