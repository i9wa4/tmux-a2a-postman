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

type skillCatalogSpec struct {
	Path    string
	All     bool
	Names   []string
	Runtime string
}

var skillCatalogUserHomeDir = os.UserHomeDir

func appendSkillCatalogToCommonTemplate(commonTemplate, markdownPath, skillPath string) (string, error) {
	return appendSkillCatalogsToCommonTemplate(commonTemplate, markdownPath, []skillCatalogSpec{
		{Path: skillPath, All: true},
	})
}

func appendSkillCatalogsToCommonTemplate(commonTemplate, markdownPath string, specs []skillCatalogSpec) (string, error) {
	catalog, err := renderSkillCatalogs(markdownPath, specs)
	if err != nil {
		return "", err
	}
	return joinTemplateSections(commonTemplate, catalog), nil
}

func renderCompactionSkillCatalogs(markdownPath string, specs []skillCatalogSpec) (map[string]string, error) {
	byRuntime := make(map[string][]skillCatalogSpec)
	var runtimes []string
	for _, spec := range specs {
		if strings.TrimSpace(spec.Path) == "" {
			continue
		}
		runtime := normalizeSkillCatalogRuntime(spec.Runtime)
		if _, ok := byRuntime[runtime]; !ok {
			runtimes = append(runtimes, runtime)
		}
		byRuntime[runtime] = append(byRuntime[runtime], spec)
	}
	if len(byRuntime) == 0 {
		return nil, nil
	}

	globalSpecs := byRuntime[""]
	result := make(map[string]string)
	if len(globalSpecs) > 0 {
		catalog, err := renderSkillCatalogs(markdownPath, globalSpecs)
		if err != nil {
			return nil, err
		}
		if catalog != "" {
			result[""] = catalog
		}
	}

	sort.Strings(runtimes)
	for _, runtime := range runtimes {
		if runtime == "" {
			continue
		}
		runtimeSpecs := append([]skillCatalogSpec{}, globalSpecs...)
		runtimeSpecs = append(runtimeSpecs, byRuntime[runtime]...)
		catalog, err := renderSkillCatalogs(markdownPath, runtimeSpecs)
		if err != nil {
			return nil, err
		}
		if catalog != "" {
			result[runtime] = catalog
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func renderSkillCatalogs(markdownPath string, specs []skillCatalogSpec) (string, error) {
	entriesByName := make(map[string]skillCatalogEntry)
	var orderedEntries []skillCatalogEntry
	var sourceDisplays []string
	for _, spec := range specs {
		if strings.TrimSpace(spec.Path) == "" {
			continue
		}
		entries, resolvedPath, err := loadSkillCatalogSpec(markdownPath, spec)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			continue
		}
		sourceDisplays = append(sourceDisplays, skillPathDisplay(spec.Path, resolvedPath))
		for _, entry := range entries {
			if _, ok := entriesByName[entry.Name]; ok {
				continue
			}
			entriesByName[entry.Name] = entry
			orderedEntries = append(orderedEntries, entry)
		}
	}
	if len(orderedEntries) == 0 {
		return "", nil
	}
	sort.SliceStable(orderedEntries, func(i, j int) bool {
		return orderedEntries[i].Name < orderedEntries[j].Name
	})
	return renderSkillCatalogFromSources(sourceDisplays, orderedEntries), nil
}

func normalizeSkillCatalogRuntime(runtime string) string {
	return strings.ToLower(strings.TrimSpace(runtime))
}

func loadSkillCatalogSpec(markdownPath string, spec skillCatalogSpec) ([]skillCatalogEntry, string, error) {
	if spec.All {
		return loadSkillCatalog(markdownPath, spec.Path)
	}
	return loadSelectedSkillCatalog(markdownPath, spec.Path, spec.Names)
}

func loadSkillCatalog(markdownPath, skillPath string) ([]skillCatalogEntry, string, error) {
	resolvedPath, err := resolveSkillPath(markdownPath, skillPath)
	if err != nil {
		return nil, "", err
	}
	if err := validateSkillPathDir(skillPath, resolvedPath); err != nil {
		return nil, resolvedPath, err
	}

	dirEntries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return nil, resolvedPath, fmt.Errorf("reading skill path %s: %w", skillPath, err)
	}
	sort.SliceStable(dirEntries, func(i, j int) bool {
		return dirEntries[i].Name() < dirEntries[j].Name()
	})

	entries := make([]skillCatalogEntry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		skillDir := filepath.Join(resolvedPath, dirEntry.Name())
		info, err := os.Stat(skillDir)
		if err != nil || !info.IsDir() {
			continue
		}
		skillFile := filepath.Join(skillDir, "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, resolvedPath, fmt.Errorf("stat skill file %s: %w", skillFile, err)
		}
		entry, err := parseSkillCatalogEntry(skillFile)
		if err != nil {
			return nil, resolvedPath, err
		}
		if entry.Name == "" {
			entry.Name = dirEntry.Name()
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, resolvedPath, nil
}

func loadSelectedSkillCatalog(markdownPath, skillPath string, names []string) ([]skillCatalogEntry, string, error) {
	resolvedPath, err := resolveSkillPath(markdownPath, skillPath)
	if err != nil {
		return nil, "", err
	}
	if err := validateSkillPathDir(skillPath, resolvedPath); err != nil {
		return nil, resolvedPath, err
	}

	entries := make([]skillCatalogEntry, 0, len(names))
	for _, name := range names {
		if err := validateSkillName(name); err != nil {
			return nil, resolvedPath, err
		}
		skillFile := filepath.Join(resolvedPath, name, "SKILL.md")
		entry, err := parseSkillCatalogEntry(skillFile)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, resolvedPath, fmt.Errorf("skill %q not found under %s", name, skillPath)
			}
			return nil, resolvedPath, fmt.Errorf("skill %q under %s: %w", name, skillPath, err)
		}
		if entry.Name == "" {
			entry.Name = name
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, resolvedPath, nil
}

func validateSkillPathDir(skillPath, resolvedPath string) error {
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return fmt.Errorf("stat skill path %s: %w", skillPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("skill path %s is not a directory", skillPath)
	}
	return nil
}

func validateSkillName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("skill name must be non-empty")
	}
	if containsGlobMeta(name) {
		return fmt.Errorf("skill %q uses a glob pattern; list skill names explicitly", name)
	}
	if filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("skill %q must be a directory name, not a path", name)
	}
	return nil
}

