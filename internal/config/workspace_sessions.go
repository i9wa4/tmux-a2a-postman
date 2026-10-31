package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseWorkspaceSessions reads only the explicit file-first tree declaration.
// It does not consult daemon TOML, discover sessions, or infer a parent from a
// session name. Native liveness remains a separate requirement at send time.
func parseWorkspaceSessions(content string, nodes map[string]bool) ([]WorkspaceSession, error) {
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil || !ok {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")), &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	root := document.Content[0]
	var tree *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if strings.EqualFold(strings.TrimSpace(root.Content[i].Value), "workspace_tree") {
			tree = root.Content[i+1]
			break
		}
	}
	if tree == nil {
		return nil, nil
	}
	if tree.Kind != yaml.SequenceNode || len(tree.Content) == 0 || len(tree.Content) > 256 {
		return nil, errors.New("workspace_tree must be a nonempty bounded list")
	}
	sessions := make([]WorkspaceSession, 0, len(tree.Content))
	byName := make(map[string]WorkspaceSession, len(tree.Content))
	for _, item := range tree.Content {
		if item.Kind != yaml.MappingNode {
			return nil, errors.New("workspace_tree entries must be mappings")
		}
		var session WorkspaceSession
		for i := 0; i+1 < len(item.Content); i += 2 {
			key, value := item.Content[i], item.Content[i+1]
			if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return nil, errors.New("workspace_tree fields must be strings")
			}
			switch strings.ToLower(strings.TrimSpace(key.Value)) {
			case "session_name":
				session.Name = value.Value
			case "parent_session_name":
				session.Parent = value.Value
			case "interface_node":
				session.InterfaceNode = value.Value
			default:
				return nil, fmt.Errorf("unsupported workspace_tree field %q", key.Value)
			}
		}
		if !validWorkspaceSessionName(session.Name) || session.Parent != "" && !validWorkspaceSessionName(session.Parent) ||
			!nodes[session.InterfaceNode] || !validWorkspaceNode(session.InterfaceNode) {
			return nil, errors.New("invalid workspace_tree session or interface node")
		}
		if _, duplicate := byName[session.Name]; duplicate {
			return nil, fmt.Errorf("duplicate workspace_tree session %q", session.Name)
		}
		byName[session.Name] = session
		sessions = append(sessions, session)
	}
	for _, session := range sessions {
		if session.Parent != "" {
			if session.Parent == session.Name {
				return nil, errors.New("workspace_tree self-parent")
			}
			if _, ok := byName[session.Parent]; !ok {
				return nil, fmt.Errorf("workspace_tree parent %q is undeclared", session.Parent)
			}
		}
		seen := map[string]bool{}
		for next := session.Name; next != ""; next = byName[next].Parent {
			if seen[next] {
				return nil, errors.New("workspace_tree contains a cycle")
			}
			seen[next] = true
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	return sessions, nil
}

func validWorkspaceSessionName(name string) bool {
	clean, err := ValidateSessionName(name)
	return err == nil && clean == name && !strings.ContainsAny(name, ": \t\r\n")
}

// InterfaceRoute checks the declared direct relationship and exact interface
// roles. A caller must also verify both native sessions and pane bindings
// immediately before delivery; a declaration is never a live authorization.
func (t WorkspaceTopology) InterfaceRoute(senderSession, senderRole, recipientAddress string) (string, string, error) {
	if strings.Count(recipientAddress, ":") != 1 {
		return "", "", errors.New("cross-session recipient must be session:node")
	}
	recipientSession, recipientRole, _ := strings.Cut(recipientAddress, ":")
	if !validWorkspaceSessionName(senderSession) || !validWorkspaceSessionName(recipientSession) ||
		!validWorkspaceNode(senderRole) || !validWorkspaceNode(recipientRole) || senderSession == recipientSession {
		return "", "", errors.New("invalid cross-session interface address")
	}
	var sender, recipient *WorkspaceSession
	for i := range t.Sessions {
		if t.Sessions[i].Name == senderSession {
			sender = &t.Sessions[i]
		}
		if t.Sessions[i].Name == recipientSession {
			recipient = &t.Sessions[i]
		}
	}
	if sender == nil || recipient == nil || sender.InterfaceNode != t.InterfaceNode ||
		sender.InterfaceNode != senderRole || recipient.InterfaceNode != recipientRole ||
		(sender.Parent != recipient.Name && recipient.Parent != sender.Name) {
		return "", "", errors.New("no declared direct parent-child interface edge")
	}
	return recipientSession, recipientRole, nil
}
