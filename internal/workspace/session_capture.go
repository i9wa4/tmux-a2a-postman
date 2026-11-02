package workspace

// Bounded history encoding shared with the sole persistent server collector.
import (
	"os"
	"strings"
	"unicode/utf8"
)

const (
	SnapshotPaneLimit = 64
	SnapshotLineLimit = 30
	SnapshotTextLimit = 8 * 1024
)

type savedPane struct {
	Version     int    `json:"version"`
	Owner       Owner  `json:"owner"`
	PanePID     string `json:"pane_pid"`
	Window      string `json:"window"`
	CapturedAt  string `json:"captured_at"`
	Fingerprint string `json:"fingerprint"`
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
}

// Publish a complete immutable observation without clobbering an older file.
// Readers of the latest index never see a partially written final record.
func createObservation(dir *os.Root, name string, value any) error {
	temp, err := randomName()
	if err != nil {
		return err
	}
	defer func() {
		if temp != "" {
			_ = dir.Remove(temp)
		}
	}()
	if err := writeJSON(dir, temp, value); err != nil {
		return err
	}
	if err := dir.Link(temp, name); err != nil {
		return err
	}
	if err := dir.Remove(temp); err != nil {
		return err
	}
	temp = ""
	return syncDirectory(dir)
}

func snapshotText(frame paneFrame) (string, bool) {
	lines := strings.Split(frame.Screen, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if frame.CursorY+1 > end && frame.CursorY < len(lines) {
		end = frame.CursorY + 1
	}
	start := 0
	if end > SnapshotLineLimit {
		start = end - SnapshotLineLimit
	}
	text := strings.Join(lines[start:end], "\n")
	truncated := start > 0
	if len(text) > SnapshotTextLimit {
		text = text[len(text)-SnapshotTextLimit:]
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
		truncated = true
	}
	return text, truncated
}
