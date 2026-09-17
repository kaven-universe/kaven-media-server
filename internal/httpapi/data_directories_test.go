package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDataDirectoriesHandlerListsContainedDirectories(t *testing.T) {
	dataDir := t.TempDir()
	for _, name := range []string{"images/2026", "hfs/blog/posts", "bing", "backup/private/data", "tmp/staged", "logs/archive"} {
		if err := os.MkdirAll(filepath.Join(dataDir, filepath.FromSlash(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dataDir, "ignored.txt"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	NewDataDirectoriesHandler(dataDir).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/data-directories", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body DataDirectoriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dataDir, "bing"),
		filepath.Join(dataDir, "hfs"),
		filepath.Join(dataDir, "hfs", "blog"),
		filepath.Join(dataDir, "hfs", "blog", "posts"),
		filepath.Join(dataDir, "images"),
		filepath.Join(dataDir, "images", "2026"),
	}
	if !reflect.DeepEqual(body.Directories, want) {
		t.Fatalf("directories = %#v, want %#v", body.Directories, want)
	}
}

func TestListDataDirectoriesSkipsSymlinksAndEnforcesLimit(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "one", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dataDir, "outside")
	if err := os.Symlink(t.TempDir(), link); err == nil {
		directories, listErr := listDataDirectories(dataDir, 10)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, directory := range directories {
			if directory == "outside" {
				t.Fatal("symlinked directory was exposed")
			}
		}
	}
	if _, err := listDataDirectories(dataDir, 1); err == nil {
		t.Fatal("directory limit was not enforced")
	}
}
