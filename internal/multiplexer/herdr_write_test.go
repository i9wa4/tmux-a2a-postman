package multiplexer

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

func herdrOwnerKeyForTest(t *testing.T, sessionName string) string {
	t.Helper()
	key, err := HerdrSessionOwnerMetadataKey(sessionName)
	if err != nil {
		t.Fatalf("HerdrSessionOwnerMetadataKey(%q) error = %v", sessionName, err)
	}
	return key
}

func TestHerdrMetadataTokenKeysSatisfyHerdrGrammar(t *testing.T) {
	for _, key := range []string{
		herdrOwnerKeyForTest(t, "work"),
		herdrOwnerKeyForTest(t, strings.Repeat("long-session-name-", 20)),
		HerdrPaneContextIDMetadataKey,
		HerdrPostmanNodeMetadataKey,
	} {
		t.Run(key, func(t *testing.T) {
			if err := validateHerdrMetadataToken(key); err != nil {
				t.Fatalf("validateHerdrMetadataToken(%q) error = %v", key, err)
			}
		})
	}
}

func TestHerdrSessionOwnerMetadataKeyIsPerSessionAndStable(t *testing.T) {
	work := herdrOwnerKeyForTest(t, "work")
	other := herdrOwnerKeyForTest(t, "other")
	if work == other {
		t.Fatalf("owner keys for distinct sessions are equal: %q", work)
	}
	if again := herdrOwnerKeyForTest(t, "work"); again != work {
		t.Fatalf("owner key is not stable: %q then %q", work, again)
	}
	if !strings.HasPrefix(work, herdrSessionOwnerMetadataKeyPrefix) {
		t.Fatalf("owner key %q lacks prefix %q", work, herdrSessionOwnerMetadataKeyPrefix)
	}
	for _, bad := range []string{"", "   ", "bad\x1bname"} {
		if _, err := HerdrSessionOwnerMetadataKey(bad); err == nil {
			t.Fatalf("HerdrSessionOwnerMetadataKey(%q) error = nil, want rejection", bad)
		}
	}
}

func TestValidateHerdrMetadataTokenRejectsInvalidKeys(t *testing.T) {
	for _, key := range []string{
		"",
		"postman" + ".session_owner",
		"postman/context",
		"postman context",
		"postman_context_é",
		strings.Repeat("a", 33),
	} {
		t.Run(key, func(t *testing.T) {
			if err := validateHerdrMetadataToken(key); err == nil {
				t.Fatalf("validateHerdrMetadataToken(%q) error = nil, want rejection", key)
			}
		})
	}
}

func TestHerdrBackendSendPaneInputRequiresWriteGateBeforeClientCall(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	config := validHerdrReadConfig()
	config.Policy.WriteEnabled = false
	backend := HerdrBackend{
		Config:         config,
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SendPaneInput(context.Background(), HerdrPaneID("workspace-1:pane-1"), HerdrPaneInput{Text: "hello"})
	assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "", HerdrGateFailureClosed)
	if client.snapshotCalls != 0 || client.writeTextCalls != 0 || client.sendKeyCalls != 0 {
		t.Fatalf("client calls = snapshot:%d write:%d key:%d, want none", client.snapshotCalls, client.writeTextCalls, client.sendKeyCalls)
	}
}

func TestHerdrBackendSendPaneInputRequiresWriteClientBeforeSnapshot(t *testing.T) {
	client := &fakeHerdrReadClient{snapshot: validHerdrSessionSnapshot()}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SendPaneInput(context.Background(), HerdrPaneID("workspace-1:pane-1"), HerdrPaneInput{Text: "hello"})
	if !errors.Is(err, ErrHerdrWriteClientMissing) {
		t.Fatalf("SendPaneInput() error = %v, want ErrHerdrWriteClientMissing", err)
	}
	if client.snapshotCalls != 0 {
		t.Fatalf("snapshotCalls = %d, want 0 before write client is present", client.snapshotCalls)
	}
}

