package multiplexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	HerdrKeyEnter                 = "Enter"
	HerdrSessionOwnerMetadataKey  = "postman.session_owner"
	HerdrPaneContextIDMetadataKey = "postman.context_id"
)

var (
	ErrHerdrWriteClientMissing    = errors.New("herdr write client missing")
	ErrHerdrInputSanitizerMissing = errors.New("herdr input sanitizer missing")
)

type HerdrInputSanitizer func(string) (string, error)

type HerdrWriteClient interface {
	HerdrReadClient
	WritePaneText(ctx context.Context, paneID string, text string) (HerdrWriteResult, error)
	SendPaneKey(ctx context.Context, paneID string, key string) (HerdrWriteResult, error)
	SetWorkspaceMetadata(ctx context.Context, workspaceID string, key string, value string) (HerdrWriteResult, error)
	ClearWorkspaceMetadata(ctx context.Context, workspaceID string, key string) (HerdrWriteResult, error)
	SetPaneMetadata(ctx context.Context, paneID string, key string, value string) (HerdrWriteResult, error)
	ClearPaneMetadata(ctx context.Context, paneID string, key string) (HerdrWriteResult, error)
}

type HerdrPaneInput struct {
	Text       string
	EnterCount int
}

type HerdrWriteResult struct {
	Envelope HerdrResponseEnvelope
}

