package version

// Version is the current version of tmux-a2a-postman
// Set via ldflags during build: -X github.com/i9wa4/tmux-a2a-postman/internal/version.Version=x.y.z
var Version = "dev"

// Commit is the git commit hash
// Set via ldflags during build: -X github.com/i9wa4/tmux-a2a-postman/internal/version.Commit=abc123
var Commit = "unknown"
