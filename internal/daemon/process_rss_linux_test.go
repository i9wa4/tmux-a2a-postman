//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadStatmRSSBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statm")
	if err := os.WriteFile(path, []byte("100 7 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bytes, err := readStatmRSSBytes(path, 4096)
	if err != nil {
		t.Fatalf("readStatmRSSBytes: %v", err)
	}
	if bytes != 7*4096 {
		t.Fatalf("bytes = %d, want %d", bytes, 7*4096)
	}
}

func TestReadStatmRSSBytesRejectsMissingResidentField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statm")
	if err := os.WriteFile(path, []byte("100\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := readStatmRSSBytes(path, 4096); err == nil {
		t.Fatal("readStatmRSSBytes err = nil, want error")
	}
}
