package config

import (
	"regexp"
	"strings"
)

type markdownFence struct {
	character byte
	length    int
}

// skip recognizes complete fence delimiters, including an outer four-backtick
// fence containing a literal three-backtick line.
func (f *markdownFence) skip(line string) bool {
	if len(line)-len(strings.TrimLeft(line, " ")) > 3 || strings.HasPrefix(line, "\t") {
		return f.length != 0
	}
	line = strings.TrimSpace(line)
	count := 0
	if len(line) > 0 && (line[0] == '`' || line[0] == '~') {
		for count < len(line) && line[count] == line[0] {
			count++
		}
	}
	if f.length != 0 {
		if count >= f.length && line[0] == f.character && strings.TrimSpace(line[count:]) == "" {
			f.length = 0
		}
		return true
	}
	if count >= 3 {
		f.character, f.length = line[0], count
		return true
	}
	return false
}

var generatedHeading = regexp.MustCompile(`^( {0,3})(#{1,6})([ \t]+.*|$)`)

// MarkdownSectionContent keeps generated policy under its surrounding H2
// envelope section. It leaves fenced code and literal sender content alone.
func MarkdownSectionContent(value string) string {
	var lines []string
	var fence markdownFence
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		if !fence.skip(line) {
			if match := generatedHeading.FindStringSubmatch(line); match != nil && len(match[2]) < 3 {
				line = match[1] + "###" + match[3]
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