func (b HerdrBackend) SendPaneInput(ctx context.Context, pane ResourceID, input HerdrPaneInput) error {
	if err := b.authorizeWritePath(); err != nil {
		return err
	}
	if pane.Backend != BackendKindHerdr || pane.Kind != ResourceKindPane {
		return fmt.Errorf("herdr input requires herdr pane resource: %#v", pane)
	}
	if pane.Native != b.Config.Runtime.PaneID {
		return ErrHerdrPaneTargetMismatch
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	sanitized, err := b.sanitizePaneInput(input.Text)
	if err != nil {
		return err
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.WritePaneText(ctx, pane.Native, sanitized)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	if err := b.validateWriteEnvelope(result.Envelope); err != nil {
		return err
	}
	for range herdrSubmitEnterCount(input.EnterCount) {
		result, err := client.SendPaneKey(ctx, pane.Native, HerdrKeyEnter)
		if err != nil {
			return NormalizeHerdrBackendError(err)
		}
		if err := b.validateWriteEnvelope(result.Envelope); err != nil {
			return err
		}
	}
	return nil
}

func (b HerdrBackend) SessionOwnerMarker(ctx context.Context, sessionName string) (string, error) {
	if err := b.authorizeReadPath(HerdrReadScopeDiscovery); err != nil {
		return "", err
	}
	if sessionName != b.Config.Runtime.SessionName {
		return "", ErrHerdrSessionNameMismatch
	}
	snapshot, err := b.readValidatedSnapshot(ctx, HerdrReadScopeDiscovery)
	if err != nil {
		return "", err
	}
	for _, workspace := range snapshot.Workspaces {
		if workspace.ID == b.Config.Runtime.WorkspaceID {
			return workspace.Metadata[HerdrSessionOwnerMetadataKey], nil
		}
	}
	return "", nil
}

func (b HerdrBackend) SetSessionOwnerMarker(ctx context.Context, contextID, sessionName string, pid int) error {
	if err := b.authorizeWritePath(); err != nil {
		return err
	}
	if contextID == "" {
		return fmt.Errorf("context ID is empty")
	}
	if sessionName != b.Config.Runtime.SessionName {
		return ErrHerdrSessionNameMismatch
	}
	if pid <= 0 {
		pid = os.Getpid()
	}
	markerValue, err := sanitizeHerdrMetadataValue(contextID + ":" + strconv.Itoa(pid))
	if err != nil {
		return err
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.SetWorkspaceMetadata(ctx, b.Config.Runtime.WorkspaceID, HerdrSessionOwnerMetadataKey, markerValue)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	return b.validateWriteEnvelope(result.Envelope)
}

func (b HerdrBackend) ClearSessionOwnerMarker(ctx context.Context, sessionName string) error {
	if err := b.authorizeWritePath(); err != nil {
		return err
	}
	if sessionName != b.Config.Runtime.SessionName {
		return ErrHerdrSessionNameMismatch
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.ClearWorkspaceMetadata(ctx, b.Config.Runtime.WorkspaceID, HerdrSessionOwnerMetadataKey)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	return b.validateWriteEnvelope(result.Envelope)
}

func (b HerdrBackend) PaneOwnerMarker(ctx context.Context, pane ResourceID) (string, error) {
	if err := b.authorizeReadPath(HerdrReadScopePane); err != nil {
		return "", err
	}
	if pane.Backend != BackendKindHerdr || pane.Kind != ResourceKindPane {
		return "", fmt.Errorf("herdr pane owner marker requires herdr pane resource: %#v", pane)
	}
	if pane.Native != b.Config.Runtime.PaneID {
		return "", ErrHerdrPaneTargetMismatch
	}
	snapshot, err := b.readValidatedSnapshot(ctx, HerdrReadScopePane)
	if err != nil {
		return "", err
	}
	if err := b.validatePaneContainment(snapshot, b.Config.Runtime.TabID, pane.Native); err != nil {
		return "", err
	}
	for _, snapshotPane := range snapshot.Panes {
		if snapshotPane.ID == pane.Native {
			return snapshotPane.Metadata[HerdrPaneContextIDMetadataKey], nil
		}
	}
	return "", nil
}

func (b HerdrBackend) SetPaneOwnerMarker(ctx context.Context, pane ResourceID, contextID string) error {
	if err := b.authorizeWritePath(); err != nil {
		return err
	}
	if contextID == "" {
		return fmt.Errorf("context ID is empty")
	}
	markerValue, err := sanitizeHerdrMetadataValue(contextID)
	if err != nil {
		return err
	}
	if pane.Backend != BackendKindHerdr || pane.Kind != ResourceKindPane {
		return fmt.Errorf("herdr pane owner marker requires herdr pane resource: %#v", pane)
	}
	if pane.Native != b.Config.Runtime.PaneID {
		return ErrHerdrPaneTargetMismatch
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.SetPaneMetadata(ctx, pane.Native, HerdrPaneContextIDMetadataKey, markerValue)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	return b.validateWriteEnvelope(result.Envelope)
}

func (b HerdrBackend) ClearPaneOwnerMarker(ctx context.Context, pane ResourceID) error {
	if err := b.authorizeWritePath(); err != nil {
		return err
	}
	if pane.Backend != BackendKindHerdr || pane.Kind != ResourceKindPane {
		return fmt.Errorf("herdr pane owner marker requires herdr pane resource: %#v", pane)
	}
	if pane.Native != b.Config.Runtime.PaneID {
		return ErrHerdrPaneTargetMismatch
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	if err := b.validateConfiguredPaneInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.ClearPaneMetadata(ctx, pane.Native, HerdrPaneContextIDMetadataKey)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	return b.validateWriteEnvelope(result.Envelope)
}

func (b HerdrBackend) authorizeWritePath() error {
	if !b.Config.Enabled {
		return ErrHerdrReadDisabled
	}
	if b.Client == nil {
		return ErrHerdrReadClientMissing
	}
	envelope := b.localReadGateEnvelope()
	return ValidateHerdrWriteGate(b.Config.Policy, b.Config.Runtime, envelope)
}

func (b HerdrBackend) validateWriteEnvelope(envelope HerdrResponseEnvelope) error {
	return ValidateHerdrWriteGate(b.Config.Policy, b.Config.Runtime, envelope)
}

func (b HerdrBackend) writeClient() (HerdrWriteClient, error) {
	client, ok := b.Client.(HerdrWriteClient)
	if !ok {
		return nil, ErrHerdrWriteClientMissing
	}
	return client, nil
}

func (b HerdrBackend) sanitizePaneInput(text string) (string, error) {
	if b.InputSanitizer == nil {
		return "", ErrHerdrInputSanitizerMissing
	}
	return b.InputSanitizer(text)
}

func herdrSubmitEnterCount(configured int) int {
	if configured <= 0 {
		return 1
	}
	return configured
}

func sanitizeHerdrMetadataValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("herdr metadata value is empty")
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("herdr metadata value is invalid UTF-8")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return "", fmt.Errorf("herdr metadata value contains control character")
		}
	}
	return value, nil
}