func containsGlobMeta(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

func resolveSkillPath(markdownPath, skillPath string) (string, error) {
	path := strings.TrimSpace(skillPath)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := skillCatalogUserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand skill path %s: %w", skillPath, err)
		}
		if home == "" {
			return "", fmt.Errorf("expand skill path %s: home directory is empty", skillPath)
		}
		if path == "~" {
			return filepath.Clean(home), nil
		}
		return filepath.Clean(filepath.Join(home, path[2:])), nil
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Clean(filepath.Join(filepath.Dir(markdownPath), path)), nil
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
	return renderSkillCatalogFromSources([]string{skillPath}, entries)
}

func renderSkillCatalogFromSources(skillPaths []string, entries []skillCatalogEntry) string {
	var b strings.Builder
	b.WriteString("### Available Skills\n\n")
	b.WriteString("Read every applicable skill before starting work. Skill files live under ")
	b.WriteString(formatInlineCodeList(skillPaths))
	b.WriteString(".\n\n")
	for _, entry := range entries {
		b.WriteString("- `")
		b.WriteString(escapeInlineCode(entry.Name))
		b.WriteString("`: ")
		b.WriteString(escapeSkillListDescription(entry.Description))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatInlineCodeList(values []string) string {
	if len(values) == 0 {
		return "the configured skill paths"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, "`"+escapeInlineCode(value)+"`")
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, ", ")
}

func escapeInlineCode(value string) string {
	return strings.ReplaceAll(value, "`", "\\`")
}

func escapeSkillListDescription(value string) string {
	return strings.ReplaceAll(value, "\n", " ")
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
