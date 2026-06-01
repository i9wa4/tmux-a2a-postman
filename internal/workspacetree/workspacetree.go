package workspacetree

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

const (
	ParentAliasPrefix = "@parent/"
	ChildAliasPrefix  = "@child/"
)

type FailureReason string

const (
	FailureNone              FailureReason = ""
	FailureNotAlias          FailureReason = "not_alias"
	FailureInvalidAlias      FailureReason = "invalid_alias"
	FailureUnknownSourceRoot FailureReason = "unknown_source_root"
	FailureAmbiguousRoot     FailureReason = "ambiguous_root"
	FailureNoParent          FailureReason = "no_parent"
	FailureNoChild           FailureReason = "no_child"
	FailureAmbiguousChild    FailureReason = "ambiguous_child"
	FailureUnknownNode       FailureReason = "unknown_node"
)

type Registration struct {
	SessionName string
	Label       string
	Root        string
	RootID      string
}

type Root struct {
	SessionName       string
	Label             string
	RootID            string
	path              string
	duplicateRootPath bool
	ambiguousSession  bool
}

type Diagnostic struct {
	Code         string
	RootID       string
	RootIDs      []string
	SessionName  string
	SessionNames []string
	Labels       []string
	Message      string
}

type Topology struct {
	roots       []Root
	bySession   map[string][]int
	diagnostics []Diagnostic
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
		bySession: make(map[string][]int),
	}
	byPath := make(map[string][]int)

	for _, registration := range registrations {
		sessionName := strings.TrimSpace(registration.SessionName)
		rootPath := strings.TrimSpace(registration.Root)
		if sessionName == "" || rootPath == "" {
			continue
		}
		normalized, err := normalizeRootPath(rootPath)
		if err != nil {
			topology.diagnostics = append(topology.diagnostics, Diagnostic{
				Code:        "invalid_root",
				SessionName: sessionName,
				Message:     "workspace root path could not be normalized",
			})
			continue
		}
		label := strings.TrimSpace(registration.Label)
		if label == "" {
			label = sessionName
		}
		rootID := strings.TrimSpace(registration.RootID)
		if rootID == "" {
			rootID = derivedRootID(normalized)
		}
		idx := len(topology.roots)
		topology.roots = append(topology.roots, Root{
			SessionName: sessionName,
			Label:       label,
			RootID:      rootID,
			path:        normalized,
		})
		topology.bySession[sessionName] = append(topology.bySession[sessionName], idx)
		byPath[normalized] = append(byPath[normalized], idx)
	}

	for _, idxs := range byPath {
		if len(idxs) < 2 {
			continue
		}
		var rootIDs, sessionNames, labels []string
		for _, idx := range idxs {
			topology.roots[idx].duplicateRootPath = true
			rootIDs = append(rootIDs, topology.roots[idx].RootID)
			sessionNames = append(sessionNames, topology.roots[idx].SessionName)
			labels = append(labels, topology.roots[idx].Label)
		}
		topology.diagnostics = append(topology.diagnostics, Diagnostic{
			Code:         "duplicate_root",
			RootID:       topology.roots[idxs[0]].RootID,
			RootIDs:      sortedUnique(rootIDs),
			SessionNames: sortedUnique(sessionNames),
			Labels:       sortedUnique(labels),
			Message:      "multiple sessions registered the same workspace root",
		})
	}

	for sessionName, idxs := range topology.bySession {
		if len(idxs) < 2 {
			continue
		}
		var rootIDs, labels []string
		for _, idx := range idxs {
			topology.roots[idx].ambiguousSession = true
			rootIDs = append(rootIDs, topology.roots[idx].RootID)
			labels = append(labels, topology.roots[idx].Label)
		}
		topology.diagnostics = append(topology.diagnostics, Diagnostic{
			Code:        "ambiguous_session_roots",
			RootIDs:     sortedUnique(rootIDs),
			SessionName: sessionName,
			Labels:      sortedUnique(labels),
			Message:     "session registered more than one workspace root",
		})
	}

	sort.Slice(topology.diagnostics, func(i, j int) bool {
		if topology.diagnostics[i].Code != topology.diagnostics[j].Code {
			return topology.diagnostics[i].Code < topology.diagnostics[j].Code
		}
		return topology.diagnostics[i].RootID < topology.diagnostics[j].RootID
	})
	return topology
}

func (t Topology) Diagnostics() []Diagnostic {
	result := make([]Diagnostic, len(t.diagnostics))
	copy(result, t.diagnostics)
	return result
}

func (t Topology) RootForSession(sessionName string) (Root, bool, FailureReason) {
	idxs := t.bySession[sessionName]
	if len(idxs) == 0 {
		return Root{}, false, FailureUnknownSourceRoot
	}
	if len(idxs) > 1 {
		return Root{}, false, FailureAmbiguousRoot
	}
	root := t.roots[idxs[0]]
	if root.duplicateRootPath || root.ambiguousSession {
		return Root{}, false, FailureAmbiguousRoot
	}
	return root, true, FailureNone
}

