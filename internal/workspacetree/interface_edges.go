package workspacetree

import (
	"sort"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

// Derived interface edges (#624/#700, Guardian ruling r24ca).
//
// A derived cross-session edge exists between two sessions only when ALL hold:
//   - both appear exactly once in this daemon's own workspace_tree and one is
//     the DIRECT parent of the other (no transitive A-C edge, no sibling edge);
//   - both declare a bare interface node (the daemon's own session through
//     [postman] interface_node, every other session through its own entry).
//
// The edge joins exactly those two declared nodes. Nothing is cached or merged
// into cfg.Edges: every decision is evaluated against the live view supplied
// by the caller, so a node that disappears, a disabled or unowned session, or
// a node rebound into another context stops being authorized immediately.
// This is the ONE evaluator for CLI pre-check, authoritative delivery,
// notification footers and status; the static cfg.Edges path stays separate.
// It grants communication only: it never grants verdict exemption, escalation,
// review or command approval, which keep reading the own-session interface node
// through config.ConfiguredInterfaceNode.

// InterfaceEdgeReason names why a derived-edge decision was made.
type InterfaceEdgeReason string

const (
	InterfaceEdgeAllowed           InterfaceEdgeReason = "allowed"
	InterfaceEdgeNoInterfaceNode   InterfaceEdgeReason = "no_interface_node"
	InterfaceEdgeUnqualified       InterfaceEdgeReason = "unqualified_address"
	InterfaceEdgeSameSession       InterfaceEdgeReason = "same_session"
	InterfaceEdgeUnknownSession    InterfaceEdgeReason = "unknown_session"
	InterfaceEdgeAmbiguousSession  InterfaceEdgeReason = "ambiguous_session"
	InterfaceEdgeNotDirectRelative InterfaceEdgeReason = "not_direct_relative"
	InterfaceEdgeUndeclared        InterfaceEdgeReason = "undeclared"
	InterfaceEdgeNotInterfaceNode  InterfaceEdgeReason = "not_interface_node"
	InterfaceEdgeNodeAbsent        InterfaceEdgeReason = "node_absent"
	InterfaceEdgeNodeAmbiguous     InterfaceEdgeReason = "node_ambiguous"
	InterfaceEdgeSessionDisabled   InterfaceEdgeReason = "session_disabled"
	InterfaceEdgeSessionNotOwned   InterfaceEdgeReason = "session_not_owned"
)

// LiveView is the caller's per-decision view of reality. Nil hooks fail
// closed (NodeExists, SessionEnabled, SessionOwned) or are ignored (Ambiguous).
type LiveView struct {
	// OwnSession is the session whose global interface_node designates its
	// own declaration (the daemon's session, or the session a CLI send runs in).
	OwnSession string
	// NodeExists reports whether the exact "session:node" address is a live,
	// discovered node. The caller's map is keyed uniquely, so a present key is
	// a single node.
	NodeExists func(address string) bool
	// Ambiguous optionally reports a discovery collision for the address.
	Ambiguous func(address string) bool
	// SessionEnabled reports whether the daemon currently has the session enabled.
	SessionEnabled func(session string) bool
	// SessionOwned reports whether this context owns the session now.
	SessionOwned func(session string) bool
}

// InterfaceEdgeDecision is the outcome of one evaluation.
type InterfaceEdgeDecision struct {
	Allowed bool
	Reason  InterfaceEdgeReason
}

func deny(reason InterfaceEdgeReason) InterfaceEdgeDecision {
	return InterfaceEdgeDecision{Reason: reason}
}

// EvaluateInterfaceEdge decides whether senderNode in senderSession may send
// to recipientAddress ("session:node") over a derived interface edge.
// recipientAddress must be qualified; a bare recipient is a same-session send
// and never a derived edge. The sender identity is the physical source (the
// session whose post/ directory held the message), never a claim in the
// message; callers run the foreign-sender guard first.
func EvaluateInterfaceEdge(cfg *config.Config, view LiveView, senderSession, senderNode, recipientAddress string) InterfaceEdgeDecision {
	if cfg.ConfiguredInterfaceNode() == "" {
		return deny(InterfaceEdgeNoInterfaceNode)
	}
	recipientSession, recipientNode, qualified := nodeaddr.Split(recipientAddress)
	if !qualified || recipientSession == "" || recipientNode == "" || senderSession == "" || senderNode == "" {
		return deny(InterfaceEdgeUnqualified)
	}
	if senderSession == recipientSession {
		return deny(InterfaceEdgeSameSession)
	}

	topology := BuildFromConfig(cfg)
	sender, ok, reason := topology.NodeForSession(senderSession)
	if !ok {
		return deny(sessionFailure(reason))
	}
	recipient, ok, reason := topology.NodeForSession(recipientSession)
	if !ok {
		return deny(sessionFailure(reason))
	}
	if sender.ParentSessionName != recipientSession && recipient.ParentSessionName != senderSession {
		return deny(InterfaceEdgeNotDirectRelative)
	}

	senderDeclared, ok := cfg.DeclaredInterfaceNode(senderSession, view.OwnSession)
	if !ok {
		return deny(InterfaceEdgeUndeclared)
	}
	recipientDeclared, ok := cfg.DeclaredInterfaceNode(recipientSession, view.OwnSession)
	if !ok {
		return deny(InterfaceEdgeUndeclared)
	}
	if senderNode != senderDeclared || recipientNode != recipientDeclared {
		return deny(InterfaceEdgeNotInterfaceNode)
	}
	return liveDecision(view, senderSession, senderDeclared, recipientSession, recipientDeclared)
}

func sessionFailure(reason FailureReason) InterfaceEdgeReason {
	if reason == FailureAmbiguousHierarchy {
		return InterfaceEdgeAmbiguousSession
	}
	return InterfaceEdgeUnknownSession
}

// liveDecision applies the per-decision live checks to both endpoints.
func liveDecision(view LiveView, sessionA, nodeA, sessionB, nodeB string) InterfaceEdgeDecision {
	for _, endpoint := range [][2]string{{sessionA, nodeA}, {sessionB, nodeB}} {
		address := nodeaddr.Full(endpoint[1], endpoint[0])
		if view.NodeExists == nil || !view.NodeExists(address) {
			return deny(InterfaceEdgeNodeAbsent)
		}
		if view.Ambiguous != nil && view.Ambiguous(address) {
			return deny(InterfaceEdgeNodeAmbiguous)
		}
		if view.SessionEnabled == nil || !view.SessionEnabled(endpoint[0]) {
			return deny(InterfaceEdgeSessionDisabled)
		}
		if view.SessionOwned == nil || !view.SessionOwned(endpoint[0]) {
			return deny(InterfaceEdgeSessionNotOwned)
		}
	}
	return InterfaceEdgeDecision{Allowed: true, Reason: InterfaceEdgeAllowed}
}

// InterfaceEdge is one derived edge between two declared interface nodes.
type InterfaceEdge struct {
	A string // "session:node"
	B string // "session:node"
}

// DeclaredInterfaceEdges lists every candidate edge the configuration
// declares (direct parent-child pairs whose two entries both declare a bare
// interface node), without any live check. A-B-C yields A-B and B-C, never
// A-C. The result is sorted and deterministic.
func DeclaredInterfaceEdges(cfg *config.Config, ownSession string) []InterfaceEdge {
	if cfg.ConfiguredInterfaceNode() == "" {
		return nil
	}
	topology := BuildFromConfig(cfg)
	var edges []InterfaceEdge
	for _, child := range topology.nodes {
		if child.ParentSessionName == "" {
			continue
		}
		childNode, ok, _ := topology.NodeForSession(child.SessionName)
		if !ok {
			continue
		}
		parentNode, ok, _ := topology.NodeForSession(child.ParentSessionName)
		if !ok {
			continue
		}
		childDeclared, ok := cfg.DeclaredInterfaceNode(childNode.SessionName, ownSession)
		if !ok {
			continue
		}
		parentDeclared, ok := cfg.DeclaredInterfaceNode(parentNode.SessionName, ownSession)
		if !ok {
			continue
		}
		edges = append(edges, InterfaceEdge{
			A: nodeaddr.Full(parentDeclared, parentNode.SessionName),
			B: nodeaddr.Full(childDeclared, childNode.SessionName),
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].A != edges[j].A {
			return edges[i].A < edges[j].A
		}
		return edges[i].B < edges[j].B
	})
	return edges
}

// ActiveInterfaceEdges returns the declared edges that pass the live view
// right now. It reuses the same per-endpoint checks as EvaluateInterfaceEdge.
func ActiveInterfaceEdges(cfg *config.Config, view LiveView) []InterfaceEdge {
	var active []InterfaceEdge
	for _, edge := range DeclaredInterfaceEdges(cfg, view.OwnSession) {
		sessionA, nodeA, _ := nodeaddr.Split(edge.A)
		sessionB, nodeB, _ := nodeaddr.Split(edge.B)
		if liveDecision(view, sessionA, nodeA, sessionB, nodeB).Allowed {
			active = append(active, edge)
		}
	}
	return active
}

// InterfaceNeighbors returns the "session:node" addresses that
// (session, node) may currently reach over derived interface edges, sorted.
// It is built on EvaluateInterfaceEdge so a footer or status list can never
// show an edge the send path would refuse.
func InterfaceNeighbors(cfg *config.Config, view LiveView, session, node string) []string {
	var neighbors []string
	seen := make(map[string]bool)
	for _, edge := range DeclaredInterfaceEdges(cfg, view.OwnSession) {
		for _, pair := range [][2]string{{edge.A, edge.B}, {edge.B, edge.A}} {
			fromSession, fromNode, _ := nodeaddr.Split(pair[0])
			if fromSession != session || fromNode != node {
				continue
			}
			if EvaluateInterfaceEdge(cfg, view, session, node, pair[1]).Allowed && !seen[pair[1]] {
				seen[pair[1]] = true
				neighbors = append(neighbors, pair[1])
			}
		}
	}
	sort.Strings(neighbors)
	return neighbors
}
