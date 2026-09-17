//go:build !linux && !windows

package backup

import "errors"

func publish(string, string) error {
	return errors.New("atomic no-replace backup publication is supported on Linux and Windows only")
}

func syncDirectory(string) error { return nil }
