package controlplane

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type HandKind string

const (
	HandKindTmux HandKind = "tmux"

	BrainRuntimeUnknown = "unknown"
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

type HeartbeatTrigger struct {
	Filename string
	Content  string
	TTL      time.Duration
}

type HeartbeatTriggerResult struct {
	Written bool
}

type HandAdapter interface {
	Kind() HandKind
	Deliver(target Target, delivery PaneDelivery) error
	DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error)
	WriteHeartbeatTrigger(target Target, trigger HeartbeatTrigger) (HeartbeatTriggerResult, error)
}

type TmuxHandAdapter struct {
	ProbeRuntime func(paneID string) (string, error)
	SendToPane   func(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error
}

func (TmuxHandAdapter) Kind() HandKind {
	return HandKindTmux
}

func (a TmuxHandAdapter) Deliver(target Target, delivery PaneDelivery) error {
	if target.Hand.Kind != HandKindTmux {
		return fmt.Errorf("tmux hand adapter cannot deliver to %q", target.Hand.Kind)
	}

	probeRuntime := a.ProbeRuntime
	if probeRuntime == nil {
		probeRuntime = func(paneID string) (string, error) {
			out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_current_command}").Output()
			return strings.TrimSpace(string(out)), err
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

	return sendToPane(
		target.Hand.Address,
		delivery.Content,
		delivery.EnterDelay,
		delivery.TmuxTimeout,
		enterCount,
		delivery.BypassCooldown,
		delivery.VerifyDelay,
		delivery.MaxRetries,
	)
}

func (TmuxHandAdapter) DeliverSystemMessage(target Target, delivery SystemMessageDelivery) (SystemMessageResult, error) {
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
	recordCompatibilityMailboxPayload(target.SessionDir, target.SessionName, "compatibility_mailbox_delivered", journal.VisibilityCompatibilityMailbox, journal.MailboxEventPayload{
		MessageID: delivery.Filename,
		From:      delivery.Sender,
		To:        target.ActorID,
		ThreadID:  delivery.ThreadID,
		Path:      shadowRelativePath(target.SessionDir, dst),
		Content:   delivery.Content,
	})
	syncCompatibilityMailbox(target.SessionDir)

	return SystemMessageResult{Delivered: true}, nil
}

func (TmuxHandAdapter) WriteHeartbeatTrigger(target Target, trigger HeartbeatTrigger) (HeartbeatTriggerResult, error) {
	inboxDir := target.InboxDir()
	entries, err := os.ReadDir(inboxDir)
	if err != nil && !os.IsNotExist(err) {
		return HeartbeatTriggerResult{}, fmt.Errorf("reading inbox %s: %w", inboxDir, err)
	}

	now := time.Now()
	unread := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		filePath := filepath.Join(inboxDir, entry.Name())
		if age > trigger.TTL {
			deadLetter := filepath.Join(target.SessionDir, "dead-letter", entry.Name())
			if err := os.Rename(filePath, deadLetter); err != nil {
				return HeartbeatTriggerResult{}, fmt.Errorf("recycling stale trigger: %w", err)
			}
		} else {
			unread++
		}
	}

	if unread > 0 {
		return HeartbeatTriggerResult{}, nil
	}

	filePath := target.PostPath(trigger.Filename)
	if err := os.WriteFile(filePath, []byte(trigger.Content), 0o600); err != nil {
		return HeartbeatTriggerResult{}, fmt.Errorf("writing trigger: %w", err)
	}
	return HeartbeatTriggerResult{Written: true}, nil
}

func DefaultHandAdapter(target Target) (HandAdapter, error) {
	switch target.Hand.Kind {
	case HandKindTmux:
		return TmuxHandAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported hand kind %q", target.Hand.Kind)
	}
}

func deadLetterDst(sessionDir, filename, suffix string) string {
	base := strings.TrimSuffix(filename, ".md")
	return filepath.Join(sessionDir, "dead-letter", base+suffix+".md")
}

func validateDeadLetterTarget(dstPath string) error {
	deadLetterDir := filepath.Dir(dstPath)
	dirInfo, err := os.Lstat(deadLetterDir)
	if err != nil {
		return fmt.Errorf("lstat dead-letter dir: %w", err)
	}
	if dirInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("dead-letter target dir is symlink: %s", deadLetterDir)
	}

	dstInfo, err := os.Lstat(dstPath)
	if err == nil {
		if dstInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("dead-letter target is symlink: %s", dstPath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat dead-letter target: %w", err)
	}
	return nil
}

func writeDeadLetterFile(dstPath string, content []byte) error {
	if err := validateDeadLetterTarget(dstPath); err != nil {
		return err
	}
	return os.WriteFile(dstPath, content, 0o600)
}

func recordCompatibilityMailboxPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) {
	if err := journal.RecordProcessMailboxPayload(sessionDir, sessionName, eventType, visibility, payload, time.Now()); err != nil {
		log.Printf("postman: WARNING: journal compatibility append failed for %s: %v\n", eventType, err)
	}
}

func syncCompatibilityMailbox(sessionDir string) {
	if err := projection.SyncCompatibilityMailbox(sessionDir); err != nil {
		log.Printf("postman: WARNING: compatibility mailbox sync failed for %s: %v\n", sessionDir, err)
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
