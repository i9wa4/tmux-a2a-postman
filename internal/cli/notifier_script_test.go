package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifierSetupDoesNotRegisterWhenBindingFails(t *testing.T) {
	bash := os.Getenv("POSTMAN_TEST_BASH")
	if bash == "" {
		var err error
		bash, err = exec.LookPath("bash")
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	setup := filepath.Join(dir, "setup.bash")
	binary := filepath.Join(dir, "postman")
	config := filepath.Join(dir, "postman.md")
	marker := filepath.Join(dir, "registered")
	for path, data := range map[string]string{
		setup:  notifierScript,
		binary: "#!/bin/sh\nprintf registered > \"$POSTMAN_TEST_REGISTRATION_MARKER\"\n",
		config: "fixture topology\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Disable the builtin and exclude external commands: the key binding must
	// fail even with an interactive Bash and otherwise valid recipient paths.
	cmd := exec.Command(bash, "--noprofile", "--norc", "-i", "-c", `enable -n bind; source "$1"`, "--", setup)
	cmd.Env = append(os.Environ(), "PATH="+dir, "POSTMAN_BIN="+binary,
		"POSTMAN_HANDLER="+binary, "POSTMAN_CONFIG="+config,
		"POSTMAN_TEST_REGISTRATION_MARKER="+marker)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "bind") {
		t.Fatalf("setup did not report the failed binding: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("registered a nonfunctional notification hook: %v", err)
	}
}
