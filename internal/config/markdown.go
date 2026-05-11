package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// parseFrontmatter parses the supported Markdown frontmatter syntax subset.
// This is intentionally not real YAML. Supported syntax is a leading ---
// delimited block with one single-line key: value pair per non-empty line.
// Parse rules:
//   - Splits on the FIRST colon only; values may contain colons
//   - Multi-line values are NOT supported
//   - Quoted strings are NOT supported (quotes are literal characters)
//   - Leading/trailing whitespace on key and value is trimmed
//   - Blank lines are allowed
//
// Returns a map of lowercase keys to string values.
func parseFrontmatter(content string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(content, "\n")
	start, end, ok, err := frontmatterBounds(lines)
	if err != nil {
		return nil, err
	}
	if !ok {
		return result, nil
	}

	previousEmptyValue := false
	for i, line := range lines[start+1 : end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if previousEmptyValue && hasIndentPrefix(line) {
			return nil, unsupportedFrontmatterSyntaxError(start+i+2, line)
		}
		if strings.HasPrefix(trimmed, "- ") {
			return nil, unsupportedFrontmatterSyntaxError(start+i+2, line)
		}
		idx := strings.Index(line, ":")
		if idx == -1 {
			return nil, unsupportedFrontmatterSyntaxError(start+i+2, line)
		}
		key := strings.TrimSpace(strings.ToLower(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])
		if key == "" {
			return nil, unsupportedFrontmatterSyntaxError(start+i+2, line)
		}
		result[key] = value
		previousEmptyValue = value == ""
	}
	return result, nil
}

func frontmatterBounds(lines []string) (int, int, bool, error) {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) || strings.TrimSpace(lines[start]) != "---" {
		return 0, 0, false, nil
	}

	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return start, i, true, nil
		}
	}
	return 0, 0, true, fmt.Errorf("unclosed markdown frontmatter starting at line %d", start+1)
}