func TestHerdrBackendSendPaneInputSanitizesBeforeWrite(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config: validHerdrReadConfig(),
		Client: client,
		InputSanitizer: func(text string) (string, error) {
			if text != "hello" {
				t.Fatalf("sanitizer input = %q, want hello", text)
			}
			return "sanitized:" + text, nil
		},
	}

	err := backend.SendPaneInput(context.Background(), HerdrPaneID("workspace-1:pane-1"), HerdrPaneInput{Text: "hello", EnterCount: 2})
	if err != nil {
		t.Fatalf("SendPaneInput() error = %v", err)
	}
	if client.snapshotCalls != 1 {
		t.Fatalf("snapshotCalls = %d, want 1", client.snapshotCalls)
	}
	if client.writeTextCalls != 1 || client.writeTextPane != "workspace-1:pane-1" || client.writeTextText != "sanitized:hello" {
		t.Fatalf("write text call = calls:%d pane:%q text:%q, want sanitized write", client.writeTextCalls, client.writeTextPane, client.writeTextText)
	}
	if client.sendKeyCalls != 2 || client.sendKeyPane != "workspace-1:pane-1" || client.sendKeyKey != HerdrKeySubmit {
		t.Fatalf("send key call = calls:%d pane:%q key:%q, want two Enter keys", client.sendKeyCalls, client.sendKeyPane, client.sendKeyKey)
	}
}

func TestHerdrBackendSendPaneInputNormalizesUnavailableWriteError(t *testing.T) {
	client := &fakeHerdrWriteClient{
		fakeHerdrReadClient: fakeHerdrReadClient{snapshot: validHerdrSessionSnapshot()},
		writeTextErr:        net.ErrClosed,
	}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SendPaneInput(context.Background(), HerdrPaneID("workspace-1:pane-1"), HerdrPaneInput{Text: "hello"})
	if !errors.Is(err, ErrHerdrBackendUnavailable) {
		t.Fatalf("SendPaneInput() error = %v, want ErrHerdrBackendUnavailable", err)
	}
}

func TestHerdrBackendSendPaneInputRequiresSanitizerBeforeSnapshot(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config: validHerdrReadConfig(),
		Client: client,
	}

	err := backend.SendPaneInput(context.Background(), HerdrPaneID("workspace-1:pane-1"), HerdrPaneInput{Text: "hello"})
	if !errors.Is(err, ErrHerdrInputSanitizerMissing) {
		t.Fatalf("SendPaneInput() error = %v, want ErrHerdrInputSanitizerMissing", err)
	}
	if client.snapshotCalls != 0 || client.writeTextCalls != 0 {
		t.Fatalf("client calls = snapshot:%d write:%d, want none before sanitizer passes", client.snapshotCalls, client.writeTextCalls)
	}
}

func TestHerdrBackendSendPaneInputRequiresSnapshotContainmentBeforeWrite(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Panes[0].TabID = "workspace-1:other-tab"
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: snapshot,
	}}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SendPaneInput(context.Background(), HerdrPaneID("workspace-1:pane-1"), HerdrPaneInput{Text: "hello"})
	if !errors.Is(err, ErrHerdrSnapshotInvalid) {
		t.Fatalf("SendPaneInput() error = %v, want ErrHerdrSnapshotInvalid", err)
	}
	if client.writeTextCalls != 0 || client.sendKeyCalls != 0 {
		t.Fatalf("mutation calls = write:%d key:%d, want none before snapshot containment passes", client.writeTextCalls, client.sendKeyCalls)
	}
}

func TestHerdrBackendSessionOwnerMarkerUsesWorkspaceMetadata(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Workspaces[0].Metadata = map[string]string{herdrOwnerKeyForTest(t, "work"): "ctx-1:123"}
	client := &fakeHerdrReadClient{snapshot: snapshot}
	backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}

	got, err := backend.SessionOwnerMarker(context.Background(), "work")
	if err != nil {
		t.Fatalf("SessionOwnerMarker() error = %v", err)
	}
	if got != "ctx-1:123" {
		t.Fatalf("SessionOwnerMarker() = %q, want marker", got)
	}
}

