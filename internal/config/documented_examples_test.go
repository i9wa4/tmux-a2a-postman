package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

func TestDocumentedConfigurations(t *testing.T) {
	for _, name := range []string{"minimal", "practical", "hierarchy", "launch"} {
		t.Run(name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("..", "..", "examples", name, "postman.md"))
			if err != nil {
				t.Fatal(err)
			}
			topology, err := LoadWorkspaceTopology(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := topology.RecipientPolicy(context.Background(), path)
			if err != nil || policy.Common == "" {
				t.Fatal(policy, err)
			}
			if _, err := topology.LaunchPresets(); err != nil {
				t.Fatal(err)
			}
			if name == "practical" {
				if topology.Reviewer != "approver" || topology.InterfaceNode != "messenger" || len(topology.Nodes) != 5 {
					t.Fatal(topology)
				}
				if policy.Nodes["builder"].Role != "Implement scoped changes and supply test evidence." || !strings.Contains(policy.Nodes["builder"].Instructions, "assigned worktree") {
					t.Fatal(policy.Nodes["builder"])
				}
				for _, skill := range []string{"postman-usage", "postman-send-message", "postman-recall", "postman-team-manager"} {
					if !strings.Contains(policy.Catalog, "`"+skill+"`") {
						t.Fatal("selected installed metadata missing", skill)
					}
				}
			}
			if name == "hierarchy" {
				for _, route := range [][3]string{{"postman-parent", "gateway", "postman-child:gateway"}, {"postman-child", "gateway", "postman-parent:gateway"}} {
					if _, _, err := topology.InterfaceRoute(route[0], route[1], route[2]); err != nil {
						t.Fatal(route, err)
					}
				}
				if _, _, err := topology.InterfaceRoute("postman-parent", "builder", "postman-child:gateway"); err == nil {
					t.Fatal("example grants builder an interface shortcut")
				}
			}
		})
	}
}

func TestDocumentedInstalledReferenceContainsCompleteUsableConfig(t *testing.T) {
	raw, err := helpdocs.Read("postman-md")
	if err != nil {
		t.Fatal(err)
	}
	_, example, ok := strings.Cut(string(raw), "````markdown\n")
	if !ok {
		t.Fatal("complete installed example missing")
	}
	example, _, ok = strings.Cut(example, "\n````")
	if !ok {
		t.Fatal("complete installed example not terminated")
	}
	path := topologyFixture(t, example)
	topology, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil || topology.Reviewer != "reviewer" || topology.InterfaceNode != "manager" {
		t.Fatal(topology, err)
	}
	policy, err := topology.RecipientPolicy(context.Background(), path)
	if err != nil || policy.Nodes["worker"].Role == "" || policy.Footer == "" {
		t.Fatal(policy, err)
	}
}
