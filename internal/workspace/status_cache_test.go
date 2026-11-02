package workspace

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStatusContextPreservesSocketMetacharacters(t *testing.T) {
	socket := "/tmp/sp ace,'$()\"/s"
	got, err := ParseServerContext(socket + ",123,0")
	if err != nil || got.Socket != socket || got.PID != 123 {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"", "relative,123,0", "/tmp/a,-1,0", "/tmp/a,pid,0", "/tmp/a,1,no", "/tmp/../tmp/a,1,0"} {
		if _, err := ParseServerContext(raw); err == nil {
			t.Fatal("invalid context", raw)
		}
	}
}

func TestStatusCacheRejectsMalformedIncompatibleAndUnsafeRecords(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	snapshot := refreshFixture(t, m, f, time.Now().UTC())
	server := ServerContext{Socket: f.socket}
	original, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	invalid := [][]byte{[]byte("{}"), []byte("null"), []byte("{"), append(append([]byte{}, original...), []byte("{}")...), []byte(strings.Replace(string(original), `"version":1`, `"version":2`, 1)), []byte(strings.Replace(string(original), `"version":1`, `"version":1,"version":1`, 1)), []byte(strings.Replace(string(original), `"id":"$0"`, `"id":"$999999999999999999999999"`, 1))}
	for _, raw := range invalid {
		if _, err := decodeStatus(raw, server); err == nil {
			t.Fatal("malformed cache accepted", string(raw))
		}
	}
	dir, err := m.Store.statusDir(server, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Chmod("status.json", 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Store.ReadServerStatus(server); err == nil {
		t.Fatal("public snapshot accepted")
	}
	if err := dir.Chmod("status.json", 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir() + "/outside"
	if err := os.WriteFile(outside, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dir.Remove("status.json"); err != nil {
		t.Fatal(err)
	}
	if err := dir.Symlink(outside, "status.json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Store.ReadServerStatus(server); err == nil {
		t.Fatal("symlink cache accepted")
	}
	// A dangerous snapshot file does not become a healthy empty state, nor does
	// the reader initialize missing state as a recovery action.
}