func TestHerdrBackendSessionOwnerMarkerIgnoresLegacySessionSuffixedMetadata(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	// The real prior key: "postman.session_owner." + session name (invalid under Herdr 0.8.2).
	legacyKey := "postman" + ".session_owner.work"
	snapshot.Workspaces[0].Metadata = map[string]string{legacyKey: "ctx-1:123"}
	client := &fakeHerdrReadClient{snapshot: snapshot}
	backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}

	got, err := backend.SessionOwnerMarker(context.Background(), "work")
	if err != nil {
		t.Fatalf("SessionOwnerMarker() error = %v", err)
	}
	if got != "" {
		t.Fatalf("SessionOwnerMarker() = %q, want no legacy marker", got)
	}
}

func TestHerdrBackendSetAndClearSessionOwnerMarkerUseWorkspaceMetadata(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	if err := backend.SetSessionOwnerMarker(context.Background(), "ctx-1", "work", 123); err != nil {
		t.Fatalf("SetSessionOwnerMarker() error = %v", err)
	}
	if client.setWorkspaceMetadataCalls != 1 ||
		client.setWorkspaceMetadataID != "workspace-1" ||
		client.setWorkspaceMetadataKey != herdrOwnerKeyForTest(t, "work") ||
		client.setWorkspaceMetadataValue != "ctx-1:123" {
		t.Fatalf("set workspace metadata = calls:%d id:%q key:%q value:%q, want session marker",
			client.setWorkspaceMetadataCalls,
			client.setWorkspaceMetadataID,
			client.setWorkspaceMetadataKey,
			client.setWorkspaceMetadataValue)
	}

	if err := backend.ClearSessionOwnerMarker(context.Background(), "work"); err != nil {
		t.Fatalf("ClearSessionOwnerMarker() error = %v", err)
	}
	if client.clearWorkspaceMetadataCalls != 1 ||
		client.clearWorkspaceMetadataID != "workspace-1" ||
		client.clearWorkspaceMetadataKey != herdrOwnerKeyForTest(t, "work") {
		t.Fatalf("clear workspace metadata = calls:%d id:%q key:%q, want session marker clear",
			client.clearWorkspaceMetadataCalls,
			client.clearWorkspaceMetadataID,
			client.clearWorkspaceMetadataKey)
	}
}

func TestHerdrBackendSetSessionOwnerMarkerRejectsUnsafeMarkerBeforeSnapshot(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SetSessionOwnerMarker(context.Background(), "ctx-\x1b[31m1", "work", 123)
	if err == nil {
		t.Fatal("SetSessionOwnerMarker() error = nil, want unsafe metadata error")
	}
	if client.snapshotCalls != 0 || client.setWorkspaceMetadataCalls != 0 {
		t.Fatalf("client calls = snapshot:%d setWorkspace:%d, want none before marker sanitization passes", client.snapshotCalls, client.setWorkspaceMetadataCalls)
	}
}

func TestHerdrBackendSetSessionOwnerMarkerRejectsWhitespaceContextBeforeConcat(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SetSessionOwnerMarker(context.Background(), "   ", "work", 123)
	if err == nil {
		t.Fatal("SetSessionOwnerMarker() error = nil, want empty context error")
	}
	if client.snapshotCalls != 0 || client.setWorkspaceMetadataCalls != 0 {
		t.Fatalf("client calls = snapshot:%d setWorkspace:%d, want none before context sanitization passes", client.snapshotCalls, client.setWorkspaceMetadataCalls)
	}
}

