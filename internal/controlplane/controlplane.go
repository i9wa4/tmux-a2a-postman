package controlplane

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
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

type HandAdapter interface {
	Kind() HandKind
	Deliver(target Target, delivery PaneDelivery) error
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

func DefaultHandAdapter(target Target) (HandAdapter, error) {
	switch target.Hand.Kind {
	case HandKindTmux:
		return TmuxHandAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported hand kind %q", target.Hand.Kind)
	}
}
