//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package datalock

import (
	"errors"
	"os"
)

func lock(*os.File) error {
	return errors.New("data directory locking is unsupported on this platform")
}
