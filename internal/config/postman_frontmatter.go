package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillCatalogInjectPing           = "ping"
	skillCatalogInjectCompactionPing = "compaction_ping"
)

func parsePostmanFrontmatter(content string) (map[string]string, []skillCatalogSpec, []skillCatalogSpec, []skillCatalogSpec, error) {
	scalars := make(map[string]string)
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !ok {
		return scalars, nil, nil, nil, nil
	}

	raw := strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
	if raw == "" {
		return scalars, nil, nil, nil, nil
	}

	var document yaml.Node
	yamlInput := strings.Repeat("\n", start+1) + raw
	if err := yaml.Unmarshal([]byte(yamlInput), &document); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse postman.md frontmatter: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind == yaml.ScalarNode && document.Content[0].Tag == "!!null" {
		return scalars, nil, nil, nil, nil
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, nil, nil, frontmatterNodeError(root, "postman.md frontmatter must be a YAML mapping")
	}

	var skillSpecs []skillCatalogSpec
	var pingSkillSpecs []skillCatalogSpec
	var compactionSkillSpecs []skillCatalogSpec
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		valueNode := root.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode {
			return nil, nil, nil, nil, frontmatterNodeError(keyNode, "frontmatter keys must be strings")
		}

		key := strings.ToLower(strings.TrimSpace(keyNode.Value))
		switch key {
		case "ui_node", "reply_command":
			value, err := parseYAMLScalarString(valueNode)
			if err != nil {
				return nil, nil, nil, nil, frontmatterNodeError(valueNode, key+" must be a scalar value")
			}
			scalars[key] = value
		case "skill_path":
			specs, err := parseSkillPathFrontmatterNode(valueNode, "skill_path", true)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			roleSpecs, pingSpecs, compactionPingSpecs, err := splitSkillPathSpecsByInject(specs)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			skillSpecs = append(skillSpecs, roleSpecs...)
			pingSkillSpecs = append(pingSkillSpecs, pingSpecs...)
			compactionSkillSpecs = append(compactionSkillSpecs, compactionPingSpecs...)
		case "compaction_skill_path":
			return nil, nil, nil, nil, frontmatterNodeError(keyNode, "compaction_skill_path is unsupported; use skill_path entries with inject: compaction_ping")
		}
	}

	return scalars, skillSpecs, pingSkillSpecs, compactionSkillSpecs, nil
}

func splitSkillPathSpecsByInject(specs []skillCatalogSpec) ([]skillCatalogSpec, []skillCatalogSpec, []skillCatalogSpec, error) {
	var roleSpecs []skillCatalogSpec
	var pingSpecs []skillCatalogSpec
	var compactionPingSpecs []skillCatalogSpec
	for _, spec := range specs {
		injects := normalizeSkillCatalogInjects(spec.Injects)
		if len(injects) == 0 {
			spec.Injects = nil
			roleSpecs = append(roleSpecs, spec)
			continue
		}
		for _, inject := range injects {
			routedSpec := spec
			routedSpec.Injects = nil
			if inject == skillCatalogInjectPing {
				pingSpecs = append(pingSpecs, routedSpec)
				continue
			}
			if inject == skillCatalogInjectCompactionPing {
				compactionPingSpecs = append(compactionPingSpecs, routedSpec)
				continue
			}
			return nil, nil, nil, fmt.Errorf("unsupported skill_path item inject %q", inject)
		}
	}
	return roleSpecs, pingSpecs, compactionPingSpecs, nil
}