func TestHerdrBackendSetPaneOwnerMarkerRequiresWriteGateBeforeMetadata(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	config := validHerdrReadConfig()
	config.Policy.WriteEnabled = false
	backend := HerdrBackend{
		Config:         config,
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SetPaneOwnerMarker(context.Background(), HerdrPaneID("workspace-1:pane-1"), "ctx-1")
	assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "", HerdrGateFailureClosed)
	if client.snapshotCalls != 0 || client.setPaneMetadataCalls != 0 {
		t.Fatalf("client calls = snapshot:%d setPane:%d, want none before write gate", client.snapshotCalls, client.setPaneMetadataCalls)
	}
}

func TestHerdrBackendSetPaneOwnerMarkerRejectsUnsafeMarkerBeforeSnapshot(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	err := backend.SetPaneOwnerMarker(context.Background(), HerdrPaneID("workspace-1:pane-1"), "ctx-\x1b[31m1")
	if err == nil {
		t.Fatal("SetPaneOwnerMarker() error = nil, want unsafe metadata error")
	}
	if client.snapshotCalls != 0 || client.setPaneMetadataCalls != 0 {
		t.Fatalf("client calls = snapshot:%d setPane:%d, want none before marker sanitization passes", client.snapshotCalls, client.setPaneMetadataCalls)
	}
}

func TestHerdrBackendPaneOwnerMarkerUsesPaneMetadata(t *testing.T) {
	snapshot := validHerdrSessionSnapshot()
	snapshot.Panes[0].Metadata[HerdrPaneContextIDMetadataKey] = "ctx-1"
	client := &fakeHerdrReadClient{snapshot: snapshot}
	backend := HerdrBackend{Config: validHerdrReadConfig(), Client: client}

	got, err := backend.PaneOwnerMarker(context.Background(), HerdrPaneID("workspace-1:pane-1"))
	if err != nil {
		t.Fatalf("PaneOwnerMarker() error = %v", err)
	}
	if got != "ctx-1" {
		t.Fatalf("PaneOwnerMarker() = %q, want marker", got)
	}
}

func TestHerdrBackendSetAndClearPaneOwnerMarkerUsePaneMetadata(t *testing.T) {
	client := &fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
		snapshot: validHerdrSessionSnapshot(),
	}}
	backend := HerdrBackend{
		Config:         validHerdrReadConfig(),
		Client:         client,
		InputSanitizer: passThroughHerdrInput,
	}

	if err := backend.SetPaneOwnerMarker(context.Background(), HerdrPaneID("workspace-1:pane-1"), "ctx-1"); err != nil {
		t.Fatalf("SetPaneOwnerMarker() error = %v", err)
	}
	if client.setPaneMetadataCalls != 1 ||
		client.setPaneMetadataID != "workspace-1:pane-1" ||
		client.setPaneMetadataKey != HerdrPaneContextIDMetadataKey ||
		client.setPaneMetadataValue != "ctx-1" {
		t.Fatalf("set pane metadata = calls:%d id:%q key:%q value:%q, want pane marker",
			client.setPaneMetadataCalls,
			client.setPaneMetadataID,
			client.setPaneMetadataKey,
			client.setPaneMetadataValue)
	}

	if err := backend.ClearPaneOwnerMarker(context.Background(), HerdrPaneID("workspace-1:pane-1")); err != nil {
		t.Fatalf("ClearPaneOwnerMarker() error = %v", err)
	}
	if client.clearPaneMetadataCalls != 1 ||
		client.clearPaneMetadataID != "workspace-1:pane-1" ||
		client.clearPaneMetadataKey != HerdrPaneContextIDMetadataKey {
		t.Fatalf("clear pane metadata = calls:%d id:%q key:%q, want pane marker clear",
			client.clearPaneMetadataCalls,
			client.clearPaneMetadataID,
			client.clearPaneMetadataKey)
	}
}

// statefulWorkspaceMetadataClient applies workspace metadata writes to the
// snapshot it serves, so tests can read back what set and clear did.
type statefulWorkspaceMetadataClient struct {
	fakeHerdrWriteClient

	metadata map[string]string
}

