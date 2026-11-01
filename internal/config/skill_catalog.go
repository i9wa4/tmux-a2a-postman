// Presentation parsing restored from v0.9.5; routing and runtime authority remain separate.
package config

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

type skillCatalogEntry struct {
	Name        string
	Description string
}

type skillCatalogSpec struct {
	Path    string
	All     bool
	Names   []string
	Injects []string
}

var skillCatalogUserHomeDir = os.UserHomeDir

func renderSkillCatalogs(markdownPath string, specs []skillCatalogSpec) (string, error) {
	entriesByName := make(map[string]skillCatalogEntry)
	var sourceDisplays []string
	sourceDisplaysSeen := make(map[string]struct{})
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
		sourceDisplay := skillPathDisplay(spec.Path, resolvedPath)
		if _, ok := sourceDisplaysSeen[sourceDisplay]; !ok {
			sourceDisplaysSeen[sourceDisplay] = struct{}{}
			sourceDisplays = append(sourceDisplays, sourceDisplay)
		}
		for _, entry := range entries {
			// Later specs win deterministically without duplicate rendered skill bodies.
			entriesByName[entry.Name] = entry
		}
		if len(entriesByName) > 256 {
			return "", fmt.Errorf("combined skill catalog exceeds 256 skills")
		}
		total := 0
		for _, entry := range entriesByName {
			total += len(entry.Name) + len(entry.Description)
		}
		if total > 32*1024 {
			return "", fmt.Errorf("combined skill metadata exceeds 32 KiB")
		}
	}
	if len(entriesByName) == 0 {
		return "", nil
	}
	orderedEntries := make([]skillCatalogEntry, 0, len(entriesByName))
	for _, entry := range entriesByName {
		orderedEntries = append(orderedEntries, entry)
	}
	sort.SliceStable(orderedEntries, func(i, j int) bool {
		return orderedEntries[i].Name < orderedEntries[j].Name
	})
	return renderSkillCatalogFromSources(sourceDisplays, orderedEntries), nil
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

	dir, err := os.OpenFile(resolvedPath, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, resolvedPath, err
	}
	defer func() { _ = dir.Close() }()
	dirEntries, err := dir.ReadDir(257)
	if len(dirEntries) > 256 {
		return nil, resolvedPath, fmt.Errorf("skill catalog exceeds 256 directory entries")
	}
	if err != nil && err != io.EOF {
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
			// A single unparseable SKILL.md must not abort the whole catalog —
			// and with it the entire postman.md config load (issue #612). Warn
			// and skip just this skill so edges/nodes still load.
			log.Printf("postman: WARNING: skipping skill %s: %v\n", dirEntry.Name(), err)
			continue
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
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return skillCatalogEntry{}, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 256*1024 {
		return skillCatalogEntry{}, fmt.Errorf("skill metadata must be a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 256*1024+1))
	if len(raw) > 256*1024 {
		return skillCatalogEntry{}, fmt.Errorf("skill metadata exceeds 256 KiB")
	}
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
	for _, key := range []string{"name", "description"} {
		if value, exists := values[key]; exists {
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return nil, fmt.Errorf("skill %s must be a string", key)
			}
			result[key] = value.Value
		}
	}
	return result, nil
}

func compactSkillDescription(description string) string {
	fields := strings.Fields(description)
	return strings.Join(fields, " ")
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
