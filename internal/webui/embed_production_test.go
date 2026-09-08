//go:build webui

package webui

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestProductionBundleContainsApplicationAndLinkedAssets(t *testing.T) {
	index, err := assets.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(index)
	if !strings.Contains(html, "<title>Kaven Media Server</title>") || !strings.Contains(html, "type=\"module\"") {
		t.Fatal("production index must contain the branded compiled application")
	}
	links := regexp.MustCompile(`(?:src|href)=["']?(/[^\s"'<>]+)`).FindAllStringSubmatch(html, -1)
	if len(links) == 0 {
		t.Fatal("production index contains no asset links")
	}
	for _, link := range links {
		info, err := fs.Stat(assets, "dist"+link[1])
		if err != nil {
			t.Errorf("missing linked asset %s: %v", link[1], err)
			continue
		}
		if info.IsDir() || info.Size() == 0 {
			t.Errorf("linked asset %s is not a nonempty file", link[1])
		}
	}
	// Check lazy-loaded chunks and fonts too: go:embed must not omit any file
	// from the generated tree because of a filename beginning with '_' or '.'.
	err = fs.WalkDir(os.DirFS("dist"), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if _, err := assets.ReadFile("dist/" + name); err != nil {
			t.Errorf("generated asset %s is not embedded: %v", name, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
