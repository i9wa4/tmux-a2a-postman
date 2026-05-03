package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type skillCatalogEntry struct {
	Name        string
	Description string
}

func appendSkillCatalogToCommonTemplate(commonTemplate, markdownPath, skillPath string) (string, error) {
	entries, resolvedPath, err := loadSkillCatalog(markdownPath, skillPath)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return commonTemplate, nil
	}
	catalog := renderSkillCatalog(skillPathDisplay(skillPath, resolvedPath), entries)
	return joinTemplateSections(commonTemplate, catalog), nil
}

func loadSkillCatalog(markdownPath, skillPath string) ([]skillCatalogEntry, string, error) {
	resolvedPath := resolveSkillPath(markdownPath, skillPath)
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, resolvedPath, fmt.Errorf("stat skill path %s: %w", skillPath, err)
	}
	if !info.IsDir() {
		return nil, resolvedPath, fmt.Errorf("skill path %s is not a directory", skillPath)
	}
	matches, err := filepath.Glob(filepath.Join(resolvedPath, "*", "SKILL.md"))
	if err != nil {
		return nil, resolvedPath, fmt.Errorf("reading skill path %s: %w", skillPath, err)
	}
	sort.Strings(matches)

	entries := make([]skillCatalogEntry, 0, len(matches))
	for _, match := range matches {
		entry, err := parseSkillCatalogEntry(match)
		if err != nil {
			return nil, resolvedPath, err
		}
		if entry.Name == "" {
			entry.Name = filepath.Base(filepath.Dir(match))
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, resolvedPath, nil
}

func resolveSkillPath(markdownPath, skillPath string) string {
	path := strings.TrimSpace(skillPath)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(markdownPath), path))
}

func skillPathDisplay(skillPath, resolvedPath string) string {
	trimmed := strings.TrimSpace(skillPath)
	if trimmed != "" {
		return filepath.ToSlash(trimmed)
	}
	return filepath.ToSlash(resolvedPath)
}

func parseSkillCatalogEntry(path string) (skillCatalogEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return skillCatalogEntry{}, fmt.Errorf("reading skill file %s: %w", path, err)
	}
	fm, err := parseSkillFrontmatter(string(raw))
	if err != nil {
		return skillCatalogEntry{}, fmt.Errorf("%s: %w", path, err)
	}
	return skillCatalogEntry{
		Name:        fm["name"],
		Description: compactSkillDescription(fm["description"]),
	}, nil
}

func parseSkillFrontmatter(content string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil {
		return nil, err
	}
	if !ok {
		return result, nil
	}

	for i := start + 1; i < end; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if hasIndentPrefix(line) {
			return nil, unsupportedFrontmatterSyntaxError(i+1, line)
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			return nil, unsupportedFrontmatterSyntaxError(i+1, line)
		}
		key := strings.TrimSpace(strings.ToLower(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])
		if key == "" {
			return nil, unsupportedFrontmatterSyntaxError(i+1, line)
		}
		if isYAMLBlockScalar(value) {
			block, next := readYAMLBlockScalar(lines, i+1, end, strings.HasPrefix(value, ">"))
			result[key] = block
			i = next - 1
			continue
		}
		result[key] = value
	}
	return result, nil
}

func isYAMLBlockScalar(value string) bool {
	return value == "|" || value == "|-" || value == "|+" || value == ">" || value == ">-" || value == ">+"
}

func readYAMLBlockScalar(lines []string, start, end int, folded bool) (string, int) {
	var block []string
	i := start
	for ; i < end; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			block = append(block, "")
			continue
		}
		if !hasIndentPrefix(line) {
			break
		}
		block = append(block, strings.TrimSpace(line))
	}
	if folded {
		return foldYAMLBlock(block), i
	}
	return strings.Join(block, "\n"), i
}

func foldYAMLBlock(lines []string) string {
	var paragraphs []string
	var current []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				paragraphs = append(paragraphs, strings.Join(current, " "))
				current = nil
			}
			continue
		}
		current = append(current, strings.TrimSpace(line))
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, strings.Join(current, " "))
	}
	return strings.Join(paragraphs, "\n\n")
}

func compactSkillDescription(description string) string {
	fields := strings.Fields(description)
	return strings.Join(fields, " ")
}

func renderSkillCatalog(skillPath string, entries []skillCatalogEntry) string {
	rows := make([][]string, 0, len(entries)+1)
	rows = append(rows, []string{"Skill", "Description"})
	for _, entry := range entries {
		rows = append(rows, []string{
			"`" + escapeMarkdownTableCell(entry.Name) + "`",
			escapeMarkdownTableCell(entry.Description),
		})
	}

	widths := []int{0, 0}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	var b strings.Builder
	b.WriteString("### Available Skills\n\n")
	b.WriteString("Read every applicable skill before starting work. Skill files live under `")
	b.WriteString(escapeInlineCode(skillPath))
	b.WriteString("`.\n\n")
	writeSkillCatalogRow(&b, rows[0], widths)
	b.WriteString("| ")
	b.WriteString(strings.Repeat("-", widths[0]))
	b.WriteString(" | ")
	b.WriteString(strings.Repeat("-", widths[1]))
	b.WriteString(" |\n")
	for _, row := range rows[1:] {
		writeSkillCatalogRow(&b, row, widths)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeSkillCatalogRow(b *strings.Builder, row []string, widths []int) {
	fmt.Fprintf(b, "| %-*s | %-*s |\n", widths[0], row[0], widths[1], row[1])
}

func escapeMarkdownTableCell(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	return strings.ReplaceAll(value, "|", "\\|")
}

func escapeInlineCode(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}

func joinTemplateSections(first, second string) string {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + "\n\n" + second
}
