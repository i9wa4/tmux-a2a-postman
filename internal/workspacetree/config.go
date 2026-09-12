package workspacetree

import "github.com/i9wa4/tmux-a2a-postman/internal/config"

func RegistrationsFromConfig(cfg *config.Config) []Registration {
	if cfg == nil || len(cfg.WorkspaceTree) == 0 {
		return nil
	}
	registrations := make([]Registration, 0, len(cfg.WorkspaceTree))
	for _, node := range cfg.WorkspaceTree {
		registrations = append(registrations, Registration{
			SessionName:       node.SessionName,
			ID:                node.ID,
			Label:             node.Label,
			ParentSessionName: node.ParentSessionName,
			Representative:    node.Representative,
			DiplomatNode:      node.DiplomatNode,
			Order:             node.Order,
		})
	}
	return registrations
}

func BuildFromConfig(cfg *config.Config) Topology {
	return Build(RegistrationsFromConfig(cfg))
}

// ConfiguredEdges combines explicit configuration with tree-derived diplomat
// authorization edges so every consumer shares one eligibility model.
func ConfiguredEdges(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	edges := append([]string{}, cfg.Edges...)
	for _, derived := range BuildFromConfig(cfg).DiplomatEdges() {
		if !hasUndirectedEdge(edges, derived) {
			edges = append(edges, derived)
		}
	}
	return edges
}

func EligibleNodeNames(cfg *config.Config) map[string]bool {
	return config.GetEdgeNodeNames(ConfiguredEdges(cfg))
}

// hasUndirectedEdge recognizes the same pair even if the configured edge is
// written in the opposite direction. This prevents a static declaration from
// duplicating a relationship that the workspace tree also derives.
func hasUndirectedEdge(edges []string, candidate string) bool {
	candidateNodes := config.OrderedEdgeNodeNames([]string{candidate})
	if len(candidateNodes) != 2 {
		return false
	}
	// ParseEdges expands every declaration into adjacent pairs, including
	// chains such as A --- B --- C. Looking up the candidate pair in that
	// adjacency prevents a derived A---B or B---C relation from being appended
	// merely because the static declaration contained a third node.
	adjacency, err := config.ParseEdges(edges)
	if err != nil {
		return false
	}
	for _, neighbor := range adjacency[candidateNodes[0]] {
		if neighbor == candidateNodes[1] {
			return true
		}
	}
	return false
}
