package workspace

import (
	"context"
	"errors"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// ConfiguredApprovalResolver rereads the explicit Markdown topology for every
// native verification. It does not consult daemon projections or infer absence
// from a failed config lookup. The Executor separately pins the initial native
// reviewer and caller identities throughout one invocation.
func ConfiguredApprovalResolver(path string) ApprovalResolver {
	return func(ctx context.Context, s Snapshot) (string, error) {
		if _, err := validateSnapshot(s); err != nil {
			return "", err
		}
		topology, err := config.LoadWorkspaceTopology(ctx, path)
		if err != nil {
			return "", err
		}
		known := false
		for _, node := range topology.Nodes {
			if node == s.Caller.Role {
				known = true
			}
		}
		if !known {
			return "", errors.New("caller is not a configured topology node")
		}
		if topology.Reviewer == "" {
			return "", nil
		}
		if topology.Reviewer == s.Caller.Role {
			return "", ErrUnknown
		}
		for _, b := range s.Bindings {
			if b.Role == topology.Reviewer {
				return topology.Reviewer, nil
			}
		}
		for _, role := range s.AbsentRoles {
			if role == topology.Reviewer {
				return "", nil
			}
		}
		return "", errors.New("configured reviewer lacks a verified native binding")
	}
}
