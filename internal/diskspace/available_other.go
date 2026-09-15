//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package diskspace

import "errors"

func Available(string) (uint64, error) {
	return 0, errors.New("available filesystem capacity is unsupported on this operating system")
}
