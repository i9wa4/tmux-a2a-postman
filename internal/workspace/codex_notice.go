package workspace

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CodexNotifier identifies an existing Codex conversation. Its app-server is
// used only as a short-lived queue writer: no thread/start, thread/resume or
// turn/start is sent. Codex's own runtime consumes its queue, also in --no-daemon.
type CodexNotifier struct {
	ThreadID string `json:"thread_id"`
	Home     string `json:"codex_home"`
	Program  string `json:"codex_program"`
	Topology string `json:"topology"`
	Root     string `json:"root"`
}

func validThreadID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func (n CodexNotifier) validate() error {
	if !validThreadID(n.ThreadID) || !filepath.IsAbs(n.Home) || !filepath.IsAbs(n.Program) || !filepath.IsAbs(n.Topology) || !filepath.IsAbs(n.Root) || strings.ContainsAny(n.Topology, "\r\n\x00") {
		return errors.New("invalid Codex recipient registration")
	}
	info, err := os.Lstat(n.Home)
	if err != nil || !privateInfo(info, true) {
		return errors.New("codex home must be an existing private directory")
	}
	info, err = os.Stat(n.Program)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("codex executable unavailable")
	}
	return nil
}

// RegisterCodexNotifier must run from the recipient's existing Codex tool
// context, using its CODEX_THREAD_ID and CODEX_HOME. The metadata-only read
// checks that the runtime conversation belongs to this native workspace.
func (s Store) RegisterCodexNotifier(ctx context.Context, rt Runtime, n CodexNotifier) error {
	if err := n.validate(); err != nil {
		return err
	}
	client, err := startCodexQueue(ctx, n)
	if err != nil {
		return err
	}
	defer client.close()
	if err := client.checkThread(ctx, n); err != nil {
		return err
	}
	return s.WithVerified(ctx, rt, func(ws *os.Root, snapshot Snapshot) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		root, err := CanonicalRoot(n.Root, home)
		if err != nil || root != snapshot.Root {
			return errors.New("codex conversation does not match the caller workspace")
		}
		return writeNotifier(ws, snapshot.Caller, notifierRegistration{Version: 1, Binding: snapshot.Caller, Runtime: "codex", Codex: &n})
	})
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type codexQueue struct {
	in        io.WriteCloser
	responses chan rpcResponse
	readErr   chan error
	done      chan error
	cancel    context.CancelFunc
	once      sync.Once
}

func startCodexQueue(ctx context.Context, n CodexNotifier) (*codexQueue, error) {
	if err := n.validate(); err != nil {
		return nil, err
	}
	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, n.Program, "app-server", "--stdio")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+n.Home)
	cmd.Dir = n.Root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Stderr = io.Discard // No model invocation; do not surface credential/runtime logs in mail JSON.
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = in.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	c := &codexQueue{in: in, responses: make(chan rpcResponse), readErr: make(chan error, 1), done: make(chan error, 1), cancel: cancel}
	go func() {
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 4096), 256*1024)
		for scan.Scan() {
			var r rpcResponse
			if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
				c.readErr <- err
				return
			}
			if len(r.ID) == 0 {
				continue
			}
			select {
			case c.responses <- r:
			case <-processCtx.Done():
				return
			}
		}
		err := scan.Err()
		if err == nil {
			err = io.EOF
		}
		c.readErr <- err
	}()
	go func() { c.done <- cmd.Wait() }()
	if err := c.call(ctx, 1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "tmux_a2a_postman", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
		c.close()
		return nil, err
	}
	if err := json.NewEncoder(c.in).Encode(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *codexQueue) close() {
	c.once.Do(func() {
		_ = c.in.Close()
		select {
		case <-c.done:
		case <-time.After(100 * time.Millisecond):
			c.cancel()
			<-c.done
		}
		c.cancel()
	})
}

func (c *codexQueue) call(ctx context.Context, id int, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := json.NewEncoder(c.in).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-c.readErr:
			return fmt.Errorf("codex %s response unavailable: %w", method, err)
		case r := <-c.responses:
			if string(r.ID) != fmt.Sprint(id) {
				return errors.New("codex response ID mismatch")
			}
			if r.Error != nil {
				return fmt.Errorf("codex %s rejected (%d): %s", method, r.Error.Code, r.Error.Message)
			}
			if result == nil {
				return nil
			}
			return json.Unmarshal(r.Result, result)
		}
	}
}

func (c *codexQueue) checkThread(ctx context.Context, n CodexNotifier) error {
	var reply struct {
		Thread struct {
			ID  string `json:"id"`
			Cwd string `json:"cwd"`
		} `json:"thread"`
	}
	if err := c.call(ctx, 2, "thread/read", map[string]any{"threadId": n.ThreadID, "includeTurns": false}, &reply); err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(reply.Thread.Cwd)
	if err != nil || root != n.Root || reply.Thread.ID != n.ThreadID {
		return errors.New("codex thread identity/workspace mismatch")
	}
	return nil
}

func noticePrompt(n CodexNotifier, msg Message) string {
	quoted := "'" + strings.ReplaceAll(n.Topology, "'", "'\\''") + "'"
	return "Postman notification for committed mail " + msg.ID + ". In this existing conversation, run tmux-a2a-postman pop --postman-md " + quoted + " " + msg.ID + ". Read the complete returned markdown_absolute_path through EOF before handling or replying. Follow your role instructions and preserve the exact reply_to / fills_input_request_id correlation. Delivery is already committed; do not resend it."
}

func (c *codexQueue) enqueue(ctx context.Context, n CodexNotifier, msg Message) (string, error) {
	hex := strings.TrimPrefix(msg.ID, "m-")
	clientID := hex[:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:]
	var reply struct {
		Queued struct {
			ID       string `json:"id"`
			ClientID string `json:"clientUserMessageId"`
		} `json:"queuedSubmission"`
	}
	err := c.call(ctx, 3, "thread/queue/add", map[string]any{"threadId": n.ThreadID, "clientUserMessageId": clientID, "input": []map[string]string{{"type": "text", "text": noticePrompt(n, msg)}}}, &reply)
	if err != nil {
		return "", err
	}
	if reply.Queued.ID == "" || reply.Queued.ClientID != clientID {
		return "", errors.New("codex queue receipt correlation mismatch")
	}
	return reply.Queued.ID, nil
}
