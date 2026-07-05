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
			Order:             node.Order,
			Root:              node.Root,
		})
	}
	return registrations
}

func BuildFromConfig(cfg *config.Config) Topology {
	return Build(RegistrationsFromConfig(cfg))
}
