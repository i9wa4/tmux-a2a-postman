package config

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// parseFrontmatter extracts key:value pairs from a YAML-style --- delimited
// block at the start of content. Parse rules:
//   - Splits on the FIRST colon only; values may contain colons
//   - Multi-line values are NOT supported (each line is one entry or ignored)
//   - Quoted strings are NOT supported (quotes are literal characters)
//   - Leading/trailing whitespace on key and value is trimmed
//   - Lines without a colon are ignored
//
// Returns a map of lowercase keys to string values.
func parseFrontmatter(content string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(content, "\n")

	// Find opening ---
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			start = i
			break
		}
	}
	if start == -1 {
		return result
	}

	// Find closing ---
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return result
	}

	// Parse key:value pairs between the delimiters
	for _, line := range lines[start+1 : end] {
		idx := strings.Index(line, ":")
		if idx == -1 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])
		if key != "" {
			result[key] = value
		}
	}
	return result
}

// extractMermaidBlock finds the content of the first ```mermaid...``` fence.
// Returns the content between the fences (not including the fence lines),
// or empty string if not found.
func extractMermaidBlock(content string) string {
	const openFence = "```mermaid"
	const closeFence = "```"

	start := strings.Index(content, openFence)
	if start == -1 {
		return ""
	}
	// Skip past the opening fence line
	afterOpen := content[start+len(openFence):]
	end := strings.Index(afterOpen, closeFence)
	if end == -1 {
		return ""
	}
	return afterOpen[:end]
}

// parseMermaidEdges extracts edge definitions from a Mermaid graph block.
// Strips the optional "graph LR/TD/RL/BT/TB" header line.
// Normalizes "A --- B" (Mermaid undirected) to "A -- B" (ParseEdges format).
// Returns a []string in ParseEdges-compatible format.
func parseMermaidEdges(mermaidBlock string) []string {
	var edges []string
	for _, line := range strings.Split(mermaidBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip Mermaid graph direction declarations
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "graph ") || lower == "graph" {
			continue
		}
		// Normalize Mermaid --- (3-dash bidirectional) to -- (2-dash)
		// Must check for --- before --, since -- is a substring of ---
		line = strings.ReplaceAll(line, "---", "--")
		edges = append(edges, line)
	}
	return edges
}

// extractNodeName extracts the backtick-wrapped name from an h2 heading.
// "## `worker-alt` Node" returns "worker-alt" (lowercased).
// Returns "" if no backtick-wrapped name is found.
func extractNodeName(heading string) string {
	// Strip leading ## and whitespace
	heading = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(heading), "##"))
	return extractBacktickName(heading)
}

// extractBacktickName extracts the first backtick-wrapped name from text.
// "1. `worker-alt` Node" returns "worker-alt" (lowercased).
// Returns "" if no backtick-wrapped name is found.
func extractBacktickName(text string) string {
	start := strings.Index(text, "`")
	if start == -1 {
		return ""
	}
	rest := text[start+1:]
	end := strings.Index(rest, "`")
	if end == -1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(rest[:end]))
}

// stripHeadingNumber strips a leading "N. " prefix from a heading string.
// "1. Edges" returns "Edges"; "Edges" returns "Edges".
func stripHeadingNumber(heading string) string {
	s := strings.TrimSpace(heading)
	return strings.TrimSpace(strings.TrimLeft(s, "0123456789. "))
}

// extractNodeFields extracts role from reserved ### `key` sections within a
// node body. Returns the field value and the body with reserved sections
// stripped. If no reserved sections are found, returns an empty string and the
// original body unchanged.
func extractNodeFields(body string) (role, template string) {
	lines := strings.Split(body, "\n")

	type reserved struct {
		key     string
		heading int
		start   int
		end     int // exclusive
	}
	var sections []reserved

	for i, line := range lines {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		heading := strings.TrimSpace(line[4:]) // strip "### "
		name := extractBacktickName(heading)
		if name != "role" {
			continue
		}
		// Find body end: next heading (## or ###) or EOF
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") || strings.HasPrefix(lines[j], "### ") {
				end = j
				break
			}
		}
		sections = append(sections, reserved{key: name, heading: i, start: i + 1, end: end})
	}

	if len(sections) == 0 {
		return "", body
	}

	// Build exclude set and extract values
	exclude := make(map[int]bool)
	values := make(map[string]string)
	for _, sec := range sections {
		values[sec.key] = strings.TrimSpace(strings.Join(lines[sec.start:sec.end], "\n"))
		for j := sec.heading; j < sec.end; j++ {
			exclude[j] = true
		}
	}

	var kept []string
	for i, line := range lines {
		if !exclude[i] {
			kept = append(kept, line)
		}
	}
	return values["role"], strings.TrimSpace(strings.Join(kept, "\n"))
}

// reservedH2Names maps backtick-wrapped h2 names to their canonical section keys.
// These names are NOT treated as node definitions.
var reservedH2Names = map[string]string{
	"edges":           "edges",
	"common_template": "common_template",
	"message_footer":  "message_footer",
}

