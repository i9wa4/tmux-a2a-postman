package multiplexer

import (
	"context"
	"errors"
	"testing"
)

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
	if client.sendKeyCalls != 2 || client.sendKeyPane != "workspace-1:pane-1" || client.sendKeyKey != HerdrKeyEnter {
		t.Fatalf("send key call = calls:%d pane:%q key:%q, want two Enter keys", client.sendKeyCalls, client.sendKeyPane, client.sendKeyKey)
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
	snapshot.Workspaces[0].Metadata = map[string]string{HerdrSessionOwnerMetadataKey: "ctx-1:123"}
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
		client.setWorkspaceMetadataKey != HerdrSessionOwnerMetadataKey ||
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
		client.clearWorkspaceMetadataKey != HerdrSessionOwnerMetadataKey {
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
