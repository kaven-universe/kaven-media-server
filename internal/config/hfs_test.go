package config

import (
	"testing"
)

func TestDefaultHFSRootsUsesPrivateManagedDirectory(t *testing.T) {
	roots := DefaultHFSRoots()
	if len(roots) != 1 || roots[0].Name != "uploaded" || roots[0].Path != "upload" || roots[0].Public || roots[0].ReadOnly {
		t.Fatalf("default HFS roots = %+v", roots)
	}
}
