package version

import "runtime/debug"

const (
	defaultVersion = "dev"
	defaultCommit  = "unknown"
	develVersion   = "(devel)"
)

// Version is the current version of tmux-a2a-postman
// Set via ldflags during build: -X github.com/i9wa4/tmux-a2a-postman/internal/version.Version=x.y.z
var Version = defaultVersion

// Commit is the git commit hash
// Set via ldflags during build: -X github.com/i9wa4/tmux-a2a-postman/internal/version.Commit=abc123
var Commit = defaultCommit

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	Version, Commit = resolveBuildInfo(Version, Commit, info)
}

func resolveBuildInfo(currentVersion, currentCommit string, info *debug.BuildInfo) (string, string) {
	if info == nil {
		return currentVersion, currentCommit
	}

	resolvedVersion := currentVersion
	if currentVersion == defaultVersion {
		if moduleVersion := buildInfoModuleVersion(info); moduleVersion != "" {
			resolvedVersion = moduleVersion
		}
	}

	resolvedCommit := currentCommit
	if currentCommit == defaultCommit {
		if revision := buildInfoVCSRevision(info); revision != "" {
			resolvedCommit = shortRevision(revision)
		}
	}

	return resolvedVersion, resolvedCommit
}

func buildInfoModuleVersion(info *debug.BuildInfo) string {
	if info.Main.Version == "" || info.Main.Version == develVersion {
		return ""
	}
	return info.Main.Version
}

func buildInfoVCSRevision(info *debug.BuildInfo) string {
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

func shortRevision(revision string) string {
	if len(revision) <= 7 {
		return revision
	}
	return revision[:7]
}
