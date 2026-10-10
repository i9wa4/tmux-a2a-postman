package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// loadWhitespaceInterfaceNodeConfig loads an explicit postman.toml from an
// isolated temp dir (no real HOME/XDG config).
func loadWhitespaceInterfaceNodeConfig(t *testing.T, interfaceNodeLine string) *config.Config {
	t.Helper()
	root := t.TempDir()
	envRoot := t.TempDir()
	t.Chdir(root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(envRoot, "xdg"))
	t.Setenv("HOME", filepath.Join(envRoot, "home"))
	configPath := filepath.Join(root, "postman.toml")
	content := "[postman]\n" + interfaceNodeLine + "\nedges = [\"messenger --- worker\"]\n\n[messenger]\n[worker]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// TestInterfaceNodeWhitespace_StartPingSelector_F002 (#764 F-002): the CLI
// start PING selector uses the trimmed interface node. A padded name narrows to
// that node; an explicit whitespace-only value keeps the fan-out.
func TestInterfaceNodeWhitespace_StartPingSelector_F002(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:messenger": {},
		"review:worker":    {},
	}

	t.Run("padded name narrows to that node", func(t *testing.T) {
		cfg := loadWhitespaceInterfaceNodeConfig(t, `interface_node = " messenger "`)
		got, ok := restrictPingTargetsToConfiguredInterfaceNode(nodes, cfg)
		if !ok || len(got) != 1 {
			t.Fatalf("restrictPingTargetsToConfiguredInterfaceNode(\" messenger \") = (%v, %v), want exactly review:messenger", got, ok)
		}
		if _, found := got["review:messenger"]; !found {
			t.Fatalf("filtered nodes = %v, want review:messenger", got)
		}
	})
	t.Run("whitespace only keeps fan-out", func(t *testing.T) {
		cfg := loadWhitespaceInterfaceNodeConfig(t, `interface_node = "   "`)
		got, ok := restrictPingTargetsToConfiguredInterfaceNode(nodes, cfg)
		if !ok || len(got) != len(nodes) {
			t.Fatalf("restrictPingTargetsToConfiguredInterfaceNode(\"   \") = (%v, %v), want every node", got, ok)
		}
	})
}
