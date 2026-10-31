package workspace

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// codexSocket is a short-lived client to a recipient-owned Codex app-server.
// It never owns or starts the server and never treats an RPC as a POP receipt.
type codexSocket struct {
	conn   net.Conn
	read   *bufio.Reader
	nextID int64
}

const (
	maxCodexFrame            = 1 << 20
	maxCodexRPCNotifications = 32
	maxCodexRPCNoticeBytes   = 64 << 10
	maxCodexRPCDuration      = 2 * time.Second
)

func connectCodexSocket(ctx context.Context, socketPath string, panePID int) (*codexSocket, error) {
	if socketPath == "" || panePID <= 0 {
		return nil, ErrUnknown
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*codexSocket, error) { _ = conn.Close(); return nil, err }
	pid, err := unixPeerPID(conn)
	if err != nil || pid != panePID {
		return fail(fmt.Errorf("%w: app-server peer process does not own recipient pane", ErrUnknown))
	}
	if err := conn.SetDeadline(deadlineFromContext(ctx)); err != nil {
		return fail(err)
	}
	return &codexSocket{conn: conn, read: bufio.NewReader(conn)}, nil
}

// dialCodexSocket is used by protocol tests; production marks rpc_started
// between connectCodexSocket and upgrade before the first socket write.
func dialCodexSocket(ctx context.Context, socketPath string, panePID int) (*codexSocket, error) {
	socket, err := connectCodexSocket(ctx, socketPath, panePID)
	if err != nil {
		return nil, err
	}
	if err := socket.upgrade(); err != nil {
		socket.close()
		return nil, err
	}
	return socket, nil
}

func (s *codexSocket) upgrade() error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	request := "GET / HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"
	if err := writeAll(s.conn, []byte(request)); err != nil {
		return err
	}
	resp, err := http.ReadResponse(s.read, &http.Request{Method: http.MethodGet})
	if err != nil {
		return err
	}
	accept := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	want := base64.StdEncoding.EncodeToString(accept[:])
	if resp.StatusCode != http.StatusSwitchingProtocols ||
		!strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") ||
		!headerHasToken(resp.Header.Get("Connection"), "upgrade") ||
		resp.Header.Get("Sec-WebSocket-Accept") != want {
		_ = resp.Body.Close()
		return fmt.Errorf("%w: invalid app-server WebSocket upgrade", ErrUnknown)
	}
	return nil
}

func headerHasToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func deadlineFromContext(ctx context.Context) time.Time {
	limit := time.Now().Add(maxCodexRPCDuration)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(limit) {
		return deadline
	}
	return limit
}

func (s *codexSocket) close() { _ = s.conn.Close() }

// writeFrame masks every client frame; app-server messages are JSON text.
func (s *codexSocket) writeFrame(opcode byte, payload []byte) error {
	if len(payload) > maxCodexFrame {
		return ErrUnknown
	}
	var header [14]byte
	header[0] = 0x80 | opcode
	n := 2
	switch {
	case len(payload) < 126:
		header[1] = 0x80 | byte(len(payload))
	case len(payload) <= 0xffff:
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:4], uint16(len(payload)))
		n = 4
	default:
		header[1] = 0x80 | 127
		binary.BigEndian.PutUint64(header[2:10], uint64(len(payload)))
		n = 10
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(header[n:], mask[:])
	n += 4
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if err := writeAll(s.conn, header[:n]); err != nil {
		return err
	}
	return writeAll(s.conn, masked)
}

func writeAll(conn net.Conn, payload []byte) error {
	for len(payload) != 0 {
		n, err := conn.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func (s *codexSocket) readFrame() (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(s.read, header[:]); err != nil {
		return 0, nil, err
	}
	if header[0]&0x80 == 0 || header[0]&0x70 != 0 || header[1]&0x80 != 0 {
		return 0, nil, fmt.Errorf("%w: unsupported or masked server frame", ErrUnknown)
	}
	size := uint64(header[1] & 0x7f)
	switch size {
	case 126:
		var extra [2]byte
		if _, err := io.ReadFull(s.read, extra[:]); err != nil {
			return 0, nil, err
		}
		size = uint64(binary.BigEndian.Uint16(extra[:]))
	case 127:
		var extra [8]byte
		if _, err := io.ReadFull(s.read, extra[:]); err != nil {
			return 0, nil, err
		}
		size = binary.BigEndian.Uint64(extra[:])
	}
	if size > maxCodexFrame {
		return 0, nil, ErrUnknown
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(s.read, payload); err != nil {
		return 0, nil, err
	}
	return header[0] & 0x0f, payload, nil
}

type codexRPC struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// call matches the exact request ID. Notifications remain separate.
// The official Codex app-server wire format omits the jsonrpc member.
func (s *codexSocket) call(ctx context.Context, method string, params any) (json.RawMessage, []codexRPC, error) {
	if err := s.conn.SetDeadline(deadlineFromContext(ctx)); err != nil {
		return nil, nil, err
	}
	s.nextID++
	id := s.nextID
	request, err := json.Marshal(struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{id, method, params})
	if err != nil {
		return nil, nil, err
	}
	if err := s.writeFrame(1, request); err != nil {
		return nil, nil, err
	}
	var notices []codexRPC
	noticeBytes := 0
	for {
		opcode, payload, err := s.readFrame()
		if err != nil {
			return nil, notices, fmt.Errorf("%w: app-server read: %v", ErrUnknown, err)
		}
		switch opcode {
		case 1:
			var event codexRPC
			if err := json.Unmarshal(payload, &event); err != nil {
				return nil, notices, err
			}
			if len(event.ID) == 0 {
				if event.Method == "" {
					return nil, notices, ErrUnknown
				}
				noticeBytes += len(payload)
				if len(notices) >= maxCodexRPCNotifications || noticeBytes > maxCodexRPCNoticeBytes {
					return nil, notices, fmt.Errorf("%w: app-server notification budget exceeded", ErrUnknown)
				}
				notices = append(notices, event)
				continue
			}
			var responseID int64
			if err := json.Unmarshal(event.ID, &responseID); err != nil || responseID != id {
				return nil, notices, fmt.Errorf("%w: app-server response ID mismatch", ErrUnknown)
			}
			if len(event.Error) != 0 && string(event.Error) != "null" {
				return nil, notices, fmt.Errorf("%w: app-server %s error: %s", ErrUnknown, method, event.Error)
			}
			if len(event.Result) == 0 {
				return nil, notices, ErrUnknown
			}
			return event.Result, notices, nil
		case 8:
			return nil, notices, io.EOF
		case 9:
			if err := s.writeFrame(10, payload); err != nil {
				return nil, notices, err
			}
		case 10:
			continue
		default:
			return nil, notices, errors.New("unsupported app-server frame opcode " + strconv.Itoa(int(opcode)))
		}
	}
}
