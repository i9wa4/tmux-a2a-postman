package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

func TestInstalledHelpAndSelectiveOfflineResourcesNeedNoRuntime(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) ([]byte, []byte, error) {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME=relative-and-unused", "XDG_STATE_HOME=relative-and-unused", "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "TMUX=", "TMUX_PANE=", "TMUX_A2A_POSTMAN_CONFIG=", "PATH="+filepath.Join(home, "no-tools"), "POSTMAN_TEST_PUBLIC_ARGS="+string(raw))
		var out, diagnostic bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostic
		err = cmd.Run()
		return out.Bytes(), diagnostic.Bytes(), err
	}
	before := statusTree(t, home)
	for _, topic := range helpdocs.Topics() {
		out, diagnostic, err := run("help", topic)
		want, readErr := helpdocs.Read(topic)
		if err != nil || readErr != nil || !bytes.HasPrefix(out, want) {
			t.Fatalf("offline help %s: %s %s (%v)", topic, out, diagnostic, err)
		}
	}
	for _, command := range []string{"send-heredoc", "pop", "get-status", "execute-heredoc", "resources"} {
		want, _, err := run("help", command)
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"--help", "-h"} {
			got, diagnostic, err := run(command, flag)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s %s differs: %s %s (%v)", command, flag, got, diagnostic, err)
			}
		}
	}
	if _, diagnostic, err := run("help", "missing-installed-topic"); err == nil || !bytes.Contains(diagnostic, []byte("unknown help topic")) {
		t.Fatal("missing topic lacked an actionable error", string(diagnostic), err)
	}
	for _, ref := range []string{"mailbox", "sending", "status", "session-flow", "external", "quickstart"} {
		want, err := helpdocs.Read("postman-usage", ref)
		if err != nil {
			t.Fatal(err)
		}
		out, diagnostic, err := run("help", "postman-usage", ref)
		if err != nil || !bytes.Equal(out, want) {
			t.Fatal("reference display did not return complete Markdown", ref, string(diagnostic), err)
		}
	}
	for _, args := range [][]string{
		{"skills", "install", "--client", "codex", "--scope", "project", "--project", project, "postman-usage"},
		{"skills", "export", "--to", filepath.Join(home, "export"), "postman-usage"},
		{"skills"},
		{"skills", "--help"},
		{"help", "../topics/overview"},
		{"help", "postman-usage", "../mailbox"},
		{"help", "postman-usage", "missing-reference"},
		{"help", "postman-usage", "mailbox", "extra"},
	} {
		if _, _, err := run(args...); err == nil {
			t.Fatal("unsupported installation or invalid Skill was accepted", args)
		}
	}
	if !reflect.DeepEqual(before, statusTree(t, home)) {
		t.Fatal("help/Skill reads or rejected operations created files")
	}
	out, diagnostic, err := run("resources")
	if err != nil {
		t.Fatal(string(out), string(diagnostic), err)
	}
	resource := strings.TrimSpace(string(out))
	if !filepath.IsAbs(resource) || filepath.Base(resource) != helpdocs.BundleID() {
		t.Fatal("resource path is not a content-identified absolute generation", resource)
	}
	for _, name := range []string{"help/postman-recall/scripts/select_day.py", "help/postman-team-manager/scripts/next_step.py", "help/postman-usage/scripts/analyze-invocations.py", "docs/licenses/postman.txt"} {
		if info, err := os.Stat(filepath.Join(resource, filepath.FromSlash(name))); err != nil || !info.Mode().IsRegular() {
			t.Fatal("bundled resource missing", name, err)
		}
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "postman.md"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Fatal("installation wrote unrelated global instructions/config", name, err)
		}
	}
}
