package config

import (
	"fmt"
	"strings"
)

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

// parseMermaidEdges extracts edge definitions from a Mermaid graph block.
// Strips graph headers and non-edge directives.
// Normalizes Mermaid node decorations and keeps "---" as the config edge
// separator.
// Returns normalized local edge definitions.
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

const (
	mermaidInterfaceNodeClass       = "interface_node"
	mermaidCommandApproverNodeClass = "command_approver_node"
)

func parseMermaidInterfaceNode(mermaidBlock string) (string, bool, error) {
	return parseMermaidDesignatedNode(mermaidBlock, mermaidInterfaceNodeClass)
}

func parseMermaidCommandApproverNode(mermaidBlock string) (string, bool, error) {
	return parseMermaidDesignatedNode(mermaidBlock, mermaidCommandApproverNodeClass)
}

func parseMermaidDesignatedNode(mermaidBlock, className string) (string, bool, error) {
	var found string
	for _, statement := range mermaidStatements(mermaidBlock) {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		for _, node := range parseMermaidDesignatedNodeStatement(statement, className) {
			if node == "" {
				continue
			}
			if found == "" {
				found = node
				continue
			}
			if found != node {
				return "", false, fmt.Errorf("multiple Mermaid %s declarations: %q and %q", className, found, node)
			}
		}
	}
	if found == "" {
		return "", false, nil
	}
	return found, true, nil
}

func parseMermaidDesignatedNodeStatement(statement, className string) []string {
	if nodes := parseMermaidClassDesignatedNodeStatement(statement, className); len(nodes) > 0 {
		return nodes
	}
	if shouldSkipMermaidStatement(statement) {
		return nil
	}
	return parseMermaidInlineDesignatedNodeStatement(statement, className)
}

func parseMermaidClassDesignatedNodeStatement(statement, className string) []string {
	fields := strings.Fields(strings.TrimSpace(strings.TrimSuffix(statement, ";")))
	if len(fields) < 3 || strings.ToLower(fields[0]) != "class" {
		return nil
	}
	classStart := -1
	for i := 2; i < len(fields); i++ {
		if mermaidClassTokensContain([]string{fields[i]}, className) {
			classStart = i
			break
		}
	}
	if classStart == -1 {
		return nil
	}
	nodes := make([]string, 0, 1)
	for _, rawNode := range strings.Split(strings.Join(fields[1:classStart], " "), ",") {
		node := normalizeMermaidNodeID(rawNode)
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func parseMermaidInlineDesignatedNodeStatement(statement, className string) []string {
	parts := []string{statement}
	switch {
	case strings.Contains(statement, "---"):
		parts = strings.Split(statement, "---")
	case containsUnsupportedMermaidEdgeOperator(statement):
		return nil
	case !isMermaidStandaloneNodeStatement(statement):
		return nil
	}
	nodes := make([]string, 0, 1)
	for _, rawNode := range parts {
		if !mermaidNodeHasClass(rawNode, className) {
			continue
		}
		node := normalizeMermaidNodeID(rawNode)
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func isMermaidStandaloneNodeStatement(statement string) bool {
	statement = strings.TrimSpace(strings.TrimSuffix(statement, ";"))
	if statement == "" {
		return false
	}
	squareDepth := 0
	parenDepth := 0
	braceDepth := 0
	for i := 0; i < len(statement); i++ {
		switch statement[i] {
		case '[':
			squareDepth++
		case ']':
			if squareDepth > 0 {
				squareDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ' ', '\t':
			if squareDepth == 0 && parenDepth == 0 && braceDepth == 0 {
				return false
			}
		}
	}
	return squareDepth == 0 && parenDepth == 0 && braceDepth == 0
}

func containsUnsupportedMermaidEdgeOperator(statement string) bool {
	operators := []string{
		"-->",
		"<--",
		"==>",
		"<==",
		"-.->",
		"<-.-",
	}
	for _, operator := range operators {
		if strings.Contains(statement, operator) {
			return true
		}
	}
	return false
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
	node := strings.TrimSpace(strings.TrimSuffix(rawNode, ";"))
	classStart := mermaidInlineClassStart(node)
	if classStart < 0 {
		return false
	}
	classes := strings.TrimSpace(node[classStart+3:])
	if cut := mermaidInlineClassSuffixEnd(classes); cut >= 0 {
		classes = classes[:cut]
	}
	return mermaidClassTokensContain(strings.Split(classes, ":::"), want)
}

func mermaidInlineClassStart(node string) int {
	squareDepth := 0
	parenDepth := 0
	braceDepth := 0
	for i := 0; i < len(node); i++ {
		if squareDepth == 0 && parenDepth == 0 && braceDepth == 0 &&
			i+3 <= len(node) && node[i:i+3] == ":::" {
			return i
		}
		switch node[i] {
		case '[':
			squareDepth++
		case ']':
			if squareDepth > 0 {
				squareDepth--
			}
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		}
	}
	return -1
}

func mermaidInlineClassSuffixEnd(classes string) int {
	for i := 0; i < len(classes); i++ {
		switch classes[i] {
		case ' ', '\t', ';', '[', '(', '{':
			return i
		case '@':
			if i+1 < len(classes) && classes[i+1] == '{' {
				return i
			}
		}
	}
	return -1
}

func mermaidStatements(block string) []string {
	var statements []string
	for _, line := range strings.Split(block, "\n") {
		line = stripMermaidComment(line)
		statements = append(statements, strings.Split(line, ";")...)
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
// (edges, common_template, message_footer) become special sections; all other
// names become node sections.
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
