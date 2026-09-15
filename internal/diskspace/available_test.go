package diskspace

import "testing"

func TestAvailableForTemporaryFilesystem(t *testing.T) {
	available, err := Available(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if available == 0 {
		t.Fatal("available bytes = 0")
	}
}
