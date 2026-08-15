package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/agentruntime"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type HandKind string

const (
	HandKindTmux  HandKind = "tmux"
	HandKindHerdr HandKind = "herdr"

	BrainRuntimeUnknown = agentruntime.Unknown
)

type Brain struct {
	Runtime string
}

type HandAttachment struct {
	Kind           HandKind
	Address        string
	HerdrRuntimeID multiplexer.HerdrRuntimeIdentity
}

type registeredHerdrHandAdapter struct {
	owner   string
	token   uint64
	adapter HerdrHandAdapter
}

type HerdrHandAdapterReplacement struct {
	Cleanup          func()
	DisplacedCleanup func()
}

type herdrHandAdapterKey struct {
	SocketPath  string
	SessionName string
	WorkspaceID string
	TabID       string
	PaneID      string
}

var (
	registeredHerdrHandAdapters   = make(map[herdrHandAdapterKey][]*registeredHerdrHandAdapter)
	registeredHerdrHandAdaptersMu sync.RWMutex
	herdrHandAdapterToken         atomic.Uint64
	herdrHandAdapterCleanupHook   func(herdrHandAdapterKey, *registeredHerdrHandAdapter)
)

type Target struct {
	ActorID     string
	RunID       string
	Brain       Brain
	Hand        HandAttachment
	SessionName string
	SessionDir  string
}

func TargetForNode(nodeName string, nodeInfo discovery.NodeInfo) Target {
	actorID := nodeaddr.Simple(nodeName)
	runID := nodeName
	if runID == "" || !strings.Contains(runID, ":") {
		switch {
		case nodeInfo.SessionName != "" && actorID != "":
			runID = nodeInfo.SessionName + ":" + actorID
		case actorID != "":
			runID = actorID
		}
	}

	handKind := HandKindTmux
	if multiplexer.BackendKindFromString(nodeInfo.Backend) == multiplexer.BackendKindHerdr {
		handKind = HandKindHerdr
	}
	brainRuntime := BrainRuntimeUnknown
	if nodeInfo.Runtime != "" {
		brainRuntime = nodeInfo.Runtime
	}

	return Target{
		ActorID: actorID,
		RunID:   runID,
		Brain: Brain{
			Runtime: brainRuntime,
		},
		Hand: HandAttachment{
			Kind:    handKind,
			Address: nodeInfo.PaneID,
			HerdrRuntimeID: multiplexer.HerdrRuntimeIdentity{
				SocketPath:  nodeInfo.HerdrSocketPath,
				SessionName: nodeInfo.SessionName,
				WorkspaceID: nodeInfo.HerdrWorkspaceID,
				TabID:       nodeInfo.HerdrTabID,
				PaneID:      nodeInfo.PaneID,
			},
		},
		SessionName: nodeInfo.SessionName,
		SessionDir:  nodeInfo.SessionDir,
	}
}

func (t Target) InboxDir() string {
	return filepath.Join(t.SessionDir, "inbox", t.ActorID)
}

func (t Target) PostPath(filename string) string {
	return filepath.Join(t.SessionDir, "post", filename)
}

type PaneDelivery struct {
	Content        string
	EnterDelay     time.Duration
	TmuxTimeout    time.Duration
	EnterCount     int
	BypassCooldown bool
	VerifyDelay    time.Duration
	MaxRetries     int
}

type SystemMessageDelivery struct {
	Filename        string
	Sender          string
	ThreadID        string
	Content         string
	QueueCap        int
	QueueFullSuffix string
}

type SystemMessageResult struct {
	// Delivered means the final inbox name is visible. Committed distinguishes
	// post-rename failures from ordinary retry-safe failures; Projected records
	// whether the durable mailbox projection was appended and synchronized.
	Delivered bool
	Committed bool
	Projected bool
}

type InteractiveDeliveryAdapter interface {
	Kind() HandKind
	Deliver(target Target, delivery PaneDelivery) error
}

