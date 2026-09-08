package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAdminCredentialsDisabledByDefault(t *testing.T) {
	credentials, err := LoadAdminCredentials(environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Enabled || credentials.Username != DefaultAdminUsername || credentials.Password != nil {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestLoadAdminCredentialsFromEnvironmentPassword(t *testing.T) {
	password := "correct horse battery staple"
	credentials, err := LoadAdminCredentials(environment(map[string]string{
		"KAVEN_ADMIN_USERNAME": "media-admin",
		"KAVEN_ADMIN_PASSWORD": password,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !credentials.Enabled || credentials.Username != "media-admin" || string(credentials.Password) != password {
		t.Fatalf("credentials = %#v", credentials)
	}
	backing := credentials.Password
	ClearAdminPassword(&credentials)
	if credentials.Password != nil || !bytes.Equal(backing, make([]byte, len(backing))) {
		t.Fatal("clearing credentials did not overwrite the password bytes")
	}
}

func TestLoadAdminCredentialsFromFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(filePath, []byte("  long password value  \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentials, err := LoadAdminCredentials(environment(map[string]string{
		"KAVEN_ADMIN_PASSWORD_FILE": filePath,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if string(credentials.Password) != "  long password value  " {
		t.Fatalf("password = %q", credentials.Password)
	}
}

func TestLoadAdminCredentialsRejectsInvalidConfiguration(t *testing.T) {
	directory := t.TempDir()
	largeFile := filepath.Join(directory, "large-secret")
	if err := os.WriteFile(largeFile, bytes.Repeat([]byte("x"), MaxAdminPasswordSize+1), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "both sources", values: map[string]string{"KAVEN_ADMIN_PASSWORD": strings.Repeat("x", 16), "KAVEN_ADMIN_PASSWORD_FILE": largeFile}},
		{name: "username without password", values: map[string]string{"KAVEN_ADMIN_USERNAME": "operator"}},
		{name: "invalid username", values: map[string]string{"KAVEN_ADMIN_USERNAME": "bad:name", "KAVEN_ADMIN_PASSWORD": strings.Repeat("x", 16)}},
		{name: "short password", values: map[string]string{"KAVEN_ADMIN_PASSWORD": "secret"}},
		{name: "control in password", values: map[string]string{"KAVEN_ADMIN_PASSWORD": "valid length but\ninvalid"}},
		{name: "relative file", values: map[string]string{"KAVEN_ADMIN_PASSWORD_FILE": "secret.txt"}},
		{name: "directory file", values: map[string]string{"KAVEN_ADMIN_PASSWORD_FILE": directory}},
		{name: "oversized file", values: map[string]string{"KAVEN_ADMIN_PASSWORD_FILE": largeFile}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadAdminCredentials(environment(test.values)); err == nil {
				t.Fatal("invalid configuration succeeded")
			}
		})
	}
}

func TestAdminCredentialErrorsDoNotContainPassword(t *testing.T) {
	secret := "do-not-print-this-secret\n"
	_, err := LoadAdminCredentials(environment(map[string]string{"KAVEN_ADMIN_PASSWORD": secret}))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error = %q", err)
	}
}

func environment(values map[string]string) EnvironmentLookup {
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}
