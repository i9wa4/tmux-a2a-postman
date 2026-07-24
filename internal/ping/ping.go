package ping

import (
	"fmt"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
	"github.com/i9wa4/tmux-a2a-postman/internal/traceid"
)

type SendOptions struct {
	CompactionTriggered bool
	Runtime             string
	// CorrelationID lets upstream trigger detectors bind their decision event to
	// the generated envelope and delivery result. Empty means generate one here.
	CorrelationID string
	// TriggerFamily identifies the initiating production path. It is deliberately
	// supplied by the caller, never inferred from timing or message content.
	TriggerFamily TriggerFamily
}

type TriggerFamily string

const (
	TriggerFamilyRuntimeAuto TriggerFamily = "runtime-auto"
	TriggerFamilyCompaction  TriggerFamily = "compaction"
	TriggerFamilyManualTUI   TriggerFamily = "manual-tui"
	TriggerFamilyOther       TriggerFamily = "other"
)

func (family TriggerFamily) String() string {
	return string(family)
}

func normalizeTriggerFamily(family TriggerFamily) (TriggerFamily, error) {
	if family == "" {
		return TriggerFamilyOther, nil
	}
	switch family {
	case TriggerFamilyRuntimeAuto, TriggerFamilyCompaction, TriggerFamilyManualTUI, TriggerFamilyOther:
		return family, nil
	default:
		return "", fmt.Errorf("unsupported trigger family %q", family)
	}
}

// ExtractSimpleName extracts the simple node name from a session-prefixed name.
// If the name contains ":", returns the part after ":". Otherwise, returns the name as-is.
func ExtractSimpleName(fullName string) string {
	parts := strings.SplitN(fullName, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return fullName
}

// SendPingToNode sends a PING message to a specific node.
// nodeName should be the full session-prefixed name (session:node).
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	_, err := SendPingToNodeWithResult(nodeInfo, contextID, nodeName, tmpl, cfg, activeNodes, livenessMap, adjacency, nodes)
	return err
}

func SendPingToNodeWithResult(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
	return SendPingToNodeWithOptions(nodeInfo, contextID, nodeName, tmpl, cfg, activeNodes, livenessMap, adjacency, nodes, SendOptions{TriggerFamily: TriggerFamilyRuntimeAuto})
}

func SendPingToNodeWithOptions(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, options SendOptions) (controlplane.SystemMessageResult, error) {
	target := controlplane.TargetForNode(nodeName, nodeInfo)
	simpleName := target.ActorID
	sourceSessionName := target.SessionName

	now := time.Now()
	ts := now.Format("20060102-150405")

	// Use simple name in filename (Issue #33: keep filenames simple)
	filename, err := message.GenerateFilename(ts, "postman", simpleName, sourceSessionName)
	if err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("generating filename: %w", err)
	}
	postPath := target.PostPath(filename)
	correlationID := options.CorrelationID
	if correlationID == "" {
		var err error
		correlationID, err = NewCorrelationID()
		if err != nil {
			return controlplane.SystemMessageResult{}, fmt.Errorf("generating correlation ID: %w", err)
		}
	}
	if err := traceid.ValidateCorrelationID(correlationID); err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("invalid correlation ID: %w", err)
	}
	triggerFamily, err := normalizeTriggerFamily(options.TriggerFamily)
	if err != nil {
		return controlplane.SystemMessageResult{}, err
	}

	// Daemon PINGs bootstrap liveness, so their body route hints must come from
	// discovered topology rather than the already-confirmed liveness map.
	content := envelope.BuildEnvelope(cfg, tmpl, simpleName, "postman", contextID, postPath, activeNodes, adjacency, nodes, sourceSessionName, nil)

	// Pass 2: inject daemon message variables.
	var skillCatalogs []string
	if cfg != nil {
		if pingCatalog := cfg.PingSkillCatalogForRuntime(options.Runtime); pingCatalog != "" {
			skillCatalogs = append(skillCatalogs, pingCatalog)
		}
		if options.CompactionTriggered {
			if compactionCatalog := cfg.CompactionSkillCatalogForRuntime(options.Runtime); compactionCatalog != "" {
				skillCatalogs = append(skillCatalogs, compactionCatalog)
			}
		}
	}
	roleContent := envelope.BuildRoleContentWithAppendix(cfg, simpleName, joinSkillCatalogs(skillCatalogs))
	content = template.ExpandVariables(content, map[string]string{
		"message_type": "ping",
		"heading":      "Ping",
		"message":      "PING from postman daemon. Do NOT reply to this message.",
		"role_content": roleContent,
	})
	content, changed, err := envelope.InjectParamsMetadata(content, map[string]string{
		"correlation_id": correlationID,
		"trigger_family": triggerFamily.String(),
	})
	if err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("injecting trace metadata: %w", err)
	}
	if !changed {
		return controlplane.SystemMessageResult{}, fmt.Errorf("daemon PING template does not support structured params trace metadata")
	}
	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("parsing injected trace metadata: %w", err)
	}
	if metadata.CorrelationID != correlationID || metadata.TriggerFamily != triggerFamily.String() {
		return controlplane.SystemMessageResult{}, fmt.Errorf("injected trace metadata did not round-trip")
	}

	message.LogPingTrace("ping_attempt", filename, target, contextID, correlationID, triggerFamily.String(), "attempted")
	result, err := message.DeliverSystemMessageDirectResultToTarget(filename, target, "postman", contextID, content, cfg, adjacency, nodes, livenessMap)
	if err != nil {
		message.LogPingTrace("ping_result", filename, target, contextID, correlationID, triggerFamily.String(), "error")
		return result, err
	}
	status := "undelivered"
	if result.Delivered {
		status = "delivered"
	}
	message.LogPingTrace("ping_result", filename, target, contextID, correlationID, triggerFamily.String(), status)
	return result, nil
}

func NewCorrelationID() (string, error) {
	return traceid.NewCorrelationID()
}

func joinSkillCatalogs(catalogs []string) string {
	var parts []string
	for _, catalog := range catalogs {
		catalog = strings.TrimSpace(catalog)
		if catalog != "" {
			parts = append(parts, catalog)
		}
	}
	return strings.Join(parts, "\n\n")
}