type SystemMessageDeliveryAdapter interface {
	DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error)
}

type HandAdapter interface {
	InteractiveDeliveryAdapter
	SystemMessageDeliveryAdapter
}

type TmuxHandAdapter struct {
	ProbeRuntime func(paneID string) (string, error)
	SendToPane   func(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error
	PaneSender   notification.PaneSender
	Backend      multiplexer.PaneBackend
}

func (TmuxHandAdapter) Kind() HandKind {
	return HandKindTmux
}

func (a TmuxHandAdapter) Deliver(target Target, delivery PaneDelivery) error {
	return TmuxInteractiveDeliveryAdapter(a).Deliver(target, delivery)
}

func (a TmuxHandAdapter) DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error) {
	return (FilesystemSystemMessageAdapter{}).DeliverSystemMessage(target, delivery)
}

type TmuxInteractiveDeliveryAdapter struct {
	ProbeRuntime func(paneID string) (string, error)
	SendToPane   func(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error
	PaneSender   notification.PaneSender
	Backend      multiplexer.PaneBackend
}

type HerdrInteractiveDeliveryAdapter struct {
	Backend        multiplexer.HerdrBackend
	InputSanitizer multiplexer.HerdrInputSanitizer
}

type HerdrHandAdapter struct {
	HerdrInteractiveDeliveryAdapter
	SystemMessageDeliveryAdapter SystemMessageDeliveryAdapter
}

func RegisterHerdrHandAdapter(paneID string, adapter HerdrHandAdapter) func() {
	if strings.TrimSpace(paneID) == "" {
		return func() {}
	}
	keys := herdrHandAdapterKeysForRegistration(adapter.Backend.Config.Runtime, paneID)
	token := herdrHandAdapterToken.Add(1)
	registeredHerdrHandAdaptersMu.Lock()
	for _, key := range keys {
		registeredHerdrHandAdapters[key] = append(registeredHerdrHandAdapters[key], &registeredHerdrHandAdapter{token: token, adapter: adapter})
	}
	registeredHerdrHandAdaptersMu.Unlock()
	return func() {
		var observed []struct {
			key     herdrHandAdapterKey
			current *registeredHerdrHandAdapter
		}
		registeredHerdrHandAdaptersMu.Lock()
		for _, key := range keys {
			current := registeredHerdrHandAdapters[key]
			kept := current[:0]
			for _, candidate := range current {
				if candidate.token != token {
					kept = append(kept, candidate)
					continue
				}
				observed = append(observed, struct {
					key     herdrHandAdapterKey
					current *registeredHerdrHandAdapter
				}{key: key, current: candidate})
			}
			if len(kept) == 0 {
				delete(registeredHerdrHandAdapters, key)
			} else {
				registeredHerdrHandAdapters[key] = kept
			}
		}
		registeredHerdrHandAdaptersMu.Unlock()
		for _, item := range observed {
			if herdrHandAdapterCleanupHook != nil {
				herdrHandAdapterCleanupHook(item.key, item.current)
			}
		}
	}
}

func ReplaceHerdrHandAdaptersForOwner(owner string, adapters map[string]HerdrHandAdapter) func() {
	replacement := ReplaceHerdrHandAdaptersForOwnerCollect(owner, adapters)
	if replacement.DisplacedCleanup != nil {
		replacement.DisplacedCleanup()
	}
	return replacement.Cleanup
}

func ReplaceHerdrHandAdaptersForOwnerCollect(owner string, adapters map[string]HerdrHandAdapter) HerdrHandAdapterReplacement {
	runtimeAdapters := make(map[multiplexer.HerdrRuntimeIdentity]HerdrHandAdapter, len(adapters))
	for paneID, adapter := range adapters {
		runtime := adapter.Backend.Config.Runtime
		if runtime.PaneID == "" {
			runtime.PaneID = paneID
		}
		runtimeAdapters[runtime] = adapter
	}
	return ReplaceHerdrHandAdaptersForOwnerRuntimeCollect(owner, runtimeAdapters)
}

func ReplaceHerdrHandAdaptersForOwnerRuntimeCollect(owner string, adapters map[multiplexer.HerdrRuntimeIdentity]HerdrHandAdapter) HerdrHandAdapterReplacement {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return HerdrHandAdapterReplacement{Cleanup: func() {}, DisplacedCleanup: func() {}}
	}
	token := herdrHandAdapterToken.Add(1)
	var displaced []struct {
		key     herdrHandAdapterKey
		current *registeredHerdrHandAdapter
	}
	registeredHerdrHandAdaptersMu.Lock()
	for key, current := range registeredHerdrHandAdapters {
		kept := current[:0]
		for _, candidate := range current {
			if candidate.owner != owner {
				kept = append(kept, candidate)
				continue
			}
			displaced = append(displaced, struct {
				key     herdrHandAdapterKey
				current *registeredHerdrHandAdapter
			}{key: key, current: candidate})
		}
		if len(kept) == 0 {
			delete(registeredHerdrHandAdapters, key)
		} else {
			registeredHerdrHandAdapters[key] = kept
		}
	}
	for runtime, adapter := range adapters {
		paneID := runtime.PaneID
		if paneID == "" {
			paneID = adapter.Backend.Config.Runtime.PaneID
		}
		for _, key := range herdrHandAdapterKeysForRegistration(runtime, paneID) {
			registeredHerdrHandAdapters[key] = append(registeredHerdrHandAdapters[key], &registeredHerdrHandAdapter{owner: owner, token: token, adapter: adapter})
		}
	}
	registeredHerdrHandAdaptersMu.Unlock()
	displacedCleanup := func() {
		for _, item := range displaced {
			if herdrHandAdapterCleanupHook != nil {
				herdrHandAdapterCleanupHook(item.key, item.current)
			}
		}
	}
	cleanup := func() {
		var observed []struct {
			key     herdrHandAdapterKey
			current *registeredHerdrHandAdapter
		}
		registeredHerdrHandAdaptersMu.Lock()
		for key, current := range registeredHerdrHandAdapters {
			kept := current[:0]
			for _, candidate := range current {
				if candidate.owner != owner || candidate.token != token {
					kept = append(kept, candidate)
					continue
				}
				observed = append(observed, struct {
					key     herdrHandAdapterKey
					current *registeredHerdrHandAdapter
				}{key: key, current: candidate})
			}
			if len(kept) == 0 {
				delete(registeredHerdrHandAdapters, key)
			} else {
				registeredHerdrHandAdapters[key] = kept
			}
		}
		registeredHerdrHandAdaptersMu.Unlock()
		for _, item := range observed {
			if herdrHandAdapterCleanupHook != nil {
				herdrHandAdapterCleanupHook(item.key, item.current)
			}
		}
	}
	return HerdrHandAdapterReplacement{Cleanup: cleanup, DisplacedCleanup: displacedCleanup}
}

