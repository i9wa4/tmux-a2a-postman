package envelope

import "strings"

// MarkdownSectionContent keeps caller-authored Markdown visually inside an
// envelope h2 section. It normalizes the shallowest ATX heading to h3, preserves
// relative heading depth, and leaves fenced code blocks untouched.
func MarkdownSectionContent(content string) string {
	return normalizeATXHeadingsUnderH2(content)
}

func normalizeATXHeadingsUnderH2(content string) string {
	if content == "" {
		return content
	}

	lines := strings.SplitAfter(content, "\n")
	minLevel, ok := shallowestATXHeadingLevel(lines)
	if !ok {
		return content
	}
	shift := 3 - minLevel

	var b strings.Builder
	if shift > 0 {
		b.Grow(len(content) + len(lines)*shift)
	} else {
		b.Grow(len(content))
	}
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
		text = shiftATXHeadingLine(text, shift)
		b.WriteString(text)
		b.WriteString(lineBreak)
	}
	return b.String()
}

func shallowestATXHeadingLevel(lines []string) (int, bool) {
	minLevel := 7
	inFence := false
	fenceChar := byte(0)
	fenceLen := 0
	for _, line := range lines {
		text := strings.TrimRight(line, "\r\n")
		if inFence {
			if closingFence(text, fenceChar, fenceLen) {
				inFence = false
			}
			continue
		}
		if markerChar, markerLen, ok := openingFence(text); ok {
			inFence = true
			fenceChar = markerChar
			fenceLen = markerLen
			continue
		}
		level, ok := atxHeadingLevel(text)
		if !ok {
			continue
		}
		if level < minLevel {
			minLevel = level
		}
	}
	if minLevel == 7 {
		return 0, false
	}
	return minLevel, true
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

func shiftATXHeadingLine(line string, shift int) string {
	if shift == 0 {
		return line
	}
	level, ok := atxHeadingLevel(line)
	if !ok {
		return line
	}
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	j := i + level
	newLevel := level + shift
	if newLevel < 3 {
		newLevel = 3
	}
	if newLevel > 6 {
		newLevel = 6
	}
	return line[:i] + strings.Repeat("#", newLevel) + line[j:]
}

func atxHeadingLevel(line string) (int, bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || line[i] != '#' {
		return 0, false
	}
	j := i
	for j < len(line) && line[j] == '#' {
		j++
	}
	count := j - i
	if count == 0 || count > 6 {
		return 0, false
	}
	if j < len(line) && line[j] != ' ' && line[j] != '\t' {
		return 0, false
	}
	return count, true
}
