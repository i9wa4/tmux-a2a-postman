package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatusGettersUseIdenticalSnapshotWithoutTmuxOrPersistentDiagnostics(t *testing.T) {
	f := newPublicApprovalFixture(t)
	if _, err := f.run(f.worker, "", "pop", "--config", f.config); err != nil {
		t.Fatal(err)
	}
	before := statusTree(t, f.stateRoot())
	proxy := t.TempDir()
	marker := filepath.Join(proxy, "called")
	if err := os.WriteFile(filepath.Join(proxy, "tmux"), []byte("#!/bin/sh\necho called > '"+marker+"'\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	read := func(command, config string) ([]byte, []byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := f.child(ctx, "", command, "--config", config)
		cmd.Dir = t.TempDir()
		cmd.Env = append(cmd.Env, "PATH="+proxy, "XDG_CONFIG_HOME=relative-and-unused")
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err := cmd.Run()
		return out.Bytes(), diagnostic.Bytes(), err
	}
	raw, diagnostic, err := read("get-status", "/missing/publisher-a.md")
	if err != nil {
		t.Fatal(string(raw), string(diagnostic), err)
	}
	again, diagnostic, err := read("get-status", "relative-and-unused.md")
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal(string(again), string(diagnostic), err)
	}
	line, diagnostic, err := read("get-status-oneline", "/missing/publisher-b.md")
	if err != nil || !strings.HasPrefix(string(line), "[0]") {
		t.Fatal(string(line), string(diagnostic), err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("pure reader called tmux", err)
	}
	if after := statusTree(t, f.stateRoot()); !reflect.DeepEqual(before, after) {
		t.Fatal("getter mutated snapshot/mail/lock/diagnostics")
	}
	// Removing the owned native server does not turn a saved observation into a
	// claim of live identity. Readers retain its exact saved as_of and bytes.
	f.tmux("kill-server")
	old, diagnostic, err := read("get-status", "/still-missing")
	if err != nil || !bytes.Equal(raw, old) {
		t.Fatal(string(old), string(diagnostic), err)
	}
}
