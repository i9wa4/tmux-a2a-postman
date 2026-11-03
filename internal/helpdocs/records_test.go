package helpdocs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableRecordHelperUsesReservedRootAndLegacyOverride(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("optional helper needs Bash")
	}
	home := t.TempDir()
	cache := filepath.Join(home, "cache")
	state := filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cache)
	resources, err := ResourcePath(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(resources, "help", "postman-records", "scripts", "mkmd")
	cwd := t.TempDir()
	run := func(root, legacy string, args ...string) (string, error) {
		cmd := exec.Command(bash, append([]string{helper}, args...)...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "XDG_STATE_HOME="+state, "POSTMAN_LOGBOOK_ROOT="+root, "MKMD_BASE_DIR="+legacy)
		raw, err := cmd.Output()
		return strings.TrimSpace(string(raw)), err
	}
	path, err := run("", "", "--dir", "plans", "--label", "original")
	if err != nil || !strings.HasPrefix(path, filepath.Join(state, "tmux-a2a-postman", "%logbook")+string(os.PathSeparator)) || !filepath.IsAbs(path) {
		t.Fatal(path, err)
	}
	if err := os.WriteFile(path, []byte("original task and evidence"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, "existing-mkmd")
	next, err := run("", legacy, "--dir", "research", "--label", "different-purpose")
	if err != nil || !strings.HasPrefix(next, legacy+string(os.PathSeparator)) {
		t.Fatal(next, err)
	}
	chosen := filepath.Join(home, "chosen-records")
	explicit, err := run(chosen, legacy, "--dir", "reviews", "--label", "review")
	if err != nil || !strings.HasPrefix(explicit, chosen+string(os.PathSeparator)) {
		t.Fatal(explicit, err)
	}
	for _, args := range [][]string{{"--dir", "../escape", "--label", "x"}, {"--dir", "plans", "--label", "a/b"}} {
		if _, err := run("", "", args...); err == nil {
			t.Fatal("unsafe helper path accepted", args)
		}
	}
	if _, err := run("relative", "", "--dir", "plans", "--label", "x"); err == nil {
		t.Fatal("relative record root accepted")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "original task and evidence" {
		t.Fatal("existing record changed or expired", string(raw), err)
	}
}
