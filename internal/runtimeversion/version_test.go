package runtimeversion

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfoUsesConfiguredVersion(t *testing.T) {
	Set(" 1.2.3 ")
	t.Cleanup(func() { Set("") })

	version := FromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "9.9.9"},
	}, true)

	if version != "1.2.3" {
		t.Fatalf("expected configured version, got %q", version)
	}
}

func TestFromBuildInfoUsesModuleVersion(t *testing.T) {
	Set("")

	version := FromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "1.2.3"},
	}, true)

	if version != "1.2.3" {
		t.Fatalf("expected module version, got %q", version)
	}
}

func TestFromBuildInfoFallsBackToVCSRevision(t *testing.T) {
	Set("")

	version := FromBuildInfo(&debug.BuildInfo{
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}},
	}, true)

	if version != "abc123" {
		t.Fatalf("expected vcs revision, got %q", version)
	}
}

func TestFromBuildInfoFallsBackToDev(t *testing.T) {
	Set("")

	version := FromBuildInfo(nil, false)

	if version != "dev" {
		t.Fatalf("expected dev fallback, got %q", version)
	}
}