func herdrHandAdapterKeysForRegistration(runtime multiplexer.HerdrRuntimeIdentity, paneID string) []herdrHandAdapterKey {
	key := herdrHandAdapterKeyForRuntime(runtime, paneID)
	keys := []herdrHandAdapterKey{key}
	if key.SocketPath != "" || key.WorkspaceID != "" {
		keys = append(keys, herdrHandAdapterKey{
			SessionName: key.SessionName,
			PaneID:      key.PaneID,
		})
	}
	if key.SessionName != "" {
		keys = append(keys, herdrHandAdapterKey{PaneID: key.PaneID})
	}
	return keys
}

func herdrHandAdapterKeyForRuntime(runtime multiplexer.HerdrRuntimeIdentity, paneID string) herdrHandAdapterKey {
	if runtime.PaneID == "" {
		runtime.PaneID = paneID
	}
	return herdrHandAdapterKey{
		SocketPath:  strings.TrimSpace(runtime.SocketPath),
		SessionName: strings.TrimSpace(runtime.SessionName),
		WorkspaceID: strings.TrimSpace(runtime.WorkspaceID),
		TabID:       strings.TrimSpace(runtime.TabID),
		PaneID:      strings.TrimSpace(runtime.PaneID),
	}
}

func herdrHandAdapterKeysForTarget(target Target) []herdrHandAdapterKey {
	runtime := target.Hand.HerdrRuntimeID
	if runtime.SessionName == "" {
		runtime.SessionName = target.SessionName
	}
	if runtime.PaneID == "" {
		runtime.PaneID = target.Hand.Address
	}
	keys := []herdrHandAdapterKey{herdrHandAdapterKeyForRuntime(runtime, target.Hand.Address)}
	if runtime.SocketPath != "" && runtime.WorkspaceID != "" {
		return keys
	}
	if runtime.SessionName != "" {
		runtime.SessionName = ""
		keys = append(keys, herdrHandAdapterKeyForRuntime(runtime, target.Hand.Address))
	}
	return keys
}