func (c *statefulWorkspaceMetadataClient) SessionSnapshot(ctx context.Context) (HerdrSessionSnapshot, error) {
	snapshot, err := c.fakeHerdrWriteClient.SessionSnapshot(ctx)
	if err != nil {
		return snapshot, err
	}
	workspaces := make([]HerdrWorkspaceSnapshot, len(snapshot.Workspaces))
	copy(workspaces, snapshot.Workspaces)
	for i := range workspaces {
		metadata := make(map[string]string, len(c.metadata))
		for key, value := range c.metadata {
			metadata[key] = value
		}
		workspaces[i].Metadata = metadata
	}
	snapshot.Workspaces = workspaces
	return snapshot, nil
}

func (c *statefulWorkspaceMetadataClient) SetWorkspaceMetadata(ctx context.Context, workspaceID string, key string, value string) (HerdrWriteResult, error) {
	c.metadata[key] = value
	return c.fakeHerdrWriteClient.SetWorkspaceMetadata(ctx, workspaceID, key, value)
}

func (c *statefulWorkspaceMetadataClient) ClearWorkspaceMetadata(ctx context.Context, workspaceID string, key string) (HerdrWriteResult, error) {
	delete(c.metadata, key)
	return c.fakeHerdrWriteClient.ClearWorkspaceMetadata(ctx, workspaceID, key)
}

func TestHerdrBackendSessionOwnerMarkersAreIsolatedPerSessionInSharedWorkspace(t *testing.T) {
	client := &statefulWorkspaceMetadataClient{
		fakeHerdrWriteClient: fakeHerdrWriteClient{fakeHerdrReadClient: fakeHerdrReadClient{
			snapshot: validHerdrSessionSnapshot(),
		}},
		metadata: map[string]string{},
	}
	newBackend := func(sessionName string) HerdrBackend {
		config := validHerdrReadConfig()
		config.Runtime.SessionName = sessionName
		config.Policy.AllowedSessions = []string{"work", "other"}
		return HerdrBackend{Config: config, Client: client, InputSanitizer: passThroughHerdrInput}
	}
	work := newBackend("work")
	other := newBackend("other")
	ctx := context.Background()

	if err := work.SetSessionOwnerMarker(ctx, "ctx-work", "work", 111); err != nil {
		t.Fatalf("work SetSessionOwnerMarker() error = %v", err)
	}
	if err := other.SetSessionOwnerMarker(ctx, "ctx-other", "other", 222); err != nil {
		t.Fatalf("other SetSessionOwnerMarker() error = %v", err)
	}
	if got, err := work.SessionOwnerMarker(ctx, "work"); err != nil || got != "ctx-work:111" {
		t.Fatalf("work SessionOwnerMarker() = %q, %v; want ctx-work:111 after other session wrote", got, err)
	}
	if got, err := other.SessionOwnerMarker(ctx, "other"); err != nil || got != "ctx-other:222" {
		t.Fatalf("other SessionOwnerMarker() = %q, %v; want ctx-other:222", got, err)
	}

	if err := work.ClearSessionOwnerMarker(ctx, "work"); err != nil {
		t.Fatalf("work ClearSessionOwnerMarker() error = %v", err)
	}
	if got, err := work.SessionOwnerMarker(ctx, "work"); err != nil || got != "" {
		t.Fatalf("work SessionOwnerMarker() after clear = %q, %v; want empty", got, err)
	}
	if got, err := other.SessionOwnerMarker(ctx, "other"); err != nil || got != "ctx-other:222" {
		t.Fatalf("other SessionOwnerMarker() after work clear = %q, %v; want live marker ctx-other:222", got, err)
	}
	if len(client.metadata) != 1 {
		t.Fatalf("workspace metadata = %#v, want only other session's key", client.metadata)
	}
}

func passThroughHerdrInput(text string) (string, error) {
	return text, nil
}

