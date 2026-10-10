package daemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// loadInterfaceNodeTestConfig writes content as an explicit postman.toml in an
// isolated temp dir (no real HOME/XDG config) and loads it.
func loadInterfaceNodeTestConfig(t *testing.T, content string) *config.Config {
	t.Helper()
	root := t.TempDir()
	envRoot := t.TempDir()
	configPath := filepath.Join(root, "postman.toml")
	t.Chdir(root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(envRoot, "xdg"))
	t.Setenv("HOME", filepath.Join(envRoot, "home"))
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// TestConfigureVerdictGateFromConfig_UnsetInterfaceNodeClearsExemption (#764
// X3): a config without an interface node must not leave a previous
// process-global exemption in place; the exemption follows the config in both
// directions.
func TestConfigureVerdictGateFromConfig_UnsetInterfaceNodeClearsExemption(t *testing.T) {
	original := verdictExemptInterfaceNode
	t.Cleanup(func() { verdictExemptInterfaceNode = original })

	configureVerdictGateFromConfig(&config.Config{InterfaceNode: "messenger"})
	if verdictExemptInterfaceNode != "messenger" {
		t.Fatalf("verdictExemptInterfaceNode = %q after configuring interface_node=messenger, want messenger", verdictExemptInterfaceNode)
	}

	configureVerdictGateFromConfig(&config.Config{})
	if verdictExemptInterfaceNode != "" {
		t.Fatalf("verdictExemptInterfaceNode = %q after a config with no interface_node, want empty (no exemption)", verdictExemptInterfaceNode)
	}

	configureVerdictGateFromConfig(&config.Config{InterfaceNode: "mouthpiece"})
	if verdictExemptInterfaceNode != "mouthpiece" {
		t.Fatalf("verdictExemptInterfaceNode = %q after configuring interface_node=mouthpiece, want mouthpiece", verdictExemptInterfaceNode)
	}
}

// TestVerdictExemptInterfaceNodeDefaultsToNoExemption (#764 A2): the package
// default is empty, not a built-in node name.
func TestVerdictExemptInterfaceNodeDefaultsToNoExemption(t *testing.T) {
	// Only the declared default is meaningful here; other tests restore the
	// variable to whatever they found, so assert the source-level default by
	// resetting through an unset config.
	original := verdictExemptInterfaceNode
	t.Cleanup(func() { verdictExemptInterfaceNode = original })
	configureVerdictGateFromConfig(config.DefaultConfig())
	if verdictExemptInterfaceNode != "" {
		t.Fatalf("embedded default config yields exemption %q, want none", verdictExemptInterfaceNode)
	}
}

// TestStartupAutoPingNodeKeys_InterfaceNode (#764 X5): with no explicit
// interface node the startup PING keeps its existing fan-out to every node;
// an explicit interface node narrows it; an explicit empty value keeps the
// fan-out. The legacy ui_node form is not a Config field any more, so it has
// no way to narrow.
func TestStartupAutoPingNodeKeys_InterfaceNode(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:messenger": {},
		"review:worker":    {},
	}
	all := []string{"review:messenger", "review:worker"}

	t.Run("unset fans out to every node", func(t *testing.T) {
		got := startupAutoPingNodeKeys(nodes, config.DefaultConfig())
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("startupAutoPingNodeKeys(unset) = %v, want %v", got, all)
		}
	})
	t.Run("nil config fans out to every node", func(t *testing.T) {
		got := startupAutoPingNodeKeys(nodes, nil)
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("startupAutoPingNodeKeys(nil) = %v, want %v", got, all)
		}
	})
	t.Run("explicit interface node narrows to that node", func(t *testing.T) {
		cfg := loadInterfaceNodeTestConfig(t, "[postman]\ninterface_node = \"messenger\"\nedges = [\"messenger --- worker\"]\n\n[messenger]\n[worker]\n")
		got := startupAutoPingNodeKeys(nodes, cfg)
		if want := []string{"review:messenger"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("startupAutoPingNodeKeys(interface_node=messenger) = %v, want %v", got, want)
		}
	})
	t.Run("explicit empty interface node keeps fan-out", func(t *testing.T) {
		cfg := loadInterfaceNodeTestConfig(t, "[postman]\ninterface_node = \"\"\nedges = [\"messenger --- worker\"]\n\n[messenger]\n[worker]\n")
		got := startupAutoPingNodeKeys(nodes, cfg)
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("startupAutoPingNodeKeys(interface_node=\"\") = %v, want %v", got, all)
		}
	})
	t.Run("legacy ui_node does not narrow", func(t *testing.T) {
		cfg := loadInterfaceNodeTestConfig(t, "[postman]\nui_node = \"messenger\"\nedges = [\"messenger --- worker\"]\n\n[messenger]\n[worker]\n")
		got := startupAutoPingNodeKeys(nodes, cfg)
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("startupAutoPingNodeKeys(legacy ui_node) = %v, want %v (legacy key must be ignored)", got, all)
		}
	})
}
