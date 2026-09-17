//go:build netbsd

package diskspace

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

func Available(path string) (uint64, error) {
	var status unix.Statvfs_t
	if err := unix.Statvfs(path, &status); err != nil {
		return 0, fmt.Errorf("read available filesystem bytes: %w", err)
	}
	blocks, size := status.Bavail, status.Frsize
	if size == 0 {
		size = status.Bsize
	}
	if size != 0 && blocks > math.MaxUint64/size {
		return math.MaxUint64, nil
	}
	return blocks * size, nil
}
