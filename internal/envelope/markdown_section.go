package envelope

import "strings"

// MarkdownSectionContent keeps caller-authored Markdown inside an envelope
// section by demoting ATX headings while preserving fenced code blocks.
func MarkdownSectionContent(content string) string {
	return demoteATXHeadings(content, 2)
}

func demoteATXHeadings(content string, levels int) string {
	if content == "" || levels <= 0 {
		return content
	}

	lines := strings.SplitAfter(content, "\n")
	var b strings.Builder
	b.Grow(len(content) + len(lines)*levels)
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0
	for _, line := range lines {
		text := strings.TrimRight(line, "\r\n")
		lineBreak := line[len(text):]
		if inFence {
			if closingFence(text, fenceChar, fenceLen) {
				inFence = false
			}
			b.WriteString(text)
			b.WriteString(lineBreak)
			continue
		}
		if markerChar, markerLen, ok := openingFence(text); ok {
			inFence = true
			fenceChar = markerChar
			fenceLen = markerLen
			b.WriteString(text)
			b.WriteString(lineBreak)
			continue
		}
		text = demoteATXHeadingLine(text, levels)
		b.WriteString(text)
		b.WriteString(lineBreak)
	}
	return b.String()
}

func openingFence(line string) (byte, int, bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	ch := line[i]
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	count := j - i
	if count < 3 {
		return 0, 0, false
	}
	if ch == '`' && strings.Contains(line[j:], "`") {
		return 0, 0, false
	}
	return ch, count, true
}

func closingFence(line string, ch byte, minLen int) bool {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != ch {
		return false
	}
	j := i
	for j < len(line) && line[j] == ch {
		j++
	}
	if j-i < minLen {
		return false
	}
	for ; j < len(line); j++ {
		if line[j] != ' ' && line[j] != '\t' {
			return false
		}
	}
	return true
}

func demoteATXHeadingLine(line string, levels int) string {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != '#' {
		return line
	}
	j := i
	for j < len(line) && line[j] == '#' {
		j++
	}
	count := j - i
	if count == 0 || count > 6 {
		return line
	}
	if j < len(line) && line[j] != ' ' && line[j] != '\t' {
		return line
	}
	newCount := count + levels
	if newCount > 6 {
		newCount = 6
	}
	return line[:i] + strings.Repeat("#", newCount) + line[j:]
}
