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
	// Match tmux delivery's Codex submit key; literal Enter may insert a newline.
	HerdrKeySubmit                = "C-m"
	HerdrSessionOwnerMetadataKey  = "postman_owner"
	HerdrPaneContextIDMetadataKey = "postman_context"
	HerdrPostmanNodeMetadataKey   = "postman_node"
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
		result, err := client.SendPaneKey(ctx, pane.Native, HerdrKeySubmit)
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
	metadataKey, err := herdrSessionOwnerMetadataKey(sessionName)
	if err != nil {
		return "", err
	}
	snapshot, err := b.readValidatedSnapshot(ctx, HerdrReadScopeDiscovery)
	if err != nil {
		return "", err
	}
	for _, workspace := range snapshot.Workspaces {
		if workspace.ID == b.Config.Runtime.WorkspaceID {
			return workspace.Metadata[metadataKey], nil
		}
	}
	return "", nil
}

func (b HerdrBackend) SetSessionOwnerMarker(ctx context.Context, contextID, sessionName string, pid int) error {
	if err := b.authorizeWorkspaceWritePath(); err != nil {
		return err
	}
	if sessionName != b.Config.Runtime.SessionName {
		return ErrHerdrSessionNameMismatch
	}
	if pid <= 0 {
		pid = os.Getpid()
	}
	contextID, err := sanitizeHerdrMetadataValue(contextID)
	if err != nil {
		return err
	}
	metadataKey, err := herdrSessionOwnerMetadataKey(sessionName)
	if err != nil {
		return err
	}
	markerValue, err := sanitizeHerdrMetadataValue(contextID + ":" + strconv.Itoa(pid))
	if err != nil {
		return err
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	if err := b.validateConfiguredWorkspaceInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.SetWorkspaceMetadata(ctx, b.Config.Runtime.WorkspaceID, metadataKey, markerValue)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	return b.validateWorkspaceWriteEnvelope(result.Envelope)
}

func (b HerdrBackend) ClearSessionOwnerMarker(ctx context.Context, sessionName string) error {
	if err := b.authorizeWorkspaceWritePath(); err != nil {
		return err
	}
	if sessionName != b.Config.Runtime.SessionName {
		return ErrHerdrSessionNameMismatch
	}
	metadataKey, err := herdrSessionOwnerMetadataKey(sessionName)
	if err != nil {
		return err
	}
	client, err := b.writeClient()
	if err != nil {
		return err
	}
	if err := b.validateConfiguredWorkspaceInSnapshot(ctx); err != nil {
		return err
	}
	result, err := client.ClearWorkspaceMetadata(ctx, b.Config.Runtime.WorkspaceID, metadataKey)
	if err != nil {
		return NormalizeHerdrBackendError(err)
	}
	return b.validateWorkspaceWriteEnvelope(result.Envelope)
}

func (b HerdrBackend) validateConfiguredWorkspaceInSnapshot(ctx context.Context) error {
	_, err := b.readValidatedSnapshot(ctx, HerdrReadScopeDiscovery)
	return err
}

func herdrSessionOwnerMetadataKey(sessionName string) (string, error) {
	if _, err := sanitizeHerdrMetadataValue(sessionName); err != nil {
		return "", fmt.Errorf("invalid herdr session owner metadata value: %w", err)
	}
	return validatedHerdrMetadataToken(HerdrSessionOwnerMetadataKey)
}

func herdrPaneContextMetadataKey() (string, error) {
	return validatedHerdrMetadataToken(HerdrPaneContextIDMetadataKey)
}

func validatedHerdrMetadataToken(key string) (string, error) {
	if err := validateHerdrMetadataToken(key); err != nil {
		return "", err
	}
	return key, nil
}

func validateHerdrMetadataToken(key string) error {
	if key == "" {
		return fmt.Errorf("invalid herdr metadata token: empty")
	}
	if len(key) > 32 {
		return fmt.Errorf("invalid herdr metadata token %q: exceeds 32 bytes", key)
	}
	for _, r := range key {
		if r >= utf8.RuneSelf {
			return fmt.Errorf("invalid herdr metadata token %q: non-ascii character %q", key, r)
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid herdr metadata token %q: character %q is not allowed", key, r)
	}
	return nil
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
	metadataKey, err := herdrPaneContextMetadataKey()
	if err != nil {
		return "", err
	}
	for _, snapshotPane := range snapshot.Panes {
		if snapshotPane.ID == pane.Native && snapshotPane.WorkspaceID == b.Config.Runtime.WorkspaceID && snapshotPane.TabID == b.Config.Runtime.TabID {
			return snapshotPane.Metadata[metadataKey], nil
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
	metadataKey, err := herdrPaneContextMetadataKey()
	if err != nil {
		return err
	}
	result, err := client.SetPaneMetadata(ctx, pane.Native, metadataKey, markerValue)
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
	metadataKey, err := herdrPaneContextMetadataKey()
	if err != nil {
		return err
	}
	result, err := client.ClearPaneMetadata(ctx, pane.Native, metadataKey)
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

func (b HerdrBackend) authorizeWorkspaceWritePath() error {
	if !b.Config.Enabled {
		return ErrHerdrReadDisabled
	}
	if b.Client == nil {
		return ErrHerdrReadClientMissing
	}
	envelope := b.localReadGateEnvelope()
	return validateHerdrWorkspaceWriteGate(b.Config.Policy, b.Config.Runtime, envelope)
}

func (b HerdrBackend) validateWriteEnvelope(envelope HerdrResponseEnvelope) error {
	return ValidateHerdrWriteGate(b.Config.Policy, b.Config.Runtime, envelope)
}

func (b HerdrBackend) validateWorkspaceWriteEnvelope(envelope HerdrResponseEnvelope) error {
	return validateHerdrWorkspaceWriteGate(b.Config.Policy, b.Config.Runtime, envelope)
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