func hasIndentPrefix(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func unsupportedFrontmatterSyntaxError(lineNumber int, line string) error {
	return fmt.Errorf(
		"unsupported markdown frontmatter syntax at line %d: %q (supported syntax is one single-line key: value pair per non-empty line; lists, nesting, and multiline values are unsupported)",
		lineNumber,
		line,
	)
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
// Strips graph headers and non-edge directives.
// Normalizes Mermaid node decorations and keeps "---" as the config edge
// separator.
// Returns a []string in the Config.Edges format.
func parseMermaidEdges(mermaidBlock string) []string {
	var edges []string
	for _, statement := range mermaidStatements(mermaidBlock) {
		statement = strings.TrimSpace(statement)
		if statement == "" || shouldSkipMermaidStatement(statement) {
			continue
		}
		if edge, ok := parseMermaidEdgeStatement(statement); ok {
			edges = append(edges, edge)
		}
	}
	return edges
}

const mermaidUINodeClass = "ui_node"

func parseMermaidUINode(mermaidBlock string) (string, bool, error) {
	var found string
	for _, statement := range mermaidStatements(mermaidBlock) {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		for _, node := range parseMermaidUINodeStatement(statement) {
			if node == "" {
				continue
			}
			if found == "" {
				found = node
				continue
			}
			if found != node {
				return "", false, fmt.Errorf("multiple Mermaid ui_node declarations: %q and %q", found, node)
			}
		}
	}
	if found == "" {
		return "", false, nil
	}
	return found, true, nil
}

func parseMermaidUINodeStatement(statement string) []string {
	if nodes := parseMermaidClassUINodeStatement(statement); len(nodes) > 0 {
		return nodes
	}
	if shouldSkipMermaidStatement(statement) {
		return nil
	}
	return parseMermaidInlineUINodeStatement(statement)
}

func parseMermaidClassUINodeStatement(statement string) []string {
	fields := strings.Fields(strings.TrimSpace(strings.TrimSuffix(statement, ";")))
	if len(fields) < 3 || strings.ToLower(fields[0]) != "class" {
		return nil
	}
	if !mermaidClassTokensContain(fields[2:], mermaidUINodeClass) {
		return nil
	}
	nodes := make([]string, 0, 1)
	for _, rawNode := range strings.Split(fields[1], ",") {
		node := normalizeMermaidNodeID(rawNode)
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func parseMermaidInlineUINodeStatement(statement string) []string {
	if !strings.Contains(statement, "---") {
		return nil
	}
	nodes := make([]string, 0, 1)
	for _, rawNode := range strings.Split(statement, "---") {
		if !mermaidNodeHasClass(rawNode, mermaidUINodeClass) {
			continue
		}
		node := normalizeMermaidNodeID(rawNode)
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func mermaidClassTokensContain(tokens []string, want string) bool {
	for _, token := range tokens {
		token = strings.TrimSpace(strings.TrimSuffix(token, ";"))
		for _, class := range strings.Split(token, ",") {
			if strings.TrimSpace(class) == want {
				return true
			}
		}
	}
	return false
}

func mermaidNodeHasClass(rawNode string, want string) bool {
	idx := strings.Index(rawNode, ":::")
	if idx < 0 {
		return false
	}
	classes := strings.TrimSpace(rawNode[idx+3:])
	if cut := strings.IndexAny(classes, " \t;"); cut >= 0 {
		classes = classes[:cut]
	}
	return mermaidClassTokensContain([]string{classes}, want)
}

func mermaidStatements(block string) []string {
	var statements []string
	for _, line := range strings.Split(block, "\n") {
		line = stripMermaidComment(line)
		for _, part := range strings.Split(line, ";") {
			statements = append(statements, part)
		}
	}
	return statements
}

func stripMermaidComment(line string) string {
	if idx := strings.Index(line, "%%"); idx >= 0 {
		return line[:idx]
	}
	return line
}

func shouldSkipMermaidStatement(statement string) bool {
	fields := strings.Fields(strings.ToLower(statement))
	if len(fields) == 0 {
		return true
	}
	switch fields[0] {
	case "graph", "flowchart", "subgraph", "end", "direction", "classdef",
		"class", "style", "click", "linkstyle", "acctitle", "accdescr":
		return true
	default:
		return false
	}
}

func parseMermaidEdgeStatement(statement string) (string, bool) {
	operators := []struct {
		raw        string
		normalized string
	}{
		{"---", "---"},
	}
	for _, op := range operators {
		if !strings.Contains(statement, op.raw) {
			continue
		}
		parts := strings.Split(statement, op.raw)
		if len(parts) < 2 {
			return "", false
		}
		nodes := make([]string, 0, len(parts))
		for _, part := range parts {
			node := normalizeMermaidNodeID(part)
			if node == "" {
				return "", false
			}
			nodes = append(nodes, node)
		}
		return strings.Join(nodes, " "+op.normalized+" "), true
	}
	return "", false
}

func normalizeMermaidNodeID(raw string) string {
	node := strings.TrimSpace(raw)
	node = strings.TrimSuffix(node, ";")
	if idx := strings.Index(node, ":::"); idx >= 0 {
		node = node[:idx]
	}
	if idx := strings.Index(node, "@{"); idx >= 0 {
		node = node[:idx]
	}
	if idx := strings.IndexAny(node, "[({"); idx >= 0 {
		node = node[:idx]
	}
	node = strings.TrimSpace(node)
	node = strings.Trim(node, "`\"'")
	return strings.TrimSpace(node)
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

// extractH2Sections parses Markdown content into a map of section key -> body.
//
// Backtick-wrapped names are extracted from h2 headings. Reserved names
// (edges, common_template) become special sections; all others become node
// sections.
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
	_, end, ok, err := frontmatterBounds(lines)
	if err != nil || !ok {
		return content
	}
	return strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
}

// loadMarkdownConfig parses a postman.md (single-file format) into a Config.
// Returns a zero-value Config with only explicitly-set fields populated.
// Global frontmatter keys: ui_node → Config.UINode override,
// reply_command → Config.ReplyCommand,
// skill_path → generated skill catalog appended to Config.CommonTemplate, or
// to daemon PING role content when an entry uses inject: ping or inject: compaction_ping.
// Mermaid edges may mark the UI node with the ui_node class when frontmatter
// does not override it.
// Reserved h2 sections: "## `edges`" → Mermaid edges;
// "## `common_template`" → Config.CommonTemplate.
// Node h2 sections: "## `name`" → node template with ### `role` h3 field.
func loadMarkdownConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(raw)
	cfg := &Config{Nodes: make(map[string]NodeConfig), NodeOrder: []string{}, PingSkillCatalogs: make(map[string]string), CompactionSkillCatalogs: make(map[string]string)}

	// Parse global frontmatter.
	fm, skillCatalogSpecs, pingSkillCatalogSpecs, compactionSkillCatalogSpecs, err := parsePostmanFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if v, ok := fm["ui_node"]; ok {
		cfg.UINode = v
		cfg.uiNodeSet = true
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
		if !cfg.uiNodeSet {
			uiNode, ok, err := parseMermaidUINode(mermaidBlock)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if ok {
				cfg.UINode = uiNode
				cfg.uiNodeSet = true
			}
		}
	}

	// Common template section
	if commonBody, ok := sections["common_template"]; ok {
		cfg.CommonTemplate = strings.TrimSpace(commonBody)
	}
	if len(skillCatalogSpecs) > 0 {
		commonTemplate, err := appendSkillCatalogsToCommonTemplate(cfg.CommonTemplate, path, skillCatalogSpecs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cfg.CommonTemplate = commonTemplate
	}
	if len(pingSkillCatalogSpecs) > 0 {
		catalogs, err := renderRuntimeSkillCatalogs(path, pingSkillCatalogSpecs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cfg.PingSkillCatalogs = catalogs
	}
	if len(compactionSkillCatalogSpecs) > 0 {
		catalogs, err := renderRuntimeSkillCatalogs(path, compactionSkillCatalogSpecs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cfg.CompactionSkillCatalogs = catalogs
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
		if key == "postman" {
			log.Printf("warning: skipping reserved node name %q in %s", key, path)
			continue
		}
		// Try h3 reserved sections first, fall back to frontmatter
		role, tmpl := extractNodeFields(body)
		if role == "" {
			fm, err := parseFrontmatter(body)
			if err != nil {
				return nil, fmt.Errorf("%s: node %q: %w", path, key, err)
			}
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
		fm, err := parseFrontmatter(content)
		if err != nil {
			return "", NodeConfig{}, fmt.Errorf("%s: %w", path, err)
		}
		role = fm["role"]
	}
	// Strip frontmatter from template (harmless if absent)
	tmpl = strings.TrimSpace(stripFrontmatter(tmpl))

	return nodeName, NodeConfig{Template: tmpl, Role: role}, nil
}
