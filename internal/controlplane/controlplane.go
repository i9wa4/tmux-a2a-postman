package controlplane

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/agentruntime"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type HandKind string

const (
	HandKindTmux HandKind = "tmux"

	BrainRuntimeUnknown = agentruntime.Unknown
)

type Brain struct {
	Runtime string
}

type HandAttachment struct {
	Kind    HandKind
	Address string
}

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

	return Target{
		ActorID: actorID,
		RunID:   runID,
		Brain: Brain{
			Runtime: BrainRuntimeUnknown,
		},
		Hand: HandAttachment{
			Kind:    HandKindTmux,
			Address: nodeInfo.PaneID,
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
	Delivered bool
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
	return TmuxInteractiveDeliveryAdapter{
		ProbeRuntime: a.ProbeRuntime,
		SendToPane:   a.SendToPane,
		PaneSender:   a.PaneSender,
		Backend:      a.Backend,
	}.Deliver(target, delivery)
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
	if err := os.WriteFile(dst, []byte(delivery.Content), 0o600); err != nil {
		return SystemMessageResult{}, fmt.Errorf("writing to inbox: %w", err)
	}
	recordMailboxProjectionPayload(target.SessionDir, target.SessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: delivery.Filename,
		From:      delivery.Sender,
		To:        target.ActorID,
		ThreadID:  delivery.ThreadID,
		Path:      shadowRelativePath(target.SessionDir, dst),
		Content:   delivery.Content,
	})
	syncMailboxProjection(target.SessionDir)

	return SystemMessageResult{Delivered: true}, nil
}

func DefaultHandAdapter(target Target) (HandAdapter, error) {
	switch target.Hand.Kind {
	case HandKindTmux:
		return TmuxHandAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported hand kind %q", target.Hand.Kind)
	}
}

func recordMailboxProjectionPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) {
	if err := journal.RecordProcessMailboxPayload(sessionDir, sessionName, eventType, visibility, payload, time.Now()); err != nil {
		log.Printf("postman: WARNING: component=%s event=append_failed mailbox_event=%s err=%v\n", projection.MailboxProjectionComponent, eventType, err)
	}
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
