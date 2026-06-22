package runtimeversion

import (
	"runtime/debug"
	"strings"
	"sync/atomic"
)

var configured atomic.Value

// Set configures the runtime version reported by the binary.
func Set(version string) {
	configured.Store(strings.TrimSpace(version))
}

// Current returns the configured runtime version, or a best-effort build-info fallback.
func Current() string {
	info, ok := debug.ReadBuildInfo()
	return FromBuildInfo(info, ok)
}

// FromBuildInfo returns the configured runtime version, or derives one from Go build metadata.
func FromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if version := configuredVersion(); version != "" {
		return version
	}
	if !ok || info == nil {
		return "dev"
	}
	if version := strings.TrimSpace(info.Main.Version); version != "" && version != "(devel)" {
		return version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && strings.TrimSpace(setting.Value) != "" {
			return setting.Value
		}
	}
	return "dev"
}

func configuredVersion() string {
	value := configured.Load()
	if value == nil {
		return ""
	}
	version, _ := value.(string)
	return version
}