func registeredHerdrHandAdapterForKeyLocked(key herdrHandAdapterKey) (*registeredHerdrHandAdapter, bool, error) {
	candidates := registeredHerdrHandAdapters[key]
	switch len(candidates) {
	case 0:
		return nil, false, nil
	case 1:
		return candidates[0], true, nil
	default:
		return nil, true, fmt.Errorf("ambiguous herdr hand adapter registration for pane %q", key.PaneID)
	}
}

func (HerdrInteractiveDeliveryAdapter) Kind() HandKind {
	return HandKindHerdr
}

func (a HerdrInteractiveDeliveryAdapter) Deliver(target Target, delivery PaneDelivery) error {
	if target.Hand.Kind != HandKindHerdr {
		return fmt.Errorf("herdr hand adapter cannot deliver to %q", target.Hand.Kind)
	}
	backend := a.Backend
	if backend.InputSanitizer == nil {
		backend.InputSanitizer = a.InputSanitizer
	}
	if backend.InputSanitizer == nil {
		backend.InputSanitizer = notification.PrepareInteractivePaneMessage
	}
	enterCount := notification.ResolveEnterCount(delivery.EnterCount, func() (string, error) {
		if target.Brain.Runtime != "" && target.Brain.Runtime != BrainRuntimeUnknown {
			return target.Brain.Runtime, nil
		}
		return backend.PaneCurrentCommand(context.Background(), multiplexer.HerdrPaneID(target.Hand.Address))
	})
	return backend.SendPaneInput(context.Background(), multiplexer.HerdrPaneID(target.Hand.Address), multiplexer.HerdrPaneInput{
		Text:       delivery.Content,
		EnterCount: enterCount,
	})
}

func (a HerdrHandAdapter) Kind() HandKind {
	return HandKindHerdr
}

func (a HerdrHandAdapter) Deliver(target Target, delivery PaneDelivery) error {
	return a.HerdrInteractiveDeliveryAdapter.Deliver(target, delivery)
}

func (a HerdrHandAdapter) DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error) {
	adapter := a.SystemMessageDeliveryAdapter
	if adapter == nil {
		adapter = FilesystemSystemMessageAdapter{}
	}
	return adapter.DeliverSystemMessage(target, delivery)
}

type generationBoundHerdrHandAdapter struct {
	generation uint64
	adapter    HerdrHandAdapter
}

func (a generationBoundHerdrHandAdapter) Kind() HandKind {
	return HandKindHerdr
}

func (a generationBoundHerdrHandAdapter) Deliver(target Target, delivery PaneDelivery) error {
	multiplexer.LockHerdrPublicationRead()
	defer multiplexer.UnlockHerdrPublicationRead()
	if multiplexer.HerdrPublicationGenerationLocked() != a.generation {
		return fmt.Errorf("stale herdr hand adapter generation")
	}
	return a.adapter.Deliver(target, delivery)
}

