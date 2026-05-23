package config

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestConfigPathResolver_ExplicitConfigPreservesDiscoveredOverlay(t *testing.T) {
	resolver := fakeConfigPathResolver(
		map[string]string{"XDG_CONFIG_HOME": "/xdg"},
		"/home/user",
		nil,
		map[string]bool{
			"/xdg/tmux-a2a-postman/postman.toml": true,
			"/xdg/tmux-a2a-postman/postman.md":   true,
		},
	)

	got := resolver.resolveConfigPaths("/explicit/postman.toml")

	if got.configPath != "/explicit/postman.toml" {
		t.Fatalf("configPath = %q, want explicit path", got.configPath)
	}
	if got.tomlPath != "/xdg/tmux-a2a-postman/postman.toml" {
		t.Fatalf("tomlPath = %q, want XDG TOML path", got.tomlPath)
	}
	if got.markdownPath != "/xdg/tmux-a2a-postman/postman.md" {
		t.Fatalf("markdownPath = %q, want XDG Markdown path", got.markdownPath)
	}
	if got.overlayDir != "/xdg/tmux-a2a-postman" {
		t.Fatalf("overlayDir = %q, want XDG overlay dir", got.overlayDir)
	}
}

func TestConfigPathResolver_XDGPrecedence(t *testing.T) {
	resolver := fakeConfigPathResolver(
		map[string]string{"XDG_CONFIG_HOME": "/xdg"},
		"/home/user",
		nil,
		map[string]bool{
			"/xdg/tmux-a2a-postman/postman.toml":               true,
			"/home/user/.config/tmux-a2a-postman/postman.toml": true,
			"/xdg/tmux-a2a-postman/postman.md":                 true,
			"/home/user/.config/tmux-a2a-postman/postman.md":   true,
		},
	)

	got := resolver.resolveConfigPaths("")

	if got.configPath != "/xdg/tmux-a2a-postman/postman.toml" {
		t.Fatalf("configPath = %q, want XDG TOML path", got.configPath)
	}
	if got.markdownPath != "/xdg/tmux-a2a-postman/postman.md" {
		t.Fatalf("markdownPath = %q, want XDG Markdown path", got.markdownPath)
	}
}

func TestConfigPathResolver_HOMEFallback(t *testing.T) {
	resolver := fakeConfigPathResolver(
		map[string]string{},
		"/home/user",
		nil,
		map[string]bool{
			"/home/user/.config/tmux-a2a-postman/postman.toml": true,
			"/home/user/.config/tmux-a2a-postman/postman.md":   true,
		},
	)

	got := resolver.resolveConfigPaths("")

	if got.configPath != "/home/user/.config/tmux-a2a-postman/postman.toml" {
		t.Fatalf("configPath = %q, want HOME TOML fallback", got.configPath)
	}
	if got.markdownPath != "/home/user/.config/tmux-a2a-postman/postman.md" {
		t.Fatalf("markdownPath = %q, want HOME Markdown fallback", got.markdownPath)
	}
	if got.overlayDir != "/home/user/.config/tmux-a2a-postman" {
		t.Fatalf("overlayDir = %q, want HOME overlay dir", got.overlayDir)
	}
}

func TestConfigPathResolver_HomeLookupFailure(t *testing.T) {
	resolver := fakeConfigPathResolver(
		map[string]string{},
		"",
		errors.New("no home"),
		map[string]bool{},
	)

	got := resolver.resolveConfigPaths("")

	if got.configPath != "" {
		t.Fatalf("configPath = %q, want empty", got.configPath)
	}
	if got.markdownPath != "" {
		t.Fatalf("markdownPath = %q, want empty", got.markdownPath)
	}
	if got.overlayDir != "" {
		t.Fatalf("overlayDir = %q, want empty", got.overlayDir)
	}
}

func TestConfigPathResolver_XDGSetDoesNotFallbackToHome(t *testing.T) {
	resolver := fakeConfigPathResolver(
		map[string]string{"XDG_CONFIG_HOME": "/xdg"},
		"/home/user",
		nil,
		map[string]bool{
			"/home/user/.config/tmux-a2a-postman/postman.toml": true,
			"/home/user/.config/tmux-a2a-postman/postman.md":   true,
		},
	)

	got := resolver.resolveConfigPaths("")

	if got.configPath != "" {
		t.Fatalf("configPath = %q, want empty when XDG is set but absent", got.configPath)
	}
	if got.markdownPath != "" {
		t.Fatalf("markdownPath = %q, want empty when XDG is set but absent", got.markdownPath)
	}
}

func TestConfigPathResolver_ProjectLocalConfigIgnored(t *testing.T) {
	resolver := fakeConfigPathResolver(
		map[string]string{},
		"/home/user",
		nil,
		map[string]bool{
			"/cwd/.tmux-a2a-postman/postman.toml": true,
		},
	)

	got, err := resolver.resolveLocalConfigPath("", "")
	if err != nil {
		t.Fatalf("resolveLocalConfigPath: %v", err)
	}
	if got != "" {
		t.Fatalf("resolveLocalConfigPath = %q, want empty after project-local retirement", got)
	}
}

func fakeConfigPathResolver(env map[string]string, home string, homeErr error, existing map[string]bool) configPathResolver {
	return configPathResolver{
		getenv: func(key string) string {
			return env[key]
		},
		userHomeDir: func() (string, error) {
			if homeErr != nil {
				return "", homeErr
			}
			return home, nil
		},
		getwd: func() (string, error) {
			return "/cwd", nil
		},
		stat: func(path string) error {
			if existing[path] {
				return nil
			}
			return errors.New("not found")
		},
		join: filepath.Join,
	}
}
