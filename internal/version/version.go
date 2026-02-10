package version

// Version is the current version of tmux-a2a-postman
// Set via ldflags during build: -X internal/version.Version=x.y.z
var Version = "dev"

// Commit is the git commit hash
// Set via ldflags during build: -X internal/version.Commit=abc123
var Commit = "unknown"