// extractH2Sections parses Markdown content into a map of section key → body.
//
// Backtick-wrapped names are extracted from h2 headings. Reserved names
// (edges, common_template) become special sections; all others become node
// sections. Plain-text "## Edges" (case-insensitive, with optional numbering)
// is also accepted for backward compatibility.
// Section body is the text from the heading line (exclusive) until the next
// h2 heading or end of content.
func extractH2Sections(content string) ([]string, map[string]string) {
	sections := make(map[string]string)
	lines := strings.Split(content, "\n")

	type section struct {
		key   string
		start int
	}
	var found []section

	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		heading := line[3:] // strip "## "
		// Try backtick-wrapped name first (handles both reserved and node names)
		name := extractBacktickName(heading)
		if name != "" {
			if canonical, ok := reservedH2Names[name]; ok {
				found = append(found, section{key: canonical, start: i + 1})
			} else {
				found = append(found, section{key: name, start: i + 1})
			}
			continue
		}
		// Fallback: plain-text "Edges" (supports "## 1. Edges")
		cleaned := strings.ToLower(stripHeadingNumber(heading))
		if cleaned == "edges" {
			found = append(found, section{key: "edges", start: i + 1})
		}
	}

	var nodeOrder []string
	for i, sec := range found {
		end := len(lines)
		if i+1 < len(found) {
			// Walk back to find the h2 line of the next section
			// found[i+1].start is the line after its heading, so heading is at start-1
			end = found[i+1].start - 1
		}
		body := strings.Join(lines[sec.start:end], "\n")
		sections[sec.key] = strings.TrimSpace(body)
		if _, reserved := reservedH2Names[sec.key]; !reserved {
			nodeOrder = appendUniqueNodeNames(nodeOrder, sec.key)
		}
	}
	return nodeOrder, sections
}

// stripFrontmatter returns content with the leading --- block removed.
func stripFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			start = i
			break
		}
	}
	if start == -1 {
		return content
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	return content
}

// loadMarkdownConfig parses a postman.md (single-file format) into a Config.
// Returns a zero-value Config with only explicitly-set fields populated.
// Global frontmatter keys: ui_node → Config.UINode,
// reply_command → Config.ReplyCommand.
// Reserved h2 sections: "## `edges`" → Mermaid edges;
// "## `common_template`" → Config.CommonTemplate.
// Node h2 sections: "## `name`" → node template with ### `role` h3 field
// (falls back to per-node frontmatter for backward compat).
func loadMarkdownConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	cfg := &Config{Nodes: make(map[string]NodeConfig), NodeOrder: []string{}}

	// Parse global frontmatter
	fm := parseFrontmatter(content)
	if v, ok := fm["ui_node"]; ok && v != "" {
		cfg.UINode = v
	}
	if v, ok := fm["reply_command"]; ok && v != "" {
		cfg.ReplyCommand = v
	}

	// Parse h2 sections (without frontmatter interfering with h2 detection)
	bodyContent := stripFrontmatter(content)
	nodeOrder, sections := extractH2Sections(bodyContent)
	cfg.recordNodeNames(nodeOrder...)

	// Edges section
	if edgesBody, ok := sections["edges"]; ok {
		mermaidBlock := extractMermaidBlock(edgesBody)
		cfg.Edges = parseMermaidEdges(mermaidBlock)
	}

	// Common template section
	if commonBody, ok := sections["common_template"]; ok {
		cfg.CommonTemplate = strings.TrimSpace(commonBody)
	}

	// Message footer section
	if footerBody, ok := sections["message_footer"]; ok {
		cfg.MessageFooter = strings.TrimSpace(footerBody)
	}

	// Node sections
	for key, body := range sections {
		if _, reserved := reservedH2Names[key]; reserved {
			continue
		}
		// Validate against reserved names
		if key == "postman" || key == "heartbeat" {
			log.Printf("warning: skipping reserved node name %q in %s", key, path)
			continue
		}
		// Try h3 reserved sections first, fall back to frontmatter
		role, tmpl := extractNodeFields(body)
		if role == "" {
			fm := parseFrontmatter(body)
			role = fm["role"]
		}
		// Strip frontmatter from template (harmless if absent)
		tmpl = strings.TrimSpace(stripFrontmatter(tmpl))
		cfg.Nodes[key] = NodeConfig{Template: tmpl, Role: role}
	}

	return cfg, nil
}

// loadNodeMarkdownFile parses a nodes/name.md split-file format into a NodeConfig.
// Uses ### `role` h3 section (preferred) with frontmatter fallback.
// The remaining body (after stripping reserved sections and frontmatter) becomes
// NodeConfig.Template. Returns: nodeName (filename without .md extension),
// NodeConfig, error.
func loadNodeMarkdownFile(path string) (string, NodeConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", NodeConfig{}, err
	}
	content := string(raw)
	nodeName := strings.TrimSuffix(filepath.Base(path), ".md")

	// Try h3 reserved sections first, fall back to frontmatter
	role, tmpl := extractNodeFields(content)
	if role == "" {
		fm := parseFrontmatter(content)
		role = fm["role"]
	}
	// Strip frontmatter from template (harmless if absent)
	tmpl = strings.TrimSpace(stripFrontmatter(tmpl))

	return nodeName, NodeConfig{Template: tmpl, Role: role}, nil
}
