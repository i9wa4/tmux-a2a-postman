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
	start := strings.Index(heading, "`")
	if start == -1 {
		return ""
	}
	rest := heading[start+1:]
	end := strings.Index(rest, "`")
	if end == -1 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(rest[:end]))
}

// extractH2Sections parses Markdown content into a map of section key → body.
//
// Special heading: "## Edges" (case-insensitive) → key "edges".
// Node headings: "## `name` ..." → key is the name extracted from backticks
// (lowercased). Headings without a backtick-wrapped name are skipped.
// Section body is the text from the heading line (exclusive) until the next
// h2 heading or end of content.
func extractH2Sections(content string) map[string]string {
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
		// Check for special "Edges" heading
		if strings.EqualFold(strings.TrimSpace(heading), "edges") {
			found = append(found, section{key: "edges", start: i + 1})
			continue
		}
		name := extractNodeName(line)
		if name != "" {
			found = append(found, section{key: name, start: i + 1})
		}
	}

	for i, sec := range found {
		end := len(lines)
		if i+1 < len(found) {
			// Walk back to find the h2 line of the next section
			// found[i+1].start is the line after its heading, so heading is at start-1
			end = found[i+1].start - 1
		}
		body := strings.Join(lines[sec.start:end], "\n")
		sections[sec.key] = strings.TrimSpace(body)
	}
	return sections
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
// h2 sections: "## Edges" → parse Mermaid block into Config.Edges;
// "## `name`" → node template (body) and per-node frontmatter (on_join, role).
func loadMarkdownConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	cfg := &Config{Nodes: make(map[string]NodeConfig)}

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
	sections := extractH2Sections(bodyContent)

	// Edges section
	if edgesBody, ok := sections["edges"]; ok {
		mermaidBlock := extractMermaidBlock(edgesBody)
		cfg.Edges = parseMermaidEdges(mermaidBlock)
	}

	// Node sections
	for key, body := range sections {
		if key == "edges" {
			continue
		}
		// Validate against reserved names
		if key == "postman" || key == "heartbeat" {
			log.Printf("warning: skipping reserved node name %q in %s", key, path)
			continue
		}
		nodeCfg := NodeConfig{Template: strings.TrimSpace(stripFrontmatter(body))}
		// Per-node frontmatter embedded in the section body
		nodeFM := parseFrontmatter(body)
		if v, ok := nodeFM["on_join"]; ok && v != "" {
			nodeCfg.OnJoin = v
		}
		if v, ok := nodeFM["role"]; ok && v != "" {
			nodeCfg.Role = v
		}
		cfg.Nodes[key] = nodeCfg
	}

	return cfg, nil
}

// loadNodeMarkdownFile parses a nodes/name.md split-file format into a NodeConfig.
// The file body (after stripping frontmatter) becomes NodeConfig.Template.
// Frontmatter supports: on_join, role.
// ui_node and reply_command are Config-level fields (not NodeConfig) and are
// silently ignored.
// Returns: nodeName (filename without .md extension), NodeConfig, error.
func loadNodeMarkdownFile(path string) (string, NodeConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", NodeConfig{}, err
	}
	content := string(raw)
	nodeName := strings.TrimSuffix(filepath.Base(path), ".md")

	fm := parseFrontmatter(content)
	body := strings.TrimSpace(stripFrontmatter(content))

	nodeCfg := NodeConfig{Template: body}
	if v, ok := fm["on_join"]; ok && v != "" {
		nodeCfg.OnJoin = v
	}
	if v, ok := fm["role"]; ok && v != "" {
		nodeCfg.Role = v
	}

	return nodeName, nodeCfg, nil
}
