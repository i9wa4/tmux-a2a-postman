package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"gopkg.in/yaml.v3"
)

// WorkspaceTopology is the bounded Markdown routing input for the file-first
// command adapters. It carries no daemon context, policy, grant or runtime state.
type WorkspaceTopology struct {
	Nodes         []string
	Edges         map[string][]string
	Reviewer      string
	InterfaceNode string
	Sessions      []WorkspaceSession
}

// WorkspaceSession declares one member of an opt-in, direct parent-child
// interface route. The declaration alone never proves a live tmux binding.
type WorkspaceSession struct {
	Name          string
	Parent        string
	InterfaceNode string
}

// LoadWorkspaceTopology reads one explicit postman.md, never a warning-and-skip
// overlay chain. Missing, unreadable or malformed input is unknown, not verified
// reviewer absence. Skill catalogs/templates are not opened or rendered here.
// Symlinks remain supported as configuration inputs (including Nix-managed
// files); runtime identity and authorization records use separate strict storage.
func LoadWorkspaceTopology(ctx context.Context, path string) (WorkspaceTopology, error) {
	var result WorkspaceTopology
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !filepath.IsAbs(path) {
		return result, errors.New("topology path must be absolute")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return result, errors.New("topology must be a bounded regular Markdown file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return result, errors.New("cannot read bounded topology")
	}
	after, err := f.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return result, errors.New("topology changed while reading")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	content := string(raw)
	if err := validateWorkspaceFrontmatter(content); err != nil {
		return result, err
	}
	fm, _, _, _, err := parsePostmanFrontmatter(content)
	if err != nil {
		return result, err
	}
	if _, exists := fm["command_approver_node"]; exists {
		return result, errors.New("legacy frontmatter command approver is not authoritative")
	}
	order, sections := extractH2Sections(stripFrontmatter(content))
	seenSections := map[string]bool{}
	for _, line := range strings.Split(stripFrontmatter(content), "\n") {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if len(line) > 64*1024 {
			return result, errors.New("topology line exceeds bound")
		}
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		key := extractBacktickName(line[3:])
		if key == "" {
			continue
		}
		if seenSections[key] {
			return result, fmt.Errorf("duplicate topology section %q", key)
		}
		seenSections[key] = true
	}
	nodes := map[string]bool{}
	for _, node := range order {
		nodes[node] = true
	}
	edgesBody := sections["edges"]
	block, err := workspaceMermaidBlock(edgesBody)
	if err != nil {
		return result, err
	}
	declared, err := validateWorkspaceMermaid(block)
	if err != nil {
		return result, err
	}
	for _, node := range declared {
		nodes[node] = true
	}
	edges := parseMermaidEdges(block)
	if len(edges) > 1024 {
		return result, errors.New("topology exceeds edge bound")
	}
	adjacency, err := ParseEdges(edges)
	if err != nil {
		return result, err
	}
	for node := range adjacency {
		nodes[node] = true
	}
	reviewer, _, err := parseMermaidCommandApproverNode(block)
	if err != nil {
		return result, err
	}
	iface, _, err := parseMermaidInterfaceNode(block)
	if err != nil {
		return result, err
	}
	if value, ok := fm["interface_node"]; ok {
		iface = strings.TrimSpace(value)
	}
	// Use the same native role grammar as config validation. A class declaration
	// alone cannot conjure an otherwise unknown node.
	for node := range nodes {
		if !validWorkspaceNode(node) {
			return result, fmt.Errorf("invalid topology node %q", node)
		}
		result.Nodes = append(result.Nodes, node)
	}
	if len(nodes) > 256 {
		return result, errors.New("topology exceeds node bound")
	}
	if reviewer != "" && (!nodes[reviewer] || !validWorkspaceNode(reviewer)) {
		return result, errors.New("configured reviewer is not a topology node")
	}
	if iface != "" && (!nodes[iface] || !validWorkspaceNode(iface)) {
		return result, errors.New("configured interface is not a topology node")
	}
	sessions, err := parseWorkspaceSessions(content, nodes)
	if err != nil {
		return result, err
	}
	if len(sessions) > 0 && iface == "" {
		return result, errors.New("workspace tree requires the local interface_node declaration")
	}
	sort.Strings(result.Nodes)
	result.Edges, result.Reviewer, result.InterfaceNode, result.Sessions = adjacency, reviewer, iface, sessions
	return result, nil
}

func validWorkspaceNode(name string) bool {
	return binding.ValidateNodeName(name) && name != "postman"
}

// Frontmatter is parsed without opening catalogs. Reject duplicate normalized
// keys, aliases/merges, and legacy approval keys rather than silently dropping
// an ambiguous authorization input. Existing YAML dependency is reused.
func validateWorkspaceFrontmatter(content string) error {
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil || !ok {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")), &doc); err != nil {
		return err
	}
	count := 0
	var visit func(*yaml.Node, int) error
	visit = func(node *yaml.Node, depth int) error {
		count++
		if depth > 32 || count > 4096 || node.Kind == yaml.AliasNode {
			return errors.New("unsupported or unbounded topology YAML")
		}
		if node.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i+1 < len(node.Content); i += 2 {
				key := node.Content[i]
				normalized := strings.ToLower(strings.TrimSpace(key.Value))
				if key.Kind != yaml.ScalarNode || normalized == "<<" || normalized == "command_approver_node" || seen[normalized] {
					return errors.New("ambiguous or legacy topology frontmatter key")
				}
				seen[normalized] = true
			}
		}
		for _, child := range node.Content {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(&doc, 0)
}

var (
	workspaceNodeToken  = regexp.MustCompile(`^([a-zA-Z0-9][a-zA-Z0-9-]{0,63})(\[[^\[\]\r\n]*\]|\([^()\r\n]*\)|\{[^{}\r\n]*\})?(:::([a-zA-Z_][a-zA-Z0-9_]*)(,[a-zA-Z_][a-zA-Z0-9_]*)*)?$`)
	workspaceClassToken = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(,[a-zA-Z_][a-zA-Z0-9_]*)*$`)
)

// This authorization reader accepts a strict Mermaid subset: graph/flowchart
// header, --- edges, simple node labels/classes, class and classDef. Other
// presentation syntax is an error; no skipped statement can imply absence.
func validateWorkspaceMermaid(block string) ([]string, error) {
	var nodes []string
	statements := mermaidStatements(block)
	if len(statements) > 2048 {
		return nil, errors.New("topology statement bound exceeded")
	}
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		fields := strings.Fields(statement)
		switch fields[0] {
		case "graph", "flowchart":
			if len(fields) != 2 || !strings.Contains("|LR|RL|TB|TD|BT|", "|"+fields[1]+"|") {
				return nil, errors.New("invalid topology graph header")
			}
			continue
		case "class":
			if len(fields) != 3 || !workspaceClassToken.MatchString(fields[2]) {
				return nil, errors.New("invalid topology class")
			}
			for _, node := range strings.Split(fields[1], ",") {
				if !validWorkspaceNode(node) {
					return nil, errors.New("invalid topology class node")
				}
			}
			continue
		case "classDef":
			if len(fields) < 3 || !workspaceClassToken.MatchString(fields[1]) {
				return nil, errors.New("invalid topology class definition")
			}
			continue
		}
		if containsUnsupportedMermaidEdgeOperator(statement) {
			return nil, errors.New("unsupported topology edge operator")
		}
		for _, part := range strings.Split(statement, "---") {
			match := workspaceNodeToken.FindStringSubmatch(strings.TrimSpace(part))
			if match == nil || !validWorkspaceNode(match[1]) || strings.Contains(match[2], ":::") {
				return nil, errors.New("unsupported or malformed topology node statement")
			}
			nodes = append(nodes, match[1])
		}
	}
	return nodes, nil
}

// Require a complete unique fence rather than treating an unclosed Mermaid
// declaration as an empty topology. Ordinary prose outside fences is harmless.
func workspaceMermaidBlock(content string) (string, error) {
	var block strings.Builder
	inFence, mermaid, found := false, false, false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inFence {
				inFence = true
				mermaid = trimmed == "```mermaid"
				if mermaid {
					if found {
						return "", errors.New("multiple topology Mermaid blocks")
					}
					found = true
				}
			} else {
				if trimmed != "```" {
					return "", errors.New("malformed topology fence")
				}
				inFence, mermaid = false, false
			}
			continue
		}
		if mermaid {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	if inFence {
		return "", errors.New("unclosed topology fence")
	}
	if strings.TrimSpace(content) != "" && !found {
		return "", errors.New("edges section lacks a complete Mermaid block")
	}
	return block.String(), nil
}