func parseSkillPathFrontmatterNode(node *yaml.Node, key string, allowInject bool) ([]skillCatalogSpec, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		path, err := parseYAMLScalarString(node)
		if err != nil {
			return nil, frontmatterNodeError(node, key+" must be a path string or a list of path entries")
		}
		if strings.TrimSpace(path) == "" {
			return nil, nil
		}
		return []skillCatalogSpec{{Path: path, All: true}}, nil
	case yaml.SequenceNode:
		specs := make([]skillCatalogSpec, 0, len(node.Content))
		for _, item := range node.Content {
			spec, err := parseSkillPathItemNode(item, key, allowInject)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(spec.Path) != "" {
				if requiresGlobalSkillCatalogPath(key, spec) && !isGlobalSkillCatalogPath(spec.Path) {
					return nil, frontmatterNodeError(item, key+" item requires a global/user-level path (~ or absolute); relative paths are invalid for compaction PING catalogs")
				}
				specs = append(specs, spec)
			}
		}
		return specs, nil
	default:
		return nil, frontmatterNodeError(node, key+" must be a path string or a list of path entries")
	}
}

func parseSkillPathItemNode(node *yaml.Node, key string, allowInject bool) (skillCatalogSpec, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		path, err := parseYAMLScalarString(node)
		if err != nil {
			return skillCatalogSpec{}, frontmatterNodeError(node, key+" list item must be a path string or a mapping")
		}
		return skillCatalogSpec{Path: path, All: true}, nil
	case yaml.MappingNode:
		return parseSkillPathMappingNode(node, key, allowInject)
	default:
		return skillCatalogSpec{}, frontmatterNodeError(node, key+" list item must be a path string or a mapping")
	}
}

func parseSkillPathMappingNode(node *yaml.Node, key string, allowInject bool) (skillCatalogSpec, error) {
	spec := skillCatalogSpec{All: true}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode {
			return skillCatalogSpec{}, frontmatterNodeError(keyNode, key+" item keys must be strings")
		}
		itemKey := strings.ToLower(strings.TrimSpace(keyNode.Value))
		switch itemKey {
		case "path":
			path, err := parseYAMLScalarString(valueNode)
			if err != nil {
				return skillCatalogSpec{}, frontmatterNodeError(valueNode, key+" item path must be a scalar value")
			}
			spec.Path = path
		case "skills":
			all, names, err := parseSkillsSelectorNode(valueNode)
			if err != nil {
				return skillCatalogSpec{}, err
			}
			spec.All = all
			spec.Names = names
		case "inject":
			if !allowInject {
				return skillCatalogSpec{}, frontmatterNodeError(keyNode, fmt.Sprintf("unsupported %s item key %q", key, keyNode.Value))
			}
			injects, err := parseSkillCatalogInjectNode(valueNode, key)
			if err != nil {
				return skillCatalogSpec{}, err
			}
			spec.Injects = injects
		case "runtime":
			return skillCatalogSpec{}, frontmatterNodeError(keyNode, key+" item runtime is unsupported; list explicit skill_path entries instead")
		default:
			return skillCatalogSpec{}, frontmatterNodeError(keyNode, fmt.Sprintf("unsupported %s item key %q", key, keyNode.Value))
		}
	}
	if strings.TrimSpace(spec.Path) == "" {
		return skillCatalogSpec{}, frontmatterNodeError(node, key+" item requires a non-empty path")
	}
	if requiresGlobalSkillCatalogPath(key, spec) && !isGlobalSkillCatalogPath(spec.Path) {
		return skillCatalogSpec{}, frontmatterNodeError(node, key+" item requires a global/user-level path (~ or absolute); relative paths are invalid for compaction PING catalogs")
	}
	return spec, nil
}