type fakeHerdrWriteClient struct {
	fakeHerdrReadClient

	writeTextResult HerdrWriteResult
	writeTextErr    error
	sendKeyResult   HerdrWriteResult
	sendKeyErr      error
	metadataResult  HerdrWriteResult
	metadataErr     error

	writeTextCalls int
	writeTextPane  string
	writeTextText  string
	sendKeyCalls   int
	sendKeyPane    string
	sendKeyKey     string

	setWorkspaceMetadataCalls int
	setWorkspaceMetadataID    string
	setWorkspaceMetadataKey   string
	setWorkspaceMetadataValue string

	clearWorkspaceMetadataCalls int
	clearWorkspaceMetadataID    string
	clearWorkspaceMetadataKey   string

	setPaneMetadataCalls int
	setPaneMetadataID    string
	setPaneMetadataKey   string
	setPaneMetadataValue string

	clearPaneMetadataCalls int
	clearPaneMetadataID    string
	clearPaneMetadataKey   string
}

func (f *fakeHerdrWriteClient) WritePaneText(_ context.Context, paneID string, text string) (HerdrWriteResult, error) {
	f.writeTextCalls++
	f.writeTextPane = paneID
	f.writeTextText = text
	if f.writeTextErr != nil {
		return HerdrWriteResult{}, f.writeTextErr
	}
	if f.writeTextResult.Envelope.ProtocolVersion != "" {
		return f.writeTextResult, nil
	}
	return HerdrWriteResult{Envelope: validHerdrEnvelope()}, nil
}

func (f *fakeHerdrWriteClient) SendPaneKey(_ context.Context, paneID string, key string) (HerdrWriteResult, error) {
	f.sendKeyCalls++
	f.sendKeyPane = paneID
	f.sendKeyKey = key
	if f.sendKeyErr != nil {
		return HerdrWriteResult{}, f.sendKeyErr
	}
	if f.sendKeyResult.Envelope.ProtocolVersion != "" {
		return f.sendKeyResult, nil
	}
	return HerdrWriteResult{Envelope: validHerdrEnvelope()}, nil
}

func (f *fakeHerdrWriteClient) SetWorkspaceMetadata(_ context.Context, workspaceID string, key string, value string) (HerdrWriteResult, error) {
	f.setWorkspaceMetadataCalls++
	f.setWorkspaceMetadataID = workspaceID
	f.setWorkspaceMetadataKey = key
	f.setWorkspaceMetadataValue = value
	return f.metadataWriteResult()
}

func (f *fakeHerdrWriteClient) ClearWorkspaceMetadata(_ context.Context, workspaceID string, key string) (HerdrWriteResult, error) {
	f.clearWorkspaceMetadataCalls++
	f.clearWorkspaceMetadataID = workspaceID
	f.clearWorkspaceMetadataKey = key
	return f.metadataWriteResult()
}

func (f *fakeHerdrWriteClient) SetPaneMetadata(_ context.Context, paneID string, key string, value string) (HerdrWriteResult, error) {
	f.setPaneMetadataCalls++
	f.setPaneMetadataID = paneID
	f.setPaneMetadataKey = key
	f.setPaneMetadataValue = value
	return f.metadataWriteResult()
}

func (f *fakeHerdrWriteClient) ClearPaneMetadata(_ context.Context, paneID string, key string) (HerdrWriteResult, error) {
	f.clearPaneMetadataCalls++
	f.clearPaneMetadataID = paneID
	f.clearPaneMetadataKey = key
	return f.metadataWriteResult()
}

func (f *fakeHerdrWriteClient) metadataWriteResult() (HerdrWriteResult, error) {
	if f.metadataErr != nil {
		return HerdrWriteResult{}, f.metadataErr
	}
	if f.metadataResult.Envelope.ProtocolVersion != "" {
		return f.metadataResult, nil
	}
	return HerdrWriteResult{Envelope: validHerdrEnvelope()}, nil
}