func (a generationBoundHerdrHandAdapter) DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error) {
	multiplexer.LockHerdrPublicationRead()
	defer multiplexer.UnlockHerdrPublicationRead()
	if multiplexer.HerdrPublicationGenerationLocked() != a.generation {
		return SystemMessageResult{}, fmt.Errorf("stale herdr hand adapter generation")
	}
	return a.adapter.DeliverSystemMessage(target, delivery)
}

func (TmuxInteractiveDeliveryAdapter) Kind() HandKind {
	return HandKindTmux
}

func (a TmuxInteractiveDeliveryAdapter) Deliver(target Target, delivery PaneDelivery) error {
	if target.Hand.Kind != HandKindTmux {
		return fmt.Errorf("tmux hand adapter cannot deliver to %q", target.Hand.Kind)
	}

	probeRuntime := a.ProbeRuntime
	if probeRuntime == nil {
		backend := a.Backend
		if backend == nil {
			backend = multiplexer.TmuxBackend{}
		}
		probeRuntime = func(paneID string) (string, error) {
			return backend.PaneCurrentCommand(context.Background(), multiplexer.TmuxPaneID(paneID))
		}
	}
	sendToPane := a.SendToPane
	if sendToPane == nil {
		sendToPane = notification.SendToPane
	}

	enterCount := notification.ResolveEnterCount(delivery.EnterCount, func() (string, error) {
		if target.Brain.Runtime != "" && target.Brain.Runtime != BrainRuntimeUnknown {
			return target.Brain.Runtime, nil
		}
		return probeRuntime(target.Hand.Address)
	})

	paneSender := a.PaneSender
	if paneSender == nil {
		paneSender = notification.PaneSenderFunc(func(paneDelivery notification.PaneDelivery) error {
			return sendToPane(
				paneDelivery.PaneID,
				paneDelivery.Message,
				paneDelivery.EnterDelay,
				paneDelivery.TmuxTimeout,
				paneDelivery.EnterCount,
				paneDelivery.BypassCooldown,
				paneDelivery.VerifyDelay,
				paneDelivery.MaxRetries,
			)
		})
	}

	return paneSender.DeliverPane(notification.PaneDelivery{
		PaneID:         target.Hand.Address,
		Message:        delivery.Content,
		EnterDelay:     delivery.EnterDelay,
		TmuxTimeout:    delivery.TmuxTimeout,
		EnterCount:     enterCount,
		BypassCooldown: delivery.BypassCooldown,
		VerifyDelay:    delivery.VerifyDelay,
		MaxRetries:     delivery.MaxRetries,
	})
}

type FilesystemSystemMessageAdapter struct{}

