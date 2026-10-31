package idle

import (
	"crypto/sha256"
	"hash/crc32"
	"strings"
	"unicode"

	"github.com/i9wa4/tmux-a2a-postman/internal/agentruntime"
)

const (
	maxCompactionPrefixTailBytes   = 256
	maxCompactionSuffixWindowBytes = 256
)

type compactionSuffixIdentity struct {
	Length int
	Hash   [sha256.Size]byte
	Head   string
	Tail   string
}

func hashContentCRC32(s string) uint32 { return crc32.ChecksumIEEE([]byte(s)) }

type compactionMarkerScan struct {
	Trigger              string
	MarkerCount          int
	MarkerLineHash       uint32
	LatestMarkerPrefix   string
	LatestMarkerSuffix   string
	LatestMarkerSuffixID compactionSuffixIdentity
	MarkerPrefixHash     uint32
	MarkerPrefixLines    int
}

func compactionTrigger(runtime, content string) string {
	return compactionTriggerScan(runtime, content).Trigger
}

func compactionTriggerScan(runtime, content string) compactionMarkerScan {
	switch agentruntime.Normalize(runtime) {
	case agentruntime.Claude:
		return claudeCompactionTriggerScan(content)
	case agentruntime.Codex:
		return codexCompactionTriggerScan(content)
	default:
		return compactionMarkerScan{}
	}
}

func claudeCompactionTriggerScan(content string) compactionMarkerScan {
	return scanCompactionMarkers(content, agentruntime.Claude+":conversation-compaction", isClaudeCompactionLine)
}

func isClaudeCompactionLine(line string) bool {
	normalized := normalizeStatusLine(line)
	return strings.HasPrefix(normalized, "conversation compacted") ||
		strings.HasPrefix(normalized, "compacted (ctrl+o")
}

func codexCompactionTriggerScan(content string) compactionMarkerScan {
	return scanCompactionMarkers(content, agentruntime.Codex+":context-compaction", isCodexCompactionLine)
}

func scanCompactionMarkers(content, trigger string, isMarker func(string) bool) compactionMarkerScan {
	markers := 0
	latestMarkerEnd := -1
	latestMarkerLine := 0
	latestMarkerHash := uint32(0)
	lineNumber := 0
	for lineStart := 0; lineStart <= len(content); {
		lineEnd := strings.IndexByte(content[lineStart:], '\n')
		nextLineStart := len(content) + 1
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += lineStart
			nextLineStart = lineEnd + 1
		}

		line := content[lineStart:lineEnd]
		if isMarker(line) {
			markers++
			latestMarkerEnd = lineEnd
			latestMarkerLine = lineNumber
			latestMarkerHash = hashContentCRC32(normalizeStatusLine(line))
		}

		lineNumber++
		lineStart = nextLineStart
	}
	if markers == 0 {
		return compactionMarkerScan{}
	}
	suffix := compactionSuffix(content, latestMarkerEnd)
	return compactionMarkerScan{
		Trigger:              trigger,
		MarkerCount:          markers,
		MarkerLineHash:       latestMarkerHash,
		LatestMarkerPrefix:   compactionPrefixTail(content, latestMarkerEnd),
		LatestMarkerSuffix:   suffix,
		LatestMarkerSuffixID: compactionSuffixID(suffix),
		MarkerPrefixHash:     hashContentCRC32(content[:latestMarkerEnd]),
		MarkerPrefixLines:    latestMarkerLine + 1,
	}
}

func compactionPrefixTail(content string, end int) string {
	if end <= 0 {
		return ""
	}
	start := 0
	if end > maxCompactionPrefixTailBytes {
		start = end - maxCompactionPrefixTailBytes
	}
	return strings.Clone(content[start:end])
}

func compactionSuffix(content string, start int) string {
	if start >= len(content) {
		return ""
	}
	return content[start:]
}

func compactionSuffixID(suffix string) compactionSuffixIdentity {
	id := compactionSuffixIdentity{
		Length: len(suffix),
		Hash:   sha256.Sum256([]byte(suffix)),
	}
	if suffix == "" {
		return id
	}
	headEnd := len(suffix)
	if headEnd > maxCompactionSuffixWindowBytes {
		headEnd = maxCompactionSuffixWindowBytes
	}
	tailStart := 0
	if len(suffix) > maxCompactionSuffixWindowBytes {
		tailStart = len(suffix) - maxCompactionSuffixWindowBytes
	}
	id.Head = strings.Clone(suffix[:headEnd])
	id.Tail = strings.Clone(suffix[tailStart:])
	return id
}

func isCodexCompactionLine(line string) bool {
	return normalizeStatusLine(line) == "context compacted"
}

func normalizeStatusLine(line string) string {
	normalized := strings.ToLower(strings.TrimSpace(line))
	normalized = strings.TrimLeftFunc(normalized, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	return strings.TrimSpace(normalized)
}

// DetectCompaction reports a known runtime marker without observing processes.
func DetectCompaction(runtime, content string) string { return compactionTrigger(runtime, content) }

func containsCompactionTrigger(runtime, content string) bool {
	return DetectCompaction(runtime, content) != ""
}
