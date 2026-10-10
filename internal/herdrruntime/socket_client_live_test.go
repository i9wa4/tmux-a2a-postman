//go:build herdr_live

package herdrruntime_test

import (
	"context"
	"os"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/herdrruntime"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

// Run with:
// HERDR_SOCKET_PATH=/path/to/herdr.sock HERDR_PANE_ID=pane-id go test -tags herdr_live ./internal/herdrruntime -run TestSocketClientLiveSmoke -count=1
func TestSocketClientLiveSmoke(t *testing.T) {
	socketPath := os.Getenv("HERDR_SOCKET_PATH")
	if socketPath == "" {
		t.Fatal("HERDR_SOCKET_PATH is required for herdr_live smoke coverage")
	}
	client, err := herdrruntime.NewSocketClient(config.HerdrConfig{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewSocketClient() error = %v", err)
	}
	envelope, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if envelope.ProtocolVersion == "" || envelope.SchemaVersion == 0 {
		t.Fatalf("Ping() envelope = %#v, want protocol/schema compatibility evidence", envelope)
	}
	snapshot, err := client.SessionSnapshot(context.Background())
	if err != nil {
		t.Fatalf("SessionSnapshot() error = %v", err)
	}
	if len(snapshot.Workspaces) == 0 || len(snapshot.Tabs) == 0 || len(snapshot.Panes) == 0 {
		t.Fatalf("SessionSnapshot() = %#v, want non-empty workspace/tab/pane inventory", snapshot)
	}
	paneID := os.Getenv("HERDR_PANE_ID")
	if paneID == "" {
		t.Fatal("HERDR_PANE_ID is required for pane read/process live smoke coverage")
	}
	read, err := client.ReadPane(context.Background(), paneID, multiplexer.HerdrPaneReadOptions{Source: "recent", TailLines: 1})
	if err != nil {
		t.Fatalf("ReadPane() error = %v", err)
	}
	if read.Envelope != envelope {
		t.Fatalf("ReadPane() envelope = %#v, want %#v", read.Envelope, envelope)
	}
	process, err := client.PaneProcessInfo(context.Background(), paneID)
	if err != nil {
		t.Fatalf("PaneProcessInfo() error = %v", err)
	}
	if process.ProcessInfo.PaneID != paneID {
		t.Fatalf("PaneProcessInfo().PaneID = %q, want %q", process.ProcessInfo.PaneID, paneID)
	}
}
