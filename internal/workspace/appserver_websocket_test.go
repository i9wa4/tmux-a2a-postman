package workspace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeCodexServer(t *testing.T, handle func(net.Conn, *bufio.Reader, int64)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "codexws-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "app.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		key := req.Header.Get("Sec-WebSocket-Key")
		accept := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(accept[:])+"\r\n\r\n")
		if err != nil {
			return
		}
		frame, err := readMaskedTestFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(frame, &request) != nil {
			return
		}
		handle(conn, reader, request.ID)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fake app server did not exit")
		}
	})
	return path
}

func readMaskedTestFrame(reader *bufio.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if header[0] != 0x81 || header[1]&0x80 == 0 {
		return nil, errors.New("client frame is not masked text")
	}
	size := int(header[1] & 0x7f)
	if size == 126 {
		var extra [2]byte
		if _, err := io.ReadFull(reader, extra[:]); err != nil {
			return nil, err
		}
		size = int(binary.BigEndian.Uint16(extra[:]))
	}
	if size > maxCodexFrame {
		return nil, errors.New("frame too large")
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}

func writeTestFrame(conn net.Conn, payload []byte) error {
	header := []byte{0x81, byte(len(payload))}
	if len(payload) >= 126 {
		header = []byte{0x81, 126, 0, 0}
		binary.BigEndian.PutUint16(header[2:], uint16(len(payload)))
	}
	if err := writeAll(conn, header); err != nil {
		return err
	}
	return writeAll(conn, payload)
}

func TestCodexSocketCorrelatesResponseAndSeparatesNotifications(t *testing.T) {
	path := fakeCodexServer(t, func(conn net.Conn, _ *bufio.Reader, id int64) {
		_ = writeTestFrame(conn, []byte(`{"method":"thread/started","params":{"threadId":"thread-1"}}`))
		reply, _ := json.Marshal(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-1"}}})
		_ = writeTestFrame(conn, reply)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	socket, err := dialCodexSocket(ctx, path, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	result, notices, err := socket.call(ctx, "thread/start", map[string]any{"approvalPolicy": "never"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Method != "thread/started" {
		t.Fatalf("unexpected notifications: %+v", notices)
	}
	if string(result) != `{"thread":{"id":"thread-1"}}` {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestCodexSocketRejectsWrongResponseID(t *testing.T) {
	path := fakeCodexServer(t, func(conn net.Conn, _ *bufio.Reader, id int64) {
		reply, _ := json.Marshal(map[string]any{"id": id + 1, "result": map[string]any{}})
		_ = writeTestFrame(conn, reply)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	socket, err := dialCodexSocket(ctx, path, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	_, _, err = socket.call(ctx, "thread/start", map[string]any{})
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("wrong ID should be unknown: %v", err)
	}
}

func TestCodexSocketRejectsWrongPeerPID(t *testing.T) {
	path := fakeCodexServer(t, func(net.Conn, *bufio.Reader, int64) {})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := dialCodexSocket(ctx, path, os.Getpid()+1)
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("wrong peer should be unknown: %v", err)
	}
}

// TestCodexProtocolBarePop confirms the handshake and that the wake input
// contains no positional mail ID. Correlation belongs to durable state.
func TestCodexProtocolBarePop(t *testing.T) {
	prompt := make(chan string, 1)
	path := fakeCodexServer(t, func(conn net.Conn, reader *bufio.Reader, initID int64) {
		initReply, _ := json.Marshal(map[string]any{"id": initID, "result": map[string]any{}})
		if writeTestFrame(conn, initReply) != nil {
			return
		}
		initialized, err := readMaskedTestFrame(reader)
		if err != nil {
			return
		}
		var note struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(initialized, &note) != nil || note.Method != "initialized" {
			return
		}
		threadFrame, err := readMaskedTestFrame(reader)
		if err != nil {
			return
		}
		var threadRequest struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(threadFrame, &threadRequest) != nil || threadRequest.Method != "thread/start" {
			return
		}
		threadReply, _ := json.Marshal(map[string]any{
			"id":     threadRequest.ID,
			"result": map[string]any{"thread": map[string]any{"id": "thr-1"}},
		})
		if writeTestFrame(conn, threadReply) != nil {
			return
		}
		turnFrame, err := readMaskedTestFrame(reader)
		if err != nil {
			return
		}
		var turnRequest struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				ThreadID string `json:"threadId"`
				Input    []struct {
					Text string `json:"text"`
				} `json:"input"`
			} `json:"params"`
		}
		if json.Unmarshal(turnFrame, &turnRequest) != nil || turnRequest.Method != "turn/start" || turnRequest.Params.ThreadID != "thr-1" || len(turnRequest.Params.Input) != 1 {
			return
		}
		prompt <- turnRequest.Params.Input[0].Text
		turnReply, _ := json.Marshal(map[string]any{
			"id":     turnRequest.ID,
			"result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}},
		})
		_ = writeTestFrame(conn, turnReply)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	socket, err := dialCodexSocket(ctx, path, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	if err := socket.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	threadID, err := socket.startThread(ctx, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	turnID, err := socket.startBarePopTurn(ctx, threadID, "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if turnID != "turn-1" {
		t.Fatalf("unexpected turn ID: %s", turnID)
	}
	select {
	case got := <-prompt:
		want := "tmux-a2a-postman pop"
		if got != want {
			t.Fatalf("wake prompt mismatch: %q", got)
		}
	default:
		t.Fatal("missing wake prompt")
	}
}

func TestCodexSocketCapsNotificationCountAndBytes(t *testing.T) {
	cases := []struct {
		name    string
		frame   []byte
		repeats int
	}{
		{"count", []byte(`{"method":"thread/started","params":{}}`), maxCodexRPCNotifications + 1},
		{"bytes", append([]byte(`{"method":"progress","params":{"pad":"`), append(bytes.Repeat([]byte("x"), 40<<10), []byte(`"}}`)...)...), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := fakeCodexServer(t, func(conn net.Conn, _ *bufio.Reader, _ int64) {
				for i := 0; i < tc.repeats; i++ {
					if writeTestFrame(conn, tc.frame) != nil {
						return
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			defer cancel()
			socket, err := dialCodexSocket(ctx, path, os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			defer socket.close()
			_, notices, err := socket.call(ctx, "thread/start", map[string]any{})
			wantAccepted := maxCodexRPCNotifications
			if tc.name == "bytes" {
				wantAccepted = 1
			}
			if !errors.Is(err, ErrUnknown) || !strings.Contains(err.Error(), "notification budget exceeded") || len(notices) != wantAccepted {
				t.Fatalf("notification budget was not enforced: accepted=%d want=%d err=%v", len(notices), wantAccepted, err)
			}
		})
	}
}

func TestCodexSocketSilentPeerHasIndependentRPCDeadline(t *testing.T) {
	path := fakeCodexServer(t, func(conn net.Conn, _ *bufio.Reader, _ int64) {
		_, _ = io.Copy(io.Discard, conn)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	socket, err := dialCodexSocket(ctx, path, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer socket.close()
	start := time.Now()
	_, _, err = socket.call(ctx, "thread/start", map[string]any{})
	if !errors.Is(err, ErrUnknown) || time.Since(start) > maxCodexRPCDuration+time.Second {
		t.Fatalf("silent peer did not fail closed within independent budget: %v elapsed=%v", err, time.Since(start))
	}
}
