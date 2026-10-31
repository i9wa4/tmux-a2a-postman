package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func workspaceFrontmatter(content string) (map[string]string, error) {
	result := map[string]string{}
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil || !ok {
		return result, err
	}
	var values map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")), &values); err != nil {
		return nil, err
	}
	if v, ok := values["interface_node"]; ok {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return nil, fmt.Errorf("interface_node must be a string")
		}
		result["interface_node"] = v.Value
	}
	return result, nil
}

func ParseEdges(edges []string) (map[string][]string, error) {
	result := map[string][]string{}
	for _, edge := range edges {
		nodes := strings.Split(edge, "---")
		if len(nodes) < 2 {
			return nil, fmt.Errorf("invalid edge %q", edge)
		}
		for i := range nodes {
			nodes[i] = strings.TrimSpace(nodes[i])
			if !validWorkspaceNode(nodes[i]) {
				return nil, fmt.Errorf("invalid edge node %q", nodes[i])
			}
		}
		for i := 0; i < len(nodes)-1; i++ {
			a, b := nodes[i], nodes[i+1]
			result[a] = append(result[a], b)
			result[b] = append(result[b], a)
		}
	}
	return result, nil
}

func appendUniqueNodeNames(order []string, names ...string) []string {
	seen := map[string]bool{}
	for _, s := range order {
		seen[s] = true
	}
	for _, s := range names {
		if s != "" && !seen[s] {
			order = append(order, s)
			seen[s] = true
		}
	}
	return order
}
