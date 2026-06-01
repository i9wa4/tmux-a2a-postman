package workspacetree

import "github.com/i9wa4/tmux-a2a-postman/internal/config"

func RegistrationsFromConfig(cfg *config.Config) []Registration {
	if cfg == nil || len(cfg.WorkspaceRoots) == 0 {
		return nil
	}
	registrations := make([]Registration, 0, len(cfg.WorkspaceRoots))
	for _, root := range cfg.WorkspaceRoots {
		registrations = append(registrations, Registration{
			SessionName: root.SessionName,
			Label:       root.Label,
			Root:        root.Root,
			RootID:      root.RootID,
		})
	}
	return registrations
}

func BuildFromConfig(cfg *config.Config) Topology {
	return Build(RegistrationsFromConfig(cfg))
}
