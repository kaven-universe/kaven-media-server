package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolveUsesInjectedIdentityAndVCSDirtyState(t *testing.T) {
	info := resolve("1.2.3", "injected", &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "automatic"},
			{Key: "vcs.modified", Value: "true"},
		},
	})
	if info.Version != "1.2.3" || info.Revision != "injected" || !info.Modified || info.GoVersion == "" {
		t.Fatalf("info = %#v", info)
	}
}

func TestResolveFallsBackToGoBuildInformation(t *testing.T) {
	info := resolve("dev", "unknown", &debug.BuildInfo{
		Main:     debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "automatic"}},
	})
	if info.Version != "v1.2.3" || info.Revision != "automatic" || info.Modified {
		t.Fatalf("info = %#v", info)
	}

	development := resolve("dev", "unknown", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if development.Version != "dev" || development.Revision != "unknown" {
		t.Fatalf("development info = %#v", development)
	}
}
