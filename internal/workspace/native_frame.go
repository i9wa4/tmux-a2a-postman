package workspace

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

type nativePane struct {
	Owner  Owner
	PID    string
	Window string
	Linked bool
	Dead   bool
}

// paneFrame attributes text to one native pane at this observation, not to
// an authenticated model or the historical author of terminal scrollback.
type paneFrame struct {
	Pane                            nativePane
	Width, Height, CursorX, CursorY int
	CursorVisible                   bool
	InputMode                       bool
	Screen                          string
}

func (t Tmux) sessionPanes(ctx context.Context, snapshot Snapshot) ([]nativePane, error) {
	out, err := tmuxOutput(ctx, t.Socket, "list-panes", "-s", "-t", snapshot.Caller.Owner.Session, "-F", "#{session_id}\t#{pane_id}\t#{pane_pid}\t#{window_id}\t#{window_linked}\t#{pane_dead}")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var panes []nativePane
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 || fields[0] != snapshot.Caller.Owner.Session || !nativeID(fields[1], '%') || !positivePID(fields[2]) || !nativeID(fields[3], '@') || (fields[4] != "0" && fields[4] != "1") || (fields[5] != "0" && fields[5] != "1") {
			return nil, ErrUnknown
		}
		if seen[fields[1]] {
			return nil, errors.New("pane is linked into multiple selected windows")
		}
		seen[fields[1]] = true
		owner := snapshot.Caller.Owner
		owner.Pane = fields[1]
		panes = append(panes, nativePane{owner, fields[2], fields[3], fields[4] == "1", fields[5] == "1"})
	}
	return panes, nil
}

func (t Tmux) readPaneFrame(ctx context.Context, target nativePane) (paneFrame, error) {
	return t.readFrame(ctx, target, false)
}

func (t Tmux) readPaneHistory(ctx context.Context, target nativePane) (paneFrame, error) {
	return t.readFrame(ctx, target, true)
}

func (t Tmux) readFrame(ctx context.Context, target nativePane, history bool) (paneFrame, error) {
	var frame paneFrame
	if !target.Owner.valid() || !positivePID(target.PID) || target.Linked || target.Dead {
		return frame, ErrUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	nonce, err := randomName()
	if err != nil {
		return frame, err
	}
	prefix := "postman-frame-" + strings.TrimPrefix(nonce, ".new-") + "\t"
	format := prefix + "#{socket_path}\t#{pid}\t#{start_time}\t#{session_id}\t#{pane_id}\t#{pane_pid}\t#{window_id}\t#{window_linked}\t#{pane_dead}\t#{pane_in_mode}\t#{pane_width}\t#{pane_height}\t#{cursor_x}\t#{cursor_y}\t#{cursor_flag}"
	// Native, synchronous commands share one tmux command list. Neither shell
	// execution nor an intervening capture helper can swap targets between them.
	args := []string{"display-message", "-p", "-t", target.Owner.Pane, format, ";", "capture-pane", "-p", "-t", target.Owner.Pane}
	if history {
		args = append(args, "-S", "-30")
	}
	args = append(args, ";", "display-message", "-p", "-t", target.Owner.Pane, format)
	out, err := tmuxOutputLimited(ctx, 60*1024, t.Socket, args...)
	if err != nil {
		return frame, err
	}
	out = strings.TrimSuffix(out, "\n")
	first := strings.IndexByte(out, '\n')
	last := strings.LastIndexByte(out, '\n')
	if first < 0 || last <= first || out[:first] != out[last+1:] || !strings.HasPrefix(out, prefix) {
		return frame, errors.New("pane changed while capturing")
	}
	fields := strings.Split(strings.TrimPrefix(out[:first], prefix), "\t")
	if len(fields) != 15 {
		return frame, ErrUnknown
	}
	server, incarnation, err := serverIdentity(t.Socket, fields[0], fields[1], fields[2])
	if err != nil {
		return frame, err
	}
	owner := Owner{server, incarnation, fields[3], fields[4]}
	if owner != target.Owner || fields[5] != target.PID || fields[7] != "0" || fields[8] != "0" || !nativeID(fields[6], '@') {
		return frame, errors.New("native pane ownership changed")
	}
	if target.Window != "" && target.Window != fields[6] {
		return frame, ErrUnknown
	}
	values := make([]int, 5)
	for i, index := range []int{10, 11, 12, 13, 14} {
		value, err := strconv.Atoi(fields[index])
		if err != nil || value < 0 {
			return frame, ErrUnknown
		}
		values[i] = value
	}
	if values[0] < 1 || values[1] < 1 || values[2] >= values[0] || values[3] >= values[1] || values[4] > 1 || (fields[9] != "0" && fields[9] != "1") {
		return frame, ErrUnknown
	}
	text := out[first+1 : last]
	if !utf8.ValidString(text) {
		return frame, errors.New("pane returned invalid UTF-8")
	}
	frame = paneFrame{Pane: nativePane{owner, fields[5], fields[6], false, false}, Width: values[0], Height: values[1], CursorX: values[2], CursorY: values[3], CursorVisible: values[4] == 1, InputMode: fields[9] == "1", Screen: text}
	if history {
		offset := len(strings.Split(text, "\n")) - frame.Height
		if offset < 0 {
			return paneFrame{}, ErrUnknown
		}
		frame.CursorY += offset
	}
	return frame, nil
}

func (f paneFrame) beforeCursor() string {
	lines := strings.Split(f.Screen, "\n")
	if f.CursorY < 0 || f.CursorX < 0 || f.CursorY >= len(lines) {
		return ""
	}
	row := runewidth.Truncate(lines[f.CursorY], f.CursorX, "")
	return strings.Join(append(append([]string{}, lines[:f.CursorY]...), row), "\n")
}

func (f paneFrame) cursorPrefix() string {
	lines := strings.Split(f.Screen, "\n")
	if f.CursorY < 0 || f.CursorX < 0 || f.CursorY >= len(lines) {
		return ""
	}
	return runewidth.Truncate(lines[f.CursorY], f.CursorX, "")
}

func (f paneFrame) sameGeometry(other paneFrame) bool {
	return f.Pane.Owner == other.Pane.Owner && f.Pane.PID == other.Pane.PID && f.Width == other.Width && f.Height == other.Height
}

func packedScreen(text string) string { return strings.Join(strings.Fields(text), "") }

func (t Tmux) noticeFrame(ctx context.Context, target Binding) (paneFrame, error) {
	frame, err := t.readPaneFrame(ctx, nativePane{Owner: target.Owner, PID: target.PanePID})
	if err != nil {
		return frame, err
	}
	if frame.InputMode || !frame.CursorVisible {
		return frame, fmt.Errorf("%w: recipient editor cannot be observed", ErrUnknown)
	}
	return frame, nil
}
