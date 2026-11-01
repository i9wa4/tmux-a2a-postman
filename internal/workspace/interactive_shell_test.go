package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInteractiveShellInitializesAliasesFunctionsAndStreams(t *testing.T) {
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			shell, err := exec.LookPath(name)
			if err != nil {
				t.Skip(name + " unavailable")
			}
			home := t.TempDir()
			work := filepath.Join(home, "work")
			if err := os.Mkdir(work, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("ZDOTDIR", home)
			rc := "export POSTMAN_INTERACTIVE_PROOF=initialized\nalias postman_alias='printf ALIAS_OK'\npostman_function() { printf FUNCTION_OK; }\n"
			if name == "bash" {
				rc += "PROMPT_COMMAND='export POSTMAN_PROMPT_PROOF=hook'\nPS1='fixture> '\n"
				err = os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(rc), 0o600)
			} else {
				rc += "precmd() { export POSTMAN_PROMPT_PROOF=hook; }\nPROMPT='fixture> '\n"
				err = os.WriteFile(filepath.Join(home, ".zshrc"), []byte(rc), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			var out, diagnostic bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			invocation := ShellInvocation{Shell: shell, CWD: work, Reason: "test", Source: "printf '%s:%s:%s:' \"$PWD\" \"$POSTMAN_INTERACTIVE_PROOF\" \"$POSTMAN_PROMPT_PROOF\"\npostman_alias\npostman_function\nprintf ERR_ONLY >&2\nexit 7\n"}
			code, started, err := runInteractive(ctx, invocation, &out, &diagnostic)
			if err != nil || !started || code != 7 || out.String() != work+":initialized:hook:ALIAS_OKFUNCTION_OK" || diagnostic.String() != "ERR_ONLY" {
				t.Fatalf("code=%d started=%v err=%v out=%q errstream=%q", code, started, err, out.String(), diagnostic.String())
			}
		})
	}
}

func TestInteractiveShellCancellationDoesNotRunNextSource(t *testing.T) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	marker := filepath.Join(home, "must-not-exist")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var out, diagnostic bytes.Buffer
	_, _, err = runInteractive(ctx, ShellInvocation{Shell: shell, CWD: home, Source: "sleep 3\nprintf bad > " + shellQuote(marker)}, &out, &diagnostic)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cancelled source continued: %v", err)
	}
}

func TestInteractiveShellLoadsApprovedDirenvAndProjectPATH(t *testing.T) {
	direnv, err := exec.LookPath("direnv")
	if err != nil {
		t.Skip("direnv unavailable")
	}
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) {
			shell, err := exec.LookPath(name)
			if err != nil {
				t.Skip(name + " unavailable")
			}
			home := t.TempDir()
			work := filepath.Join(home, "work")
			bin := filepath.Join(work, "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"HOME": home, "ZDOTDIR": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_CACHE_HOME": filepath.Join(home, "cache"), "DIRENV_DIR": "", "DIRENV_DIFF": "", "DIRENV_WATCHES": ""} {
				t.Setenv(key, value)
			}
			if err := os.WriteFile(filepath.Join(work, ".envrc"), []byte("export POSTMAN_PROJECT_ENV=ENVRC_OK\nPATH_add bin\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "project-proof"), []byte("#!/bin/sh\nprintf PROJECT_PATH_OK"), 0o700); err != nil {
				t.Fatal(err)
			}
			rc := "eval \"$(" + shellQuote(direnv) + " hook " + name + ")\"\n"
			file := ".zshrc"
			if name == "bash" {
				file = ".bash_profile"
			}
			if err := os.WriteFile(filepath.Join(home, file), []byte(rc), 0o600); err != nil {
				t.Fatal(err)
			}
			allow := exec.Command(direnv, "allow", work)
			allow.Dir = work
			if out, err := allow.CombinedOutput(); err != nil {
				t.Fatalf("direnv allow in owned fixture: %v %s", err, out)
			}
			var out, diagnostic bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			code, started, err := runInteractive(ctx, ShellInvocation{Shell: shell, CWD: work, Source: "printf '%s:' \"$POSTMAN_PROJECT_ENV\"; project-proof"}, &out, &diagnostic)
			if err != nil || !started || code != 0 || out.String() != "ENVRC_OK:PROJECT_PATH_OK" {
				t.Fatalf("shell=%s code=%d started=%v err=%v out=%q stderr=%q", name, code, started, err, out.String(), diagnostic.String())
			}
		})
	}
}
