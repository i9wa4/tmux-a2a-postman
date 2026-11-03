package config

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"

	"gopkg.in/yaml.v3"
)

type RecipientRole struct {
	Role         string
	Instructions string
}

// RecipientPolicy is descriptive data loaded once before mailbox mutation.
// Its input is the exact already-validated Markdown used for current routing.
type RecipientPolicy struct {
	Common  string
	Catalog string
	Footer  string
	Nodes   map[string]RecipientRole
}

func (t WorkspaceTopology) RecipientPolicy(ctx context.Context, path string) (RecipientPolicy, error) {
	result := RecipientPolicy{Nodes: map[string]RecipientRole{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	_, sections := extractH2Sections(stripFrontmatter(t.messageSource))
	result.Common, result.Footer = strings.TrimSpace(sections["common_template"]), strings.TrimSpace(sections["message_footer"])
	for _, name := range t.Nodes {
		body := strings.TrimSpace(sections[name])
		role, err := roleDescription(body)
		if err != nil {
			return result, err
		}
		result.Nodes[name] = RecipientRole{Role: role, Instructions: body}
	}
	_, ordinary, _, _, err := parsePostmanFrontmatter(t.messageSource)
	if err != nil {
		return result, err
	}
	if len(ordinary) > 32 {
		return result, errors.New("at most 32 skill catalog roots are supported")
	}
	for _, spec := range ordinary {
		if len(spec.Names) > 256 {
			return result, errors.New("at most 256 selected skills are supported per catalog")
		}
	}
	if t.builtin {
		var catalog strings.Builder
		catalog.WriteString("### Available Skills\n\nThese embedded entry points need no installed Skill tree. Read each selected reference through EOF before the workflow. Inspect the entry and its reference table with `tmux-a2a-postman help <name>`.\n\n")
		for _, name := range helpdocs.WorkflowNames() {
			raw, readErr := helpdocs.Read(name)
			if readErr != nil {
				return result, readErr
			}
			metadata, parseErr := parseSkillFrontmatter(string(raw))
			if parseErr != nil {
				return result, parseErr
			}
			catalog.WriteString("- `" + name + "`: " + escapeSkillListDescription(metadata["description"]) + "\n")
		}
		result.Catalog = catalog.String()
	} else {
		result.Catalog, err = renderSkillCatalogs(path, ordinary)
	}
	if err != nil {
		return result, err
	}
	if len(result.Catalog) > 32*1024 {
		return result, errors.New("generated skill catalog exceeds 32 KiB")
	}
	return result, ctx.Err()
}

var numberedRoleHeading = regexp.MustCompile("(?i)^###\\s+(?:[0-9]+(?:\\.[0-9]+)*\\.?\\s+)?`role`\\s*$")

func roleDescription(body string) (string, error) {
	reading := false
	var fence markdownFence
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence.skip(line) {
			continue
		}
		if numberedRoleHeading.MatchString(trimmed) {
			reading = true
			continue
		}
		if reading && strings.HasPrefix(line, "### ") {
			break
		}
		if reading {
			lines = append(lines, line)
		}
	}
	if reading {
		return strings.TrimSpace(strings.Join(lines, "\n")), nil
	}
	frontLines := strings.Split(body, "\n")
	start, end, present, err := frontmatterBounds(frontLines)
	if err != nil || !present {
		return "", err
	}
	var fields map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(frontLines[start+1:end], "\n")), &fields); err != nil {
		return "", err
	}
	if role, ok := fields["role"]; ok {
		if role.Kind != yaml.ScalarNode || role.Tag != "!!str" {
			return "", errors.New("recipient role description must be a string")
		}
		return role.Value, nil
	}
	return "", nil
}