func parseSkillCatalogInjectNode(node *yaml.Node, key string) ([]string, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		inject, err := parseYAMLScalarString(node)
		if err != nil {
			return nil, frontmatterNodeError(node, key+" item inject must be a scalar value or a YAML list of scalar values")
		}
		inject = normalizeSkillCatalogInject(inject)
		if isRoleSkillCatalogInject(inject) {
			return nil, nil
		}
		if !isPingSkillCatalogInject(inject) {
			return nil, frontmatterNodeError(node, fmt.Sprintf("unsupported %s item inject %q", key, inject))
		}
		return []string{inject}, nil
	case yaml.SequenceNode:
		injects := make([]string, 0, len(node.Content))
		seen := make(map[string]struct{}, len(node.Content))
		for _, item := range node.Content {
			inject, err := parseYAMLScalarString(item)
			if err != nil {
				return nil, frontmatterNodeError(item, key+" item inject list items must be scalar values")
			}
			inject = normalizeSkillCatalogInject(inject)
			if inject == "" {
				return nil, frontmatterNodeError(item, key+" item inject list items must be non-empty")
			}
			if !isPingSkillCatalogInject(inject) {
				return nil, frontmatterNodeError(item, fmt.Sprintf("unsupported %s item inject %q", key, inject))
			}
			if _, ok := seen[inject]; ok {
				continue
			}
			seen[inject] = struct{}{}
			injects = append(injects, inject)
		}
		if len(injects) == 0 {
			return nil, frontmatterNodeError(node, key+" item inject list must contain at least one inject mode")
		}
		return injects, nil
	default:
		return nil, frontmatterNodeError(node, key+" item inject must be a scalar value or a YAML list of scalar values")
	}
}

func parseSkillsSelectorNode(node *yaml.Node) (bool, []string, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		value, err := parseYAMLScalarString(node)
		if err != nil {
			return false, nil, frontmatterNodeError(node, "skills must be omitted, all, or a YAML list of skill names")
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.EqualFold(value, "all") {
			return true, nil, nil
		}
		return false, nil, frontmatterNodeError(node, "skills must be omitted, all, or a YAML list of skill names")
	case yaml.SequenceNode:
		names := make([]string, 0, len(node.Content))
		seen := make(map[string]struct{}, len(node.Content))
		for _, item := range node.Content {
			name, err := parseYAMLScalarString(item)
			if err != nil {
				return false, nil, frontmatterNodeError(item, "skills list items must be scalar skill names")
			}
			name = strings.TrimSpace(name)
			if name == "" {
				return false, nil, frontmatterNodeError(item, "skills list items must be non-empty")
			}
			if containsGlobMeta(name) {
				return false, nil, frontmatterNodeError(item, "skills does not support glob patterns; list skill names explicitly")
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		if len(names) == 0 {
			return false, nil, frontmatterNodeError(node, "skills list must contain at least one skill name")
		}
		return false, names, nil
	default:
		return false, nil, frontmatterNodeError(node, "skills must be all or a YAML list of skill names")
	}
}

func normalizeSkillCatalogInject(inject string) string {
	return strings.ToLower(strings.TrimSpace(inject))
}

func normalizeSkillCatalogInjects(injects []string) []string {
	normalized := make([]string, 0, len(injects))
	for _, inject := range injects {
		inject = normalizeSkillCatalogInject(inject)
		if inject == "" {
			continue
		}
		normalized = append(normalized, inject)
	}
	return normalized
}

func isRoleSkillCatalogInject(inject string) bool {
	return normalizeSkillCatalogInject(inject) == ""
}

func isPingSkillCatalogInject(inject string) bool {
	switch normalizeSkillCatalogInject(inject) {
	case skillCatalogInjectPing, skillCatalogInjectCompactionPing:
		return true
	default:
		return false
	}
}

func requiresGlobalSkillCatalogPath(key string, spec skillCatalogSpec) bool {
	return key == "skill_path" && hasPingSkillCatalogInject(spec.Injects)
}

func hasPingSkillCatalogInject(injects []string) bool {
	for _, inject := range injects {
		if isPingSkillCatalogInject(inject) {
			return true
		}
	}
	return false
}

func isGlobalSkillCatalogPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "~" || strings.HasPrefix(path, "~/") || filepath.IsAbs(path)
}

func parseYAMLScalarString(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("not scalar")
	}
	if node.Tag == "!!null" {
		return "", nil
	}
	return node.Value, nil
}

func frontmatterNodeError(node *yaml.Node, message string) error {
	if node != nil && node.Line > 0 {
		return fmt.Errorf("unsupported postman.md frontmatter at line %d: %s", node.Line, message)
	}
	return fmt.Errorf("unsupported postman.md frontmatter: %s", message)
}
