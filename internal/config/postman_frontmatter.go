package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/agentruntime"
	"gopkg.in/yaml.v3"
)

const (
	skillCatalogInjectRole           = "role"
	skillCatalogInjectContext        = "context"
	skillCatalogInjectCompactionPing = "compaction_ping"
	skillCatalogInjectPing           = "ping"
)

func parsePostmanFrontmatter(content string) (map[string]string, []skillCatalogSpec, []skillCatalogSpec, error) {
	scalars := make(map[string]string)
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil {
		return nil, nil, nil, err
	}
	if !ok {
		return scalars, nil, nil, nil
	}

	raw := strings.TrimSpace(strings.Join(lines[start+1:end], "\n"))
	if raw == "" {
		return scalars, nil, nil, nil
	}

	var document yaml.Node
	yamlInput := strings.Repeat("\n", start+1) + raw
	if err := yaml.Unmarshal([]byte(yamlInput), &document); err != nil {
		return nil, nil, nil, fmt.Errorf("parse postman.md frontmatter: %w", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind == yaml.ScalarNode && document.Content[0].Tag == "!!null" {
		return scalars, nil, nil, nil
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, nil, frontmatterNodeError(root, "postman.md frontmatter must be a YAML mapping")
	}

	var skillSpecs []skillCatalogSpec
	var compactionSkillSpecs []skillCatalogSpec
	for i := 0; i+1 < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		valueNode := root.Content[i+1]
		if keyNode.Kind != yaml.ScalarNode {
			return nil, nil, nil, frontmatterNodeError(keyNode, "frontmatter keys must be strings")
		}

		key := strings.ToLower(strings.TrimSpace(keyNode.Value))
		switch key {
		case "ui_node", "reply_command":
			value, err := parseYAMLScalarString(valueNode)
			if err != nil {
				return nil, nil, nil, frontmatterNodeError(valueNode, key+" must be a scalar value")
			}
			scalars[key] = value
		case "skill_path":
			specs, err := parseSkillPathFrontmatterNode(valueNode, "skill_path", true, true)
			if err != nil {
				return nil, nil, nil, err
			}
			roleSpecs, compactionPingSpecs, err := splitSkillPathSpecsByInject(specs)
			if err != nil {
				return nil, nil, nil, err
			}
			skillSpecs = append(skillSpecs, roleSpecs...)
			compactionSkillSpecs = append(compactionSkillSpecs, compactionPingSpecs...)
		case "compaction_skill_path":
			specs, err := parseSkillPathFrontmatterNode(valueNode, "compaction_skill_path", true, false)
			if err != nil {
				return nil, nil, nil, err
			}
			compactionSkillSpecs = append(compactionSkillSpecs, specs...)
		}
	}

	return scalars, skillSpecs, compactionSkillSpecs, nil
}

func splitSkillPathSpecsByInject(specs []skillCatalogSpec) ([]skillCatalogSpec, []skillCatalogSpec, error) {
	var roleSpecs []skillCatalogSpec
	var compactionPingSpecs []skillCatalogSpec
	for _, spec := range specs {
		inject := normalizeSkillCatalogInject(spec.Inject)
		if isRoleSkillCatalogInject(inject) {
			spec.Inject = ""
			roleSpecs = append(roleSpecs, spec)
			continue
		}
		if isCompactionPingSkillCatalogInject(inject) {
			spec.Inject = ""
			compactionPingSpecs = append(compactionPingSpecs, spec)
			continue
		}
		return nil, nil, fmt.Errorf("unsupported skill_path item inject %q", spec.Inject)
	}
	return roleSpecs, compactionPingSpecs, nil
}

func parseSkillPathFrontmatterNode(node *yaml.Node, key string, allowRuntime, allowInject bool) ([]skillCatalogSpec, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		path, err := parseYAMLScalarString(node)
		if err != nil {
			return nil, frontmatterNodeError(node, key+" must be a path string or a list of path entries")
		}
		if strings.TrimSpace(path) == "" {
			return nil, nil
		}
		if key == "compaction_skill_path" && !isGlobalSkillCatalogPath(path) {
			return nil, frontmatterNodeError(node, key+" requires a global/user-level path (~ or absolute); relative paths are invalid for compaction PING catalogs")
		}
		return []skillCatalogSpec{{Path: path, All: true}}, nil
	case yaml.SequenceNode:
		specs := make([]skillCatalogSpec, 0, len(node.Content))
		for _, item := range node.Content {
			spec, err := parseSkillPathItemNode(item, key, allowRuntime, allowInject)
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

func parseSkillPathItemNode(node *yaml.Node, key string, allowRuntime, allowInject bool) (skillCatalogSpec, error) {
	switch node.Kind {
	case yaml.ScalarNode:
		path, err := parseYAMLScalarString(node)
		if err != nil {
			return skillCatalogSpec{}, frontmatterNodeError(node, key+" list item must be a path string or a mapping")
		}
		return skillCatalogSpec{Path: path, All: true}, nil
	case yaml.MappingNode:
		return parseSkillPathMappingNode(node, key, allowRuntime, allowInject)
	default:
		return skillCatalogSpec{}, frontmatterNodeError(node, key+" list item must be a path string or a mapping")
	}
}

func parseSkillPathMappingNode(node *yaml.Node, key string, allowRuntime, allowInject bool) (skillCatalogSpec, error) {
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
			inject, err := parseYAMLScalarString(valueNode)
			if err != nil {
				return skillCatalogSpec{}, frontmatterNodeError(valueNode, key+" item inject must be a scalar value")
			}
			inject = normalizeSkillCatalogInject(inject)
			switch {
			case isRoleSkillCatalogInject(inject), isCompactionPingSkillCatalogInject(inject):
				spec.Inject = inject
			default:
				return skillCatalogSpec{}, frontmatterNodeError(valueNode, fmt.Sprintf("unsupported %s item inject %q", key, inject))
			}
		case "runtime":
			if !allowRuntime {
				return skillCatalogSpec{}, frontmatterNodeError(keyNode, fmt.Sprintf("unsupported %s item key %q", key, keyNode.Value))
			}
			runtime, err := parseYAMLScalarString(valueNode)
			if err != nil {
				return skillCatalogSpec{}, frontmatterNodeError(valueNode, key+" item runtime must be a scalar value")
			}
			spec.Runtime = normalizeSkillCatalogRuntime(runtime)
			if spec.Runtime != "" && !agentruntime.IsSupported(spec.Runtime) {
				return skillCatalogSpec{}, frontmatterNodeError(valueNode, fmt.Sprintf("unsupported %s item runtime %q; supported runtimes are %s", key, spec.Runtime, supportedSkillCatalogRuntimeList()))
			}
		default:
			return skillCatalogSpec{}, frontmatterNodeError(keyNode, fmt.Sprintf("unsupported %s item key %q", key, keyNode.Value))
		}
	}
	if strings.TrimSpace(spec.Path) == "" {
		return skillCatalogSpec{}, frontmatterNodeError(node, key+" item requires a non-empty path")
	}
	if key == "skill_path" && spec.Runtime != "" && !isCompactionPingSkillCatalogInject(spec.Inject) {
		return skillCatalogSpec{}, frontmatterNodeError(node, "skill_path item runtime requires inject: compaction_ping")
	}
	if requiresGlobalSkillCatalogPath(key, spec) && !isGlobalSkillCatalogPath(spec.Path) {
		return skillCatalogSpec{}, frontmatterNodeError(node, key+" item requires a global/user-level path (~ or absolute); relative paths are invalid for compaction PING catalogs")
	}
	return spec, nil
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

func isRoleSkillCatalogInject(inject string) bool {
	switch normalizeSkillCatalogInject(inject) {
	case "", skillCatalogInjectRole, skillCatalogInjectContext:
		return true
	default:
		return false
	}
}

func isCompactionPingSkillCatalogInject(inject string) bool {
	switch normalizeSkillCatalogInject(inject) {
	case skillCatalogInjectCompactionPing, skillCatalogInjectPing:
		return true
	default:
		return false
	}
}

func requiresGlobalSkillCatalogPath(key string, spec skillCatalogSpec) bool {
	if key == "compaction_skill_path" {
		return true
	}
	return key == "skill_path" && isCompactionPingSkillCatalogInject(spec.Inject)
}

func isGlobalSkillCatalogPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "~" || strings.HasPrefix(path, "~/") || filepath.IsAbs(path)
}

func supportedSkillCatalogRuntimeList() string {
	definitions := agentruntime.Supported()
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	return strings.Join(ids, ", ")
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
