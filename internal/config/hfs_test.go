package config

import (
	"strings"
	"testing"
)

func TestLoadHFSRootsUsesPrivateDefault(t *testing.T) {
	roots, err := LoadHFSRoots(environment(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultHFSRoots()[0]
	if len(roots) != 1 || roots[0] != want || roots[0].Public {
		t.Fatalf("roots = %#v, want private %#v", roots, want)
	}
}

func TestLoadHFSRootsParsesPublicAndPrivateRoots(t *testing.T) {
	roots, err := LoadHFSRoots(environment(map[string]string{
		HFSRootsEnvironment: `[{"name":"public","path":"hfs/public","public":true},{"name":"private","path":"hfs/private"},{"name":"legacy","path":"C:/legacy","readOnly":true}]`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 3 || !roots[0].Public || roots[1].Public || !roots[2].ReadOnly {
		t.Fatalf("roots = %#v", roots)
	}
}

func TestLoadHFSRootsRejectsMalformedConfiguration(t *testing.T) {
	tooMany := make([]string, maxHFSRootCount+1)
	for index := range tooMany {
		tooMany[index] = `{"name":"root","path":"hfs/root"}`
	}
	tests := []string{
		"",
		"null",
		`{"name":"one"}`,
		`[{"name":"one","path":"hfs/one","unknown":true}]`,
		`[{"name":"one","path":"hfs/one"}] trailing`,
		strings.Repeat("x", maxHFSRootsJSONSize+1),
		"[" + strings.Join(tooMany, ",") + "]",
	}
	for _, value := range tests {
		if _, err := LoadHFSRoots(environment(map[string]string{HFSRootsEnvironment: value})); err == nil {
			t.Errorf("configuration %q succeeded", value)
		}
	}
}
