//go:build openbsd

package diskspace

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

func Available(path string) (uint64, error) {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return 0, fmt.Errorf("read available filesystem bytes: %w", err)
	}
	blocks, size := uint64(status.F_bavail), uint64(status.F_bsize)
	if size != 0 && blocks > math.MaxUint64/size {
		return math.MaxUint64, nil
	}
	return blocks * size, nil
}