func (t Topology) NearestParent(sessionName string) (Root, bool, FailureReason) {
	source, ok, reason := t.RootForSession(sessionName)
	if !ok {
		return Root{}, false, reason
	}

	var candidates []Root
	bestDepth := -1
	for _, candidate := range t.roots {
		if candidate.SessionName == source.SessionName {
			continue
		}
		if !isStrictDescendant(source.path, candidate.path) {
			continue
		}
		depth := pathDepth(candidate.path)
		switch {
		case depth > bestDepth:
			candidates = []Root{candidate}
			bestDepth = depth
		case depth == bestDepth:
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return Root{}, false, FailureNoParent
	}
	if rootsAmbiguous(candidates) {
		return Root{}, false, FailureAmbiguousRoot
	}
	return candidates[0], true, FailureNone
}

func (t Topology) NearestChildren(sessionName string) ([]Root, FailureReason) {
	source, ok, reason := t.RootForSession(sessionName)
	if !ok {
		return nil, reason
	}

	descendants := make([]Root, 0)
	for _, candidate := range t.roots {
		if candidate.SessionName == source.SessionName {
			continue
		}
		if isStrictDescendant(candidate.path, source.path) {
			descendants = append(descendants, candidate)
		}
	}

	children := make([]Root, 0, len(descendants))
	for _, candidate := range descendants {
		nearest := true
		for _, other := range descendants {
			if other.SessionName == candidate.SessionName && other.path == candidate.path {
				continue
			}
			if isStrictDescendant(candidate.path, other.path) {
				nearest = false
				break
			}
		}
		if nearest {
			children = append(children, candidate)
		}
	}
	sortRoots(children)
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

	var target Root
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
			return AliasResolution{Input: alias, AliasKind: parsed.kind, Selector: parsed.selector, NodeName: parsed.nodeName, FailureReason: FailureNoChild}
		}
		if rootsAmbiguous(matches) || len(matches) > 1 {
			return AliasResolution{Input: alias, AliasKind: parsed.kind, Selector: parsed.selector, NodeName: parsed.nodeName, FailureReason: FailureAmbiguousChild}
		}
		target = matches[0]
	default:
		return AliasResolution{Input: alias, FailureReason: FailureInvalidAlias}
	}

	address := nodeaddr.Full(parsed.nodeName, target.SessionName)
	if nodeExists != nil && !nodeExists(address) {
		return AliasResolution{
			Input:         alias,
			Address:       address,
			SessionName:   target.SessionName,
			NodeName:      parsed.nodeName,
			AliasKind:     parsed.kind,
			Selector:      parsed.selector,
			FailureReason: FailureUnknownNode,
		}
	}
	return AliasResolution{
		Input:       alias,
		Address:     address,
		SessionName: target.SessionName,
		NodeName:    parsed.nodeName,
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

type parsedAlias struct {
	kind     string
	selector string
	nodeName string
}

func parseAlias(alias string) (parsedAlias, error) {
	switch {
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
			if err := validateAliasNode(parts[0]); err != nil {
				return parsedAlias{}, err
			}
			return parsedAlias{kind: "child", nodeName: parts[0]}, nil
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

func selectChildren(children []Root, selector string) []Root {
	if selector == "" {
		if len(children) == 1 {
			return children
		}
		return children
	}
	var matches []Root
	for _, child := range children {
		if child.Label == selector || child.SessionName == selector || child.RootID == selector {
			matches = append(matches, child)
		}
	}
	return matches
}

func rootsAmbiguous(roots []Root) bool {
	for _, root := range roots {
		if root.duplicateRootPath || root.ambiguousSession {
			return true
		}
	}
	return len(roots) > 1
}

func normalizeRootPath(root string) (string, error) {
	cleaned := filepath.Clean(root)
	if !filepath.IsAbs(cleaned) {
		abs, err := filepath.Abs(cleaned)
		if err != nil {
			return "", err
		}
		cleaned = abs
	}
	return filepath.Clean(cleaned), nil
}

func derivedRootID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return fmt.Sprintf("root-%x", sum[:6])
}

func isStrictDescendant(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathDepth(path string) int {
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) {
		return 0
	}
	return strings.Count(cleaned, string(filepath.Separator))
}

func sortRoots(roots []Root) {
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].Label != roots[j].Label {
			return roots[i].Label < roots[j].Label
		}
		if roots[i].SessionName != roots[j].SessionName {
			return roots[i].SessionName < roots[j].SessionName
		}
		return roots[i].RootID < roots[j].RootID
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
