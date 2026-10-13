package workspacetree

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #624/#700 A4(d) and A8 invariant: no other code computes derived interface
// edges, and the code that decides review, command approval, verdict
// exemption and escalation never reads the workspace tree or peer
// declarations. This scans the non-test Go sources of the module.

// interfaceEdgeCallers are the only files allowed to call the shared
// evaluator or read per-session declarations (the call sites named in the
// Guardian ruling: CLI pre-check and footers, delivery, notification, status).
var interfaceEdgeCallers = map[string]bool{
	"internal/cli/interface_edge.go":          true,
	"internal/cli/session_status_contract.go": true,
	"internal/message/interface_edge.go":      true,
	"internal/envelope/interface_edge.go":     true,
}

// privilegeFiles decide privileges that must never derive from tree position
// or peer declarations.
var privilegeFiles = []string{
	"internal/verdictgate/gate.go",
	"internal/daemon/escalation.go",
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestNoOtherCodeComputesInterfaceEdges(t *testing.T) {
	root := moduleRoot(t)
	forbidden := []string{
		"EvaluateInterfaceEdge(", "InterfaceNeighbors(", "DeclaredInterfaceEdges(", "ActiveInterfaceEdges(", "DeclaredInterfaceNode(",
	}
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		// The evaluator and the declaration accessor define these names.
		if strings.HasPrefix(rel, "internal/workspacetree/") || rel == "internal/config/interface_declaration.go" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, name := range forbidden {
			if strings.Contains(string(raw), name) && !interfaceEdgeCallers[rel] {
				t.Errorf("%s references %s but is not an allowed call site of the shared evaluator", rel, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func TestPrivilegeDecisionsNeverReadTheWorkspaceTree(t *testing.T) {
	root := moduleRoot(t)
	for _, rel := range privilegeFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", rel, err)
		}
		text := string(raw)
		for _, name := range []string{"internal/workspacetree", "WorkspaceTree", "DeclaredInterfaceNode", "InterfaceEdge"} {
			if strings.Contains(text, name) {
				t.Errorf("%s references %q; verdict exemption and escalation must read only the own-session interface node", rel, name)
			}
		}
	}
}
