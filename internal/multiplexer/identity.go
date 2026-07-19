package multiplexer

import (
	"context"
	"fmt"
	"strings"
)

type IdentityFailure string

const (
	IdentityFailureUnknown      IdentityFailure = "unknown"
	IdentityFailureLookupFailed IdentityFailure = "lookup_failed"
)

type CurrentIdentity struct {
	Backend     BackendKind
	SessionName string
	NodeName    string
	Pane        ResourceID
	NativeIDs   map[string]string
}

type IdentityTarget struct {
	Pane ResourceID
}

type IdentityBackend interface {
	Kind() BackendKind
	CurrentIdentity(ctx context.Context, target IdentityTarget) (CurrentIdentity, error)
	CurrentPaneID(ctx context.Context, target IdentityTarget) (ResourceID, error)
	CurrentSessionName(ctx context.Context, pane ResourceID) (string, error)
	CurrentNodeName(ctx context.Context, pane ResourceID) (string, error)
}

type IdentityError struct {
	Backend BackendKind
	Failure IdentityFailure
	Field   string
	Err     error
}

func (e IdentityError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%s identity %s", e.Backend, e.Failure)
	}
	return fmt.Sprintf("%s identity %s for %s", e.Backend, e.Failure, e.Field)
}

func (e IdentityError) Unwrap() error {
	return e.Err
}

func (b TmuxBackend) CurrentIdentity(ctx context.Context, target IdentityTarget) (CurrentIdentity, error) {
	pane, err := b.CurrentPaneID(ctx, target)
	if err != nil {
		return CurrentIdentity{}, err
	}
	sessionName, err := b.CurrentSessionName(ctx, pane)
	if err != nil {
		return CurrentIdentity{}, err
	}
	nodeName, err := b.CurrentNodeName(ctx, pane)
	if err != nil {
		return CurrentIdentity{}, err
	}
	return CurrentIdentity{
		Backend:     BackendKindTmux,
		SessionName: sessionName,
		NodeName:    nodeName,
		Pane:        pane,
		NativeIDs: map[string]string{
			"pane_id":      pane.Native,
			"session_name": sessionName,
			"pane_title":   nodeName,
		},
	}, nil
}

func (b TmuxBackend) CurrentPaneID(_ context.Context, target IdentityTarget) (ResourceID, error) {
	if target.Pane != (ResourceID{}) {
		if target.Pane.Backend != BackendKindTmux || target.Pane.Kind != ResourceKindPane || strings.TrimSpace(target.Pane.Native) == "" {
			return ResourceID{}, identityLookupError("pane_id", nil)
		}
		return target.Pane, nil
	}
	out, err := b.Runner.Output("display-message", "-p", "#{pane_id}")
	if err != nil {
		return ResourceID{}, identityLookupError("pane_id", err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return ResourceID{}, identityLookupError("pane_id", nil)
	}
	return TmuxPaneID(value), nil
}

func (b TmuxBackend) CurrentSessionName(_ context.Context, pane ResourceID) (string, error) {
	value, err := b.currentDisplayMessage(pane, "#{session_name}")
	if err != nil {
		return "", identityLookupError("session_name", err)
	}
	if value == "" {
		return "", identityLookupError("session_name", nil)
	}
	return value, nil
}

func (b TmuxBackend) CurrentNodeName(_ context.Context, pane ResourceID) (string, error) {
	value, err := b.currentDisplayMessage(pane, "#{pane_title}")
	if err != nil {
		return "", identityLookupError("pane_title", err)
	}
	if value == "" {
		return "", identityLookupError("pane_title", nil)
	}
	return value, nil
}

func (b TmuxBackend) currentDisplayMessage(pane ResourceID, format string) (string, error) {
	if pane.Backend != BackendKindTmux || pane.Kind != ResourceKindPane || strings.TrimSpace(pane.Native) == "" {
		return "", identityLookupError("pane_id", nil)
	}
	args := []string{"display-message"}
	args = append(args, "-t", pane.Native)
	args = append(args, "-p", format)
	out, err := b.Runner.Output(args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func identityLookupError(field string, err error) IdentityError {
	return IdentityError{
		Backend: BackendKindTmux,
		Failure: IdentityFailureLookupFailed,
		Field:   field,
		Err:     err,
	}
}