var syncInboxDirectoryFn = func(inboxDir string) error {
	dir, err := os.Open(inboxDir)
	if err != nil {
		return fmt.Errorf("opening inbox for durability sync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("syncing inbox directory: %w", err)
	}
	return nil
}

var (
	appendMailboxProjectionPayloadFn = appendMailboxProjectionPayload
	syncMailboxProjectionFn          = projection.SyncMailboxProjection
	openCurrentWriterFn              = journal.OpenCurrentWriter
	beforeSystemMessageCommitFn      = func(string) error { return nil }
	afterSystemMessageCommitFn       = func(string) error { return nil }
)

func SetOpenCurrentWriterForTest(fn func(string) (*journal.Writer, error)) func() {
	original := openCurrentWriterFn
	openCurrentWriterFn = fn
	return func() { openCurrentWriterFn = original }
}

func (FilesystemSystemMessageAdapter) DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error) {
	recipientInbox := target.InboxDir()
	if err := os.MkdirAll(recipientInbox, 0o700); err != nil {
		return SystemMessageResult{}, fmt.Errorf("creating recipient inbox: %w", err)
	}

	if count, countErr := countInboxMessages(recipientInbox); countErr == nil && count >= delivery.QueueCap {
		log.Printf("postman: inbox queue full for %s (cap=%d, current=%d): leaving %s undelivered for retry\n", target.ActorID, delivery.QueueCap, count, delivery.Filename)
		return SystemMessageResult{Delivered: false}, nil
	}

	dst := filepath.Join(recipientInbox, delivery.Filename)
	tmp, err := os.CreateTemp(recipientInbox, "."+delivery.Filename+".tmp-")
	if err != nil {
		return SystemMessageResult{}, fmt.Errorf("creating inbox draft: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return SystemMessageResult{}, fmt.Errorf("setting inbox draft permissions: %w", err)
	}
	if _, err := tmp.WriteString(delivery.Content); err != nil {
		tmp.Close()
		return SystemMessageResult{}, fmt.Errorf("writing inbox draft: %w", err)
	}
	if err := beforeSystemMessageCommitFn("write"); err != nil {
		tmp.Close()
		return SystemMessageResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return SystemMessageResult{}, fmt.Errorf("syncing inbox draft: %w", err)
	}
	if err := beforeSystemMessageCommitFn("file-sync"); err != nil {
		tmp.Close()
		return SystemMessageResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return SystemMessageResult{}, fmt.Errorf("closing inbox draft: %w", err)
	}
	if err := beforeSystemMessageCommitFn("file-close"); err != nil {
		return SystemMessageResult{}, err
	}
	if err := beforeSystemMessageCommitFn("rename"); err != nil {
		return SystemMessageResult{}, err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return SystemMessageResult{}, fmt.Errorf("committing inbox delivery: %w", err)
	}
	committed := SystemMessageResult{Delivered: true, Committed: true}
	if err := afterSystemMessageCommitFn("directory-open"); err != nil {
		return committed, err
	}
	if err := syncInboxDirectoryFn(recipientInbox); err != nil {
		return committed, fmt.Errorf("inbox delivery committed but durability sync failed: %w", err)
	}
	if err := afterSystemMessageCommitFn("directory-close"); err != nil {
		return committed, err
	}
	if err := afterSystemMessageCommitFn("final-stat"); err != nil {
		return committed, err
	}
	if _, err := os.Stat(dst); err != nil {
		return committed, fmt.Errorf("inbox delivery committed but final verification failed: %w", err)
	}
	payload := journal.MailboxEventPayload{
		MessageID: delivery.Filename,
		From:      delivery.Sender,
		To:        target.ActorID,
		ThreadID:  delivery.ThreadID,
		Path:      shadowRelativePath(target.SessionDir, dst),
		Content:   delivery.Content,
	}
	if err := appendMailboxProjectionPayloadFn(target.SessionDir, target.SessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, payload); err != nil {
		return committed, fmt.Errorf("inbox delivery committed but projection append failed (recoverable by projection sync): %w", err)
	}
	if err := syncMailboxProjectionFn(target.SessionDir); err != nil {
		return committed, fmt.Errorf("inbox delivery committed but projection sync failed (recoverable by projection sync): %w", err)
	}

	committed.Projected = true
	return committed, nil
}

func DefaultHandAdapter(target Target) (HandAdapter, error) {
	switch target.Hand.Kind {
	case HandKindTmux:
		return TmuxHandAdapter{}, nil
	case HandKindHerdr:
		multiplexer.LockHerdrPublicationRead()
		defer multiplexer.UnlockHerdrPublicationRead()
		registeredHerdrHandAdaptersMu.RLock()
		defer registeredHerdrHandAdaptersMu.RUnlock()
		for _, key := range herdrHandAdapterKeysForTarget(target) {
			registration, ok, err := registeredHerdrHandAdapterForKeyLocked(key)
			if err != nil {
				return nil, err
			}
			if ok {
				return generationBoundHerdrHandAdapter{
					generation: multiplexer.HerdrPublicationGenerationLocked(),
					adapter:    registration.adapter,
				}, nil
			}
		}
		return nil, fmt.Errorf("herdr hand adapter not registered for %q", target.Hand.Address)
	default:
		return nil, fmt.Errorf("unsupported hand kind %q", target.Hand.Kind)
	}
}

func recordMailboxProjectionPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) {
	if err := appendMailboxProjectionPayload(sessionDir, sessionName, eventType, visibility, payload); err != nil {
		log.Printf("postman: WARNING: component=%s event=append_failed mailbox_event=%s err=%v\n", projection.MailboxProjectionComponent, eventType, err)
	}
}

func appendMailboxProjectionPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) error {
	payload = enrichMailboxProjectionPayload(payload)
	now := time.Now()
	if _, err := journal.RecordProcessMailboxPayloadIfAbsent(sessionDir, sessionName, eventType, visibility, payload, sameMailboxProjectionPayload(eventType, payload), now); err != nil {
		return err
	}
	writer, err := openCurrentWriterFn(sessionDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("opening current writer after mailbox commit: %w", err)
		}
		if _, shadowErr := journal.OpenShadowWriter(sessionDir, "system-message-delivery", sessionName, os.Getpid(), now); shadowErr != nil {
			return fmt.Errorf("opening current writer after mailbox commit: %w", err)
		}
		writer, err = openCurrentWriterFn(sessionDir)
		if err != nil {
			return fmt.Errorf("opening current writer after mailbox commit: %w", err)
		}
	}
	_, _, err = writer.AppendCurrentSessionEventIfAbsent(eventType, visibility, payload, journal.AppendOptions{
		ThreadID: payload.ThreadID,
	}, now, sameMailboxProjectionPayload(eventType, payload))
	return err
}

func sameMailboxProjectionPayload(eventType string, want journal.MailboxEventPayload) journal.EventEquivalenceFunc {
	return func(event journal.Event) (bool, error) {
		if event.Type != eventType {
			return false, nil
		}
		var got journal.MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &got); err != nil {
			return false, err
		}
		return got.MessageID == want.MessageID &&
			got.Path == want.Path &&
			got.SourcePath == want.SourcePath &&
			got.From == want.From &&
			got.To == want.To &&
			got.ThreadID == want.ThreadID, nil
	}
}

// enrichMailboxProjectionPayload keeps the durable mailbox event aligned with
// the logical envelope identity. System delivery may use a privileged transport
// path, but the journal must retain the requester and approval correlation IDs
// authored in the envelope rather than treating the transport as the sender.
func enrichMailboxProjectionPayload(payload journal.MailboxEventPayload) journal.MailboxEventPayload {
	if payload.Content == "" {
		return payload
	}
	metadata, err := envelope.ParseMetadata(payload.Content)
	if err != nil {
		return payload
	}
	if payload.MessageID == "" {
		payload.MessageID = metadata.MessageID
	}
	if payload.From == "" {
		payload.From = metadata.From
	}
	if payload.To == "" {
		payload.To = metadata.To
	}
	if payload.ThreadID == "" {
		payload.ThreadID = metadata.ThreadID
	}
	if payload.InputRequestID == "" {
		payload.InputRequestID = metadata.InputRequestID
	}
	if payload.FillsInputRequestID == "" {
		payload.FillsInputRequestID = metadata.FillsInputRequestID
	}
	if payload.InputRequestSetID == "" {
		payload.InputRequestSetID = metadata.InputRequestSetID
	}
	return payload
}

func syncMailboxProjection(sessionDir string) {
	if err := projection.SyncMailboxProjection(sessionDir); err != nil {
		log.Printf("postman: WARNING: component=%s event=sync_failed session_dir=%s err=%v\n", projection.MailboxProjectionComponent, sessionDir, err)
	}
}

func countInboxMessages(inboxDir string) (int, error) {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			n++
		}
	}
	return n, nil
}

func shadowRelativePath(sessionDir, fullPath string) string {
	rel, err := filepath.Rel(sessionDir, fullPath)
	if err != nil {
		return filepath.Base(fullPath)
	}
	return rel
}
