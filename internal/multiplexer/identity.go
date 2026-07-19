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

type IdentityBackend interface {
	Kind() BackendKind
	CurrentIdentity(ctx context.Context, paneID string) (CurrentIdentity, error)
	CurrentPaneID(ctx context.Context, paneID string) (ResourceID, error)
	CurrentSessionName(ctx context.Context, paneID string) (string, error)
	CurrentNodeName(ctx context.Context, paneID string) (string, error)
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

func (b TmuxBackend) CurrentIdentity(ctx context.Context, paneID string) (CurrentIdentity, error) {
	pane, err := b.CurrentPaneID(ctx, paneID)
	if err != nil {
		return CurrentIdentity{}, err
	}
	sessionName, err := b.CurrentSessionName(ctx, pane.Native)
	if err != nil {
		return CurrentIdentity{}, err
	}
	nodeName, err := b.CurrentNodeName(ctx, pane.Native)
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

func (b TmuxBackend) CurrentPaneID(_ context.Context, paneID string) (ResourceID, error) {
	if paneID != "" {
		return TmuxPaneID(paneID), nil
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

func (b TmuxBackend) CurrentSessionName(_ context.Context, paneID string) (string, error) {
	value, err := b.currentDisplayMessage(paneID, "#{session_name}")
	if err != nil {
		return "", identityLookupError("session_name", err)
	}
	return value, nil
}

func (b TmuxBackend) CurrentNodeName(_ context.Context, paneID string) (string, error) {
	value, err := b.currentDisplayMessage(paneID, "#{pane_title}")
	if err != nil {
		return "", identityLookupError("pane_title", err)
	}
	return value, nil
}

func (b TmuxBackend) currentDisplayMessage(paneID, format string) (string, error) {
	args := []string{"display-message"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
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
