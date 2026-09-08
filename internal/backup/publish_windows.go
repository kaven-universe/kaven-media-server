package backup

import "golang.org/x/sys/windows"

func publish(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

// Windows does not expose Unix directory fsync semantics. File contents are
// flushed separately and publication requests write-through rename semantics.
func syncDirectory(string) error { return nil }
