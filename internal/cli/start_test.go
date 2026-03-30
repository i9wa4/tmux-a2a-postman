package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWatchedConfigPath(t *testing.T) {
	tests := []struct {
		name       string
		withXDG    bool
		withLocal  bool
		withCustom bool
		want       []string
		wantNodes  []string
	}{
		{
			name:       "explicit path wins",
			withXDG:    true,
			withLocal:  true,
			withCustom: true,
			want:       []string{"custom", "local"},
			wantNodes:  []string{"custom", "local"},
		},
		{
			name:      "project local overrides xdg",
			withXDG:   true,
			withLocal: true,
			want:      []string{"xdg", "local"},
			wantNodes: []string{"xdg", "local"},
		},
		{
			name:      "xdg used when local missing",
			withXDG:   true,
			withLocal: false,
			want:      []string{"xdg"},
			wantNodes: []string{"xdg"},
		},
		{
			name:      "project local used without xdg",
			withLocal: true,
			want:      []string{"local"},
			wantNodes: []string{"local"},
		},
		{
			name: "empty when nothing exists",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			homeDir := filepath.Join(root, "home")
			repoDir := filepath.Join(homeDir, "work", "repo")
			xdgDir := filepath.Join(root, "xdg")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatalf("MkdirAll repo: %v", err)
			}
			if err := os.MkdirAll(homeDir, 0o755); err != nil {
				t.Fatalf("MkdirAll home: %v", err)
			}

			t.Chdir(repoDir)
			t.Setenv("HOME", homeDir)
			t.Setenv("XDG_CONFIG_HOME", xdgDir)

			paths := make(map[string]string)
			if tc.withXDG {
				paths["xdg"] = filepath.Join(xdgDir, "tmux-a2a-postman", "postman.toml")
				writeWatcherConfigFixture(t, paths["xdg"])
			}
			if tc.withLocal {
				paths["local"] = filepath.Join(repoDir, ".tmux-a2a-postman", "postman.toml")
				writeWatcherConfigFixture(t, paths["local"])
			}
			explicitPath := ""
			if tc.withCustom {
				explicitPath = filepath.Join(root, "custom", "postman.toml")
				writeWatcherConfigFixture(t, explicitPath)
				paths["custom"] = explicitPath
			}

			got := resolveWatchedConfigPaths(explicitPath)
			want := make([]string, 0, len(tc.want))
			for _, label := range tc.want {
				want = append(want, paths[label])
			}
			if len(got) != len(want) {
				t.Fatalf("resolveWatchedConfigPaths(%q) len = %d, want %d; got=%v want=%v", explicitPath, len(got), len(want), got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("resolveWatchedConfigPaths(%q)[%d] = %q, want %q; full got=%v want=%v", explicitPath, i, got[i], want[i], got, want)
				}
			}

			gotNodes := resolveWatchedNodesDirs(got)
			wantNodes := make([]string, 0, len(tc.wantNodes))
			for _, label := range tc.wantNodes {
				wantNodes = append(wantNodes, filepath.Join(filepath.Dir(paths[label]), "nodes"))
			}
			if len(gotNodes) != len(wantNodes) {
				t.Fatalf("resolveWatchedNodesDirs(%q) len = %d, want %d; got=%v want=%v", explicitPath, len(gotNodes), len(wantNodes), gotNodes, wantNodes)
			}
			for i := range wantNodes {
				if gotNodes[i] != wantNodes[i] {
					t.Fatalf("resolveWatchedNodesDirs(%q)[%d] = %q, want %q; full got=%v want=%v", explicitPath, i, gotNodes[i], wantNodes[i], gotNodes, wantNodes)
				}
			}
		})
	}
}

func writeWatcherConfigFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("[postman]\nui_node = \"messenger\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(path), "nodes"), 0o755); err != nil {
		t.Fatalf("MkdirAll nodes dir: %v", err)
	}
}
