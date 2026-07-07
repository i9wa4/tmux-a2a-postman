package workspacetree

import (
	"fmt"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

const (
	ParentAlias       = "@parent"
	ChildAliasPrefix  = "@child/"
	ParentAliasPrefix = ParentAlias + "/"
)

type FailureReason string

const (
	FailureNone                 FailureReason = ""
	FailureNotAlias             FailureReason = "not_alias"
	FailureInvalidAlias         FailureReason = "invalid_alias"
	FailureUnknownSourceSession FailureReason = "unknown_source_session"
	FailureAmbiguousHierarchy   FailureReason = "ambiguous_hierarchy"
	FailureUnknownParent        FailureReason = "unknown_parent"
	FailureNoParent             FailureReason = "no_parent"
	FailureNoChild              FailureReason = "no_child"
	FailureAmbiguousChild       FailureReason = "ambiguous_child"
	FailureUnknownNode          FailureReason = "unknown_node"
)

type Registration struct {
	SessionName       string
	ID                string
	Label             string
	ParentSessionName string
	Representative    string
	Order             int
}

type Node struct {
	SessionName       string
	ID                string
	Label             string
	ParentSessionName string
	Representative    string
	Order             int
}

type Diagnostic struct {
	Code              string
	ID                string
	IDs               []string
	SessionName       string
	SessionNames      []string
	ParentSessionName string
	Labels            []string
	Message           string
}

type Topology struct {
	nodes            []Node
	bySession        map[string][]int
	childrenByParent map[string][]int
	diagnostics      []Diagnostic
}

type AliasResolution struct {
	Input         string
	Address       string
	SessionName   string
	NodeName      string
	AliasKind     string
	Selector      string
	Found         bool
	FailureReason FailureReason
}

func Build(registrations []Registration) Topology {
	topology := Topology{
		bySession:        make(map[string][]int),
		childrenByParent: make(map[string][]int),
	}

	for _, registration := range registrations {
		sessionName := strings.TrimSpace(registration.SessionName)
		if sessionName == "" {
			continue
		}
		label := strings.TrimSpace(registration.Label)
		if label == "" {
			label = sessionName
		}
		id := strings.TrimSpace(registration.ID)
		if id == "" {
			id = sessionName
		}
		parentSessionName := strings.TrimSpace(registration.ParentSessionName)

		idx := len(topology.nodes)
		topology.nodes = append(topology.nodes, Node{
			SessionName:       sessionName,
			ID:                id,
			Label:             label,
			ParentSessionName: parentSessionName,
			Representative:    strings.TrimSpace(registration.Representative),
			Order:             registration.Order,
		})
		topology.bySession[sessionName] = append(topology.bySession[sessionName], idx)
		if parentSessionName != "" {
			topology.childrenByParent[parentSessionName] = append(topology.childrenByParent[parentSessionName], idx)
		}
	}

	topology.recordDuplicateSessions()
	topology.recordUnknownParents()
	sortDiagnostics(topology.diagnostics)
	return topology
}

func (t *Topology) recordDuplicateSessions() {
	for sessionName, idxs := range t.bySession {
		if len(idxs) < 2 {
			continue
		}
		var ids, labels []string
		for _, idx := range idxs {
			ids = append(ids, t.nodes[idx].ID)
			labels = append(labels, t.nodes[idx].Label)
		}
		t.diagnostics = append(t.diagnostics, Diagnostic{
			Code:        "duplicate_session",
			IDs:         sortedUnique(ids),
			SessionName: sessionName,
			Labels:      sortedUnique(labels),
			Message:     "session appears more than once in workspace tree",
		})
	}
}

func (t *Topology) recordUnknownParents() {
	for _, node := range t.nodes {
		if node.ParentSessionName == "" {
			continue
		}
		if len(t.bySession[node.ParentSessionName]) == 0 {
			t.diagnostics = append(t.diagnostics, Diagnostic{
				Code:              "unknown_parent",
				ID:                node.ID,
				SessionName:       node.SessionName,
				ParentSessionName: node.ParentSessionName,
				Message:           "workspace tree parent session is not configured",
			})
		}
	}
}

func (t Topology) Diagnostics() []Diagnostic {
	result := make([]Diagnostic, len(t.diagnostics))
	copy(result, t.diagnostics)
	return result
}

func (t Topology) NodeForSession(sessionName string) (Node, bool, FailureReason) {
	idxs := t.bySession[sessionName]
	if len(idxs) == 0 {
		return Node{}, false, FailureUnknownSourceSession
	}
	if len(idxs) > 1 {
		return Node{}, false, FailureAmbiguousHierarchy
	}
	return t.nodes[idxs[0]], true, FailureNone
}

func (t Topology) NearestParent(sessionName string) (Node, bool, FailureReason) {
	source, ok, reason := t.NodeForSession(sessionName)
	if !ok {
		return Node{}, false, reason
	}
	if source.ParentSessionName == "" {
		return Node{}, false, FailureNoParent
	}
	parent, ok, reason := t.NodeForSession(source.ParentSessionName)
	if !ok {
		if reason == FailureUnknownSourceSession {
			return Node{}, false, FailureUnknownParent
		}
		return Node{}, false, reason
	}
	return parent, true, FailureNone
}

func (t Topology) NearestChildren(sessionName string) ([]Node, FailureReason) {
	if _, ok, reason := t.NodeForSession(sessionName); !ok {
		return nil, reason
	}

	childIdxs := append([]int(nil), t.childrenByParent[sessionName]...)
	children := make([]Node, 0, len(childIdxs))
	for _, idx := range childIdxs {
		child := t.nodes[idx]
		if _, ok, reason := t.NodeForSession(child.SessionName); !ok {
			return nil, reason
		}
		children = append(children, child)
	}
	sortNodes(children)
	return children, FailureNone
}

func (t Topology) ResolveAlias(alias, sourceSessionName string, nodeExists func(string) bool) AliasResolution {
	parsed, err := parseAlias(alias)
	if err != nil {
		if !IsAlias(alias) {
			return AliasResolution{Input: alias, FailureReason: FailureNotAlias}
		}
		return AliasResolution{Input: alias, FailureReason: FailureInvalidAlias}
	}

	var target Node
	switch parsed.kind {
	case "parent":
		parent, ok, reason := t.NearestParent(sourceSessionName)
		if !ok {
			return AliasResolution{Input: alias, AliasKind: parsed.kind, NodeName: parsed.nodeName, FailureReason: reason}
		}
		target = parent
	case "child":
		children, reason := t.NearestChildren(sourceSessionName)
		if reason != FailureNone {
			return AliasResolution{Input: alias, AliasKind: parsed.kind, Selector: parsed.selector, NodeName: parsed.nodeName, FailureReason: reason}
		}
		matches := selectChildren(children, parsed.selector)
		if len(matches) == 0 {
			switch {
			case parsed.nodeName == "" && len(children) == 1:
				parsed.nodeName = parsed.selector
				parsed.selector = ""
				matches = children
			case parsed.nodeName == "" && len(children) > 1:
				return AliasResolution{Input: alias, AliasKind: parsed.kind, Selector: parsed.selector, NodeName: parsed.nodeName, FailureReason: FailureAmbiguousChild}
			default:
				return AliasResolution{Input: alias, AliasKind: parsed.kind, Selector: parsed.selector, NodeName: parsed.nodeName, FailureReason: FailureNoChild}
			}
		}
		if len(matches) > 1 {
			return AliasResolution{Input: alias, AliasKind: parsed.kind, Selector: parsed.selector, NodeName: parsed.nodeName, FailureReason: FailureAmbiguousChild}
		}
		target = matches[0]
	default:
		return AliasResolution{Input: alias, FailureReason: FailureInvalidAlias}
	}

	resolvedNodeName := parsed.nodeName
	address := nodeaddr.Full(resolvedNodeName, target.SessionName)
	if parsed.nodeName == "" {
		if target.Representative == "" {
			return AliasResolution{
				Input:         alias,
				SessionName:   target.SessionName,
				AliasKind:     parsed.kind,
				Selector:      parsed.selector,
				FailureReason: FailureUnknownNode,
			}
		}
		resolvedNodeName = target.Representative
		address = nodeaddr.Full(resolvedNodeName, target.SessionName)
	}
	if nodeExists != nil && !nodeExists(address) {
		return AliasResolution{
			Input:         alias,
			Address:       address,
			SessionName:   target.SessionName,
			NodeName:      resolvedNodeName,
			AliasKind:     parsed.kind,
			Selector:      parsed.selector,
			FailureReason: FailureUnknownNode,
		}
	}
	return AliasResolution{
		Input:       alias,
		Address:     address,
		SessionName: target.SessionName,
		NodeName:    resolvedNodeName,
		AliasKind:   parsed.kind,
		Selector:    parsed.selector,
		Found:       true,
	}
}

func IsAlias(address string) bool {
	return strings.HasPrefix(address, "@")
}

func ValidateAliasSyntax(alias string) error {
	_, err := parseAlias(alias)
	return err
}

func (t Topology) RelationshipAlias(viewpointAddress, targetAddress string) (string, bool) {
	viewSessionName, _, viewHasSession := nodeaddr.Split(viewpointAddress)
	targetSessionName, targetNodeName, targetHasSession := nodeaddr.Split(targetAddress)
	if !viewHasSession || !targetHasSession {
		return "", false
	}
	view, ok, _ := t.NodeForSession(viewSessionName)
	if !ok {
		return "", false
	}
	target, ok, _ := t.NodeForSession(targetSessionName)
	if !ok || target.Representative == "" || targetNodeName != target.Representative {
		return "", false
	}
	if view.ParentSessionName == target.SessionName {
		return ParentAlias, true
	}
	if target.ParentSessionName == view.SessionName {
		return ChildAliasPrefix + target.Label, true
	}
	return "", false
}

type parsedAlias struct {
	kind     string
	selector string
	nodeName string
}

func parseAlias(alias string) (parsedAlias, error) {
	switch {
	case alias == ParentAlias:
		return parsedAlias{kind: "parent"}, nil
	case strings.HasPrefix(alias, ParentAliasPrefix):
		nodeName := strings.TrimPrefix(alias, ParentAliasPrefix)
		if err := validateAliasNode(nodeName); err != nil {
			return parsedAlias{}, err
		}
		return parsedAlias{kind: "parent", nodeName: nodeName}, nil
	case strings.HasPrefix(alias, ChildAliasPrefix):
		rest := strings.TrimPrefix(alias, ChildAliasPrefix)
		parts := strings.Split(rest, "/")
		switch len(parts) {
		case 1:
			if strings.TrimSpace(parts[0]) == "" {
				return parsedAlias{}, fmt.Errorf("child selector or node is required")
			}
			if err := validateAliasNode(parts[0]); err != nil {
				return parsedAlias{}, err
			}
			return parsedAlias{kind: "child", selector: parts[0]}, nil
		case 2:
			if strings.TrimSpace(parts[0]) == "" {
				return parsedAlias{}, fmt.Errorf("child selector is required")
			}
			if err := validateAliasNode(parts[1]); err != nil {
				return parsedAlias{}, err
			}
			return parsedAlias{kind: "child", selector: parts[0], nodeName: parts[1]}, nil
		default:
			return parsedAlias{}, fmt.Errorf("invalid child alias")
		}
	default:
		return parsedAlias{}, fmt.Errorf("workspace tree aliases must use %q or %q", ParentAliasPrefix, ChildAliasPrefix)
	}
}

func validateAliasNode(nodeName string) error {
	if !binding.ValidateNodeName(nodeName) {
		return fmt.Errorf("invalid alias node %q (must match %s)", nodeName, binding.NodeNamePattern)
	}
	return nil
}

func selectChildren(children []Node, selector string) []Node {
	if selector == "" {
		return children
	}
	var matches []Node
	for _, child := range children {
		if child.Label == selector || child.SessionName == selector || child.ID == selector {
			matches = append(matches, child)
		}
	}
	return matches
}

func sortNodes(nodes []Node) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Order != nodes[j].Order {
			return nodes[i].Order < nodes[j].Order
		}
		if nodes[i].Label != nodes[j].Label {
			return nodes[i].Label < nodes[j].Label
		}
		if nodes[i].SessionName != nodes[j].SessionName {
			return nodes[i].SessionName < nodes[j].SessionName
		}
		return nodes[i].ID < nodes[j].ID
	})
}

func sortedUnique(values []string) []string {
	seen := make(map[string]bool, len(values))
	var result []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		if diagnostics[i].SessionName != diagnostics[j].SessionName {
			return diagnostics[i].SessionName < diagnostics[j].SessionName
		}
		return diagnostics[i].ID < diagnostics[j].ID
	})
}
