package cli

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

// makeBuilderInputs returns a minimal sessionStatusInputs for two edge nodes.
func makeBuilderInputs(nodeNames []string) sessionStatusInputs {
	edgeNodeRank := make(map[string]int, len(nodeNames))
	nodes := make(map[string]discovery.NodeInfo, len(nodeNames))
	inboxCounts := make(map[string]int, len(nodeNames))
	paneActivity := make(map[string]paneActivityEvidence, len(nodeNames))

	for i, name := range nodeNames {
		edgeNodeRank[name] = i
		paneID := "%" + name
		nodes["session:"+name] = discovery.NodeInfo{
			PaneID:      paneID,
			SessionName: "session",
		}
		paneActivity[paneID] = paneActivityEvidence{Status: "live"}
	}

	return sessionStatusInputs{
		contextID:        "ctx",
		sessionName:      "session",
		sessionDir:       "",
		orderedEdgeNodes: nodeNames,
		edgeNodeRank:     edgeNodeRank,
		nodes:            nodes,
		paneActivity:     paneActivity,
		queues:           status.SessionQueues{},
		inboxCounts:      inboxCounts,
		delivery:         &status.DeliveryStatus{State: "ok", Severity: "ok"},
		blockedByNode:    map[string][]projection.BlockedReport{},
	}
}

func TestBuildSessionStatusSnapshotSeverityMatrix(t *testing.T) {
	t.Run("ok_when_all_nodes_active", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha", "beta"})
		result := buildSessionStatusSnapshot(inputs)

		if result.Severity != "ok" {
			t.Errorf("expected severity ok, got %q", result.Severity)
		}
		if result.NodeCount != 2 {
			t.Errorf("expected 2 nodes, got %d", result.NodeCount)
		}
	})

	t.Run("blocked_severity_propagates_from_node", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha"})
		inputs.blockedByNode = map[string][]projection.BlockedReport{
			"alpha": {
				{
					Node:            "alpha",
					MessageID:       "msg-001",
					BlockedReportID: "br-001",
					EvidenceLevel:   "proven",
					EvidenceSource:  "blocked_report_file",
					Reason:          "waiting for human",
				},
			},
		}
		result := buildSessionStatusSnapshot(inputs)

		if result.Severity != "blocked" {
			t.Errorf("expected severity blocked, got %q", result.Severity)
		}
		if result.Nodes[0].Flow == nil || result.Nodes[0].Flow.State != "blocked" {
			t.Errorf("expected node flow state blocked")
		}
	})

	t.Run("needs_action_when_input_required", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha"})
		inputs.useInputRequests = true
		inputs.inputRequests = projection.MessageInputRequestState{
			InputRequiredCounts:  map[string]int{"alpha": 1},
			WaitingOnInputCounts: map[string]int{},
			InfoUnreadCounts:     map[string]int{},
			UnreadCounts:         map[string]int{"alpha": 0},
		}
		// Inbox count below unread threshold so inputRequiredCount stays positive
		inputs.inboxCounts["alpha"] = 0
		result := buildSessionStatusSnapshot(inputs)

		if result.Severity != "needs_action" {
			t.Errorf("expected severity needs_action, got %q", result.Severity)
		}
	})

	t.Run("expected_wait_when_waiting_on_input", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha"})
		inputs.useInputRequests = true
		inputs.inputRequests = projection.MessageInputRequestState{
			InputRequiredCounts:  map[string]int{},
			WaitingOnInputCounts: map[string]int{"alpha": 1},
			InfoUnreadCounts:     map[string]int{},
			UnreadCounts:         map[string]int{},
		}
		result := buildSessionStatusSnapshot(inputs)

		if result.Severity != "expected_wait" {
			t.Errorf("expected severity expected_wait, got %q", result.Severity)
		}
	})

	t.Run("delivery_stuck_propagates_to_session", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha"})
		inputs.delivery = &status.DeliveryStatus{
			State:                "delivery_stuck",
			Severity:             "delivery_stuck",
			PostCount:            1,
			OldestPostAgeSeconds: 400,
			Reason:               "oldest post item is at or above delivery_stuck threshold",
		}
		result := buildSessionStatusSnapshot(inputs)

		if result.Severity != "delivery_stuck" {
			t.Errorf("expected severity delivery_stuck, got %q", result.Severity)
		}
	})

	t.Run("delivery_failure_propagates_to_session", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha"})
		inputs.delivery = &status.DeliveryStatus{
			State:           "delivery_failure",
			Severity:        "delivery_failure",
			DeadLetterCount: 2,
			Reason:          "dead-letter files exist",
		}
		result := buildSessionStatusSnapshot(inputs)

		if result.Severity != "delivery_failure" {
			t.Errorf("expected severity delivery_failure, got %q", result.Severity)
		}
	})

	t.Run("node_order_follows_edge_rank", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"zeta", "alpha", "mu"})
		result := buildSessionStatusSnapshot(inputs)

		if result.NodeCount != 3 {
			t.Fatalf("expected 3 nodes, got %d", result.NodeCount)
		}
		if result.Nodes[0].Name != "zeta" || result.Nodes[1].Name != "alpha" || result.Nodes[2].Name != "mu" {
			t.Errorf("unexpected node order: %v %v %v",
				result.Nodes[0].Name, result.Nodes[1].Name, result.Nodes[2].Name)
		}
	})

	t.Run("blocked_beats_needs_action_in_severity", func(t *testing.T) {
		inputs := makeBuilderInputs([]string{"alpha"})
		inputs.blockedByNode = map[string][]projection.BlockedReport{
			"alpha": {{Node: "alpha", EvidenceLevel: "proven", EvidenceSource: "blocked_report_file"}},
		}
		inputs.useInputRequests = true
		inputs.inputRequests = projection.MessageInputRequestState{
			InputRequiredCounts:  map[string]int{"alpha": 1},
			WaitingOnInputCounts: map[string]int{},
			InfoUnreadCounts:     map[string]int{},
			UnreadCounts:         map[string]int{"alpha": 0},
		}
		inputs.inboxCounts["alpha"] = 0
		result := buildSessionStatusSnapshot(inputs)

		// blocked > needs_action in severity ranking
		if result.Severity != "blocked" {
			t.Errorf("expected severity blocked (beats needs_action), got %q", result.Severity)
		}
	})
}
