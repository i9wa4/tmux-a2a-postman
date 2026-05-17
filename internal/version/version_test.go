package version

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildInfo_UsesTaggedModuleVersionWhenLDFlagsAreDefault(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/i9wa4/tmux-a2a-postman",
			Version: "v1.2.3",
		},
	}

	gotVersion, gotCommit := resolveBuildInfo(defaultVersion, defaultCommit, info)
	if gotVersion == defaultVersion {
		t.Fatalf("version stayed at default %q; tagged module installs must report the module version", gotVersion)
	}
	if gotVersion != "v1.2.3" {
		t.Fatalf("version = %q, want module version", gotVersion)
	}
	if gotCommit != defaultCommit {
		t.Fatalf("commit = %q, want default commit when no VCS revision is embedded", gotCommit)
	}
}

func TestResolveBuildInfo_UsesVCSRevisionWhenLDFlagsAreDefault(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/i9wa4/tmux-a2a-postman",
			Version: develVersion,
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
		},
	}

	gotVersion, gotCommit := resolveBuildInfo(defaultVersion, defaultCommit, info)
	if gotVersion != defaultVersion {
		t.Fatalf("version = %q, want default version for devel module builds", gotVersion)
	}
	if gotCommit != "0123456" {
		t.Fatalf("commit = %q, want short VCS revision", gotCommit)
	}
}

func TestResolveBuildInfo_KeepsLDFlagValues(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/i9wa4/tmux-a2a-postman",
			Version: "v1.2.3",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
		},
	}

	gotVersion, gotCommit := resolveBuildInfo("v9.9.9", "fedcba9", info)
	if gotVersion != "v9.9.9" {
		t.Fatalf("version = %q, want ldflag version", gotVersion)
	}
	if gotCommit != "fedcba9" {
		t.Fatalf("commit = %q, want ldflag commit", gotCommit)
	}
}
