package todo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

type Summary struct {
	Node    string `json:"node"`
	Checked int    `json:"checked"`
	Total   int    `json:"total"`
	Exists  bool   `json:"exists"`
	Invalid bool   `json:"invalid"`
}

func ParseDocument(content string) Summary {
	var summary Summary
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "- [ ]" || strings.HasPrefix(trimmed, "- [ ] "):
			summary.Total++
		case trimmed == "- [x]" || strings.HasPrefix(trimmed, "- [x] "):
			summary.Checked++
			summary.Total++
		case trimmed == "- [X]" || strings.HasPrefix(trimmed, "- [X] "):
			summary.Checked++
			summary.Total++
		case strings.HasPrefix(trimmed, "- ["):
			summary.Invalid = true
		}
	}
	return summary
}

func (summary Summary) Token() string {
	switch {
	case summary.Invalid:
		return "[!]"
	case summary.Total == 0:
		return "[·]"
	case summary.Checked == 0:
		return "[ ]"
	case summary.Checked == summary.Total:
		return "[x]"
	default:
		return "[-]"
	}
}

func (summary Summary) State() string {
	switch summary.Token() {
	case "[!]":
		return "invalid"
	case "[·]":
		return "empty"
	case "[ ]":
		return "todo"
	case "[x]":
		return "done"
	default:
		return "partial"
	}
}

func Dir(sessionDir string) string {
	return filepath.Join(sessionDir, "todo")
}

func Path(sessionDir, node string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	return filepath.Join(Dir(sessionDir), node+".md"), nil
}

func ReadFile(sessionDir, node string) (string, error) {
	path, err := Path(sessionDir, node)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func WriteOwnerFile(sessionDir, ownerNode, targetNode, body string) error {
	if ownerNode != targetNode {
		return fmt.Errorf("node %q may only write todo/%s.md", ownerNode, ownerNode)
	}
	if err := validateNodeName(ownerNode); err != nil {
		return err
	}
	path, err := Path(sessionDir, targetNode)
	if err != nil {
		return err
	}

	todoDir := Dir(sessionDir)
	if err := os.MkdirAll(todoDir, 0o700); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(todoDir, targetNode+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmpFile.WriteString(body); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func Summaries(sessionDir string, nodes []string) ([]Summary, error) {
	var summaries []Summary
	seen := make(map[string]bool)
	for _, node := range nodes {
		if node == "" || seen[node] {
			continue
		}
		summary, err := summaryForNode(sessionDir, node)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
		seen[node] = true
	}

	entries, err := os.ReadDir(Dir(sessionDir))
	if err != nil {
		if os.IsNotExist(err) {
			return summaries, nil
		}
		return nil, err
	}
	var extras []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		node := strings.TrimSuffix(entry.Name(), ".md")
		if node == "" || seen[node] {
			continue
		}
		extras = append(extras, node)
	}
	sort.Strings(extras)
	for _, node := range extras {
		summary, err := summaryForNode(sessionDir, node)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func summaryForNode(sessionDir, node string) (Summary, error) {
	path, err := Path(sessionDir, node)
	if err != nil {
		return Summary{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Summary{Node: node}, nil
		}
		return Summary{}, err
	}
	summary := ParseDocument(string(data))
	summary.Node = node
	summary.Exists = true
	return summary, nil
}

func validateNodeName(node string) error {
	if binding.ValidateNodeName(node) {
		return nil
	}
	return fmt.Errorf("invalid todo node name %q (must match %s)", node, binding.NodeNamePattern)
}
