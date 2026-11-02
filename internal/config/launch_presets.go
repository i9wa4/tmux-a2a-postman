package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxLaunchPanes = 64

var presetName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type LaunchPane struct {
	Role    string `json:"role"`
	Command string `json:"command"`
}
type LaunchColumn struct {
	Panes []LaunchPane `json:"panes"`
}
type LaunchPreset struct {
	Columns []LaunchColumn `json:"columns"`
}

// LaunchPresets parses inert startup data from the exact already validated
// configuration. Ordinary routing/rendering never invokes or executes presets.
func (t WorkspaceTopology) LaunchPresets() (map[string]LaunchPreset, error) {
	result := map[string]LaunchPreset{}
	lines := strings.Split(t.messageSource, "\n")
	start, end, exists, err := frontmatterBounds(lines)
	if err != nil || !exists {
		return result, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")), &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return result, nil
	}
	mapping := document.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter must be a mapping")
	}
	var presets *yaml.Node
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "presets" {
			presets = mapping.Content[i+1]
		}
	}
	if presets == nil {
		return result, nil
	}
	if presets.Kind != yaml.MappingNode || len(presets.Content) == 0 || len(presets.Content) > 64 {
		return nil, errors.New("presets must contain 1 to 32 named mappings")
	}
	roles := map[string]bool{}
	for _, role := range t.Nodes {
		roles[role] = true
	}
	for i := 0; i < len(presets.Content); i += 2 {
		key, value := presets.Content[i], presets.Content[i+1]
		if key.Tag != "!!str" || !presetName.MatchString(key.Value) {
			return nil, errors.New("preset names must be 1 to 64 lowercase letters, digits or hyphens, starting alphanumeric")
		}
		parsed, err := parseLaunchPreset(value, roles)
		if err != nil {
			return nil, fmt.Errorf("preset %s: %w", key.Value, err)
		}
		result[key.Value] = parsed
	}
	return result, nil
}

func presetFields(node *yaml.Node, allowed ...string) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errors.New("expected a mapping")
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		name := node.Content[i].Value
		ok := false
		for _, key := range allowed {
			if key == name {
				ok = true
			}
		}
		if !ok || fields[name] != nil {
			return nil, fmt.Errorf("unsupported or duplicate preset field %q", name)
		}
		fields[name] = node.Content[i+1]
	}
	return fields, nil
}

func parseLaunchPreset(node *yaml.Node, roles map[string]bool) (LaunchPreset, error) {
	result := LaunchPreset{Columns: []LaunchColumn{}}
	fields, err := presetFields(node, "columns")
	if err != nil {
		return result, err
	}
	columns := fields["columns"]
	if columns == nil || columns.Kind != yaml.SequenceNode || len(columns.Content) < 1 || len(columns.Content) > 8 {
		return result, errors.New("columns must be an ordered list of 1 to 8 columns")
	}
	seen := map[string]bool{}
	for ci, column := range columns.Content {
		fields, err := presetFields(column, "panes")
		if err != nil {
			return result, fmt.Errorf("column %d: %w", ci+1, err)
		}
		panes := fields["panes"]
		if panes == nil || panes.Kind != yaml.SequenceNode || len(panes.Content) == 0 {
			return result, fmt.Errorf("column %d requires a nonempty panes list", ci+1)
		}
		parsed := LaunchColumn{Panes: []LaunchPane{}}
		for pi, pane := range panes.Content {
			fields, err := presetFields(pane, "role", "command")
			if err != nil {
				return result, fmt.Errorf("column %d pane %d: %w", ci+1, pi+1, err)
			}
			role, command := fields["role"], fields["command"]
			if role == nil || role.Tag != "!!str" || !roles[role.Value] || seen[role.Value] {
				return result, fmt.Errorf("column %d pane %d: role must be an existing, unique topology node", ci+1, pi+1)
			}
			if command == nil || command.Tag != "!!str" || strings.TrimSpace(command.Value) == "" || len(command.Value) > 8192 || !utf8.ValidString(command.Value) || strings.ContainsRune(command.Value, 0) {
				return result, fmt.Errorf("role %s requires a UTF-8 command of 1 to 8192 bytes without NUL", role.Value)
			}
			seen[role.Value] = true
			if len(seen) > MaxLaunchPanes {
				return result, errors.New("preset exceeds 64 panes")
			}
			parsed.Panes = append(parsed.Panes, LaunchPane{Role: role.Value, Command: command.Value})
		}
		result.Columns = append(result.Columns, parsed)
	}
	return result, nil
}
