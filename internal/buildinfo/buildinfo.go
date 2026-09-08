// Package buildinfo reports the identity of the running executable.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strconv"
)

var (
	Version  = "dev"
	Revision = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Modified  bool   `json:"modified"`
	GoVersion string `json:"goVersion"`
}

func Current() Info {
	debugInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{Version: Version, Revision: Revision, GoVersion: runtime.Version()}
	}
	return resolve(Version, Revision, debugInfo)
}

func resolve(version, revision string, debugInfo *debug.BuildInfo) Info {
	result := Info{Version: version, Revision: revision, GoVersion: runtime.Version()}
	if result.Version == "dev" && debugInfo.Main.Version != "" && debugInfo.Main.Version != "(devel)" {
		result.Version = debugInfo.Main.Version
	}
	for _, setting := range debugInfo.Settings {
		switch setting.Key {
		case "vcs.revision":
			if result.Revision == "unknown" && setting.Value != "" {
				result.Revision = setting.Value
			}
		case "vcs.modified":
			result.Modified, _ = strconv.ParseBool(setting.Value)
		}
	}
	return result
}
