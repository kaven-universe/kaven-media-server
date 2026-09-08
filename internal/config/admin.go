package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultAdminUsername = "admin"
	MinAdminPasswordSize = 16
	MaxAdminPasswordSize = 1024
)

type AdminCredentials struct {
	Enabled  bool
	Username string
	Password []byte
}

type EnvironmentLookup func(string) (string, bool)

func AdminCredentialsFromEnvironment() (AdminCredentials, error) {
	return LoadAdminCredentials(os.LookupEnv)
}

func LoadAdminCredentials(lookup EnvironmentLookup) (AdminCredentials, error) {
	username, usernameSet := lookup("KAVEN_ADMIN_USERNAME")
	if !usernameSet {
		username = DefaultAdminUsername
	}
	direct, directSet := lookup("KAVEN_ADMIN_PASSWORD")
	filePath, fileSet := lookup("KAVEN_ADMIN_PASSWORD_FILE")
	if directSet && fileSet {
		return AdminCredentials{}, errors.New("configure only one of KAVEN_ADMIN_PASSWORD and KAVEN_ADMIN_PASSWORD_FILE")
	}
	if !directSet && !fileSet {
		if usernameSet && username != DefaultAdminUsername {
			return AdminCredentials{}, errors.New("KAVEN_ADMIN_USERNAME requires an admin password")
		}
		return AdminCredentials{Username: username}, nil
	}
	if err := validateAdminUsername(username); err != nil {
		return AdminCredentials{}, err
	}

	var password []byte
	if directSet {
		password = []byte(direct)
	} else {
		loaded, err := readPasswordFile(filePath)
		if err != nil {
			return AdminCredentials{}, err
		}
		password = loaded
	}
	if err := validateAdminPassword(password); err != nil {
		clear(password)
		return AdminCredentials{}, err
	}
	return AdminCredentials{Enabled: true, Username: username, Password: password}, nil
}

func ClearAdminPassword(credentials *AdminCredentials) {
	if credentials == nil {
		return
	}
	clear(credentials.Password)
	credentials.Password = nil
}

func readPasswordFile(filePath string) ([]byte, error) {
	if !filepath.IsAbs(filePath) {
		return nil, errors.New("KAVEN_ADMIN_PASSWORD_FILE must be an absolute path")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open KAVEN_ADMIN_PASSWORD_FILE: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect KAVEN_ADMIN_PASSWORD_FILE: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("KAVEN_ADMIN_PASSWORD_FILE must reference a regular file")
	}
	password, err := io.ReadAll(io.LimitReader(file, MaxAdminPasswordSize+3))
	if err != nil {
		return nil, fmt.Errorf("read KAVEN_ADMIN_PASSWORD_FILE: %w", err)
	}
	password = trimOneLineEnding(password)
	return password, nil
}

func trimOneLineEnding(value []byte) []byte {
	if len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
		if len(value) > 0 && value[len(value)-1] == '\r' {
			value = value[:len(value)-1]
		}
	}
	return value
}

func validateAdminUsername(username string) error {
	if len(username) < 1 || len(username) > 64 {
		return errors.New("KAVEN_ADMIN_USERNAME must be 1-64 characters")
	}
	for _, character := range username {
		if !isAdminUsernameCharacter(character) {
			return errors.New("KAVEN_ADMIN_USERNAME contains an invalid character")
		}
	}
	return nil
}

func isAdminUsernameCharacter(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9' || strings.ContainsRune("._@-", character)
}

func validateAdminPassword(password []byte) error {
	if len(password) < MinAdminPasswordSize || len(password) > MaxAdminPasswordSize {
		return fmt.Errorf("admin password must be %d-%d bytes", MinAdminPasswordSize, MaxAdminPasswordSize)
	}
	if !utf8.Valid(password) {
		return errors.New("admin password must be valid UTF-8")
	}
	for _, character := range string(password) {
		if unicode.IsControl(character) {
			return errors.New("admin password must not contain control characters")
		}
	}
	return nil
}
