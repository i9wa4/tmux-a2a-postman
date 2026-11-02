package helpdocs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectedOfflineInstallAndIdempotentExistingEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	for _, client := range []string{"claude", "codex"} {
		for _, scope := range []string{"user", "project"} {
			t.Run(client+"-"+scope, func(t *testing.T) {
				opts := InstallOptions{Client: client, Scope: scope, Names: []string{"postman-send-message"}, Version: "git-test"}
				if scope == "project" {
					opts.Project = project
				}
				first, err := Install(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				if len(first.Actions) != 1 || first.Actions[0].Outcome != "installed" {
					t.Fatalf("unexpected install %+v", first)
				}
				if data, err := os.ReadFile(filepath.Join(first.Target, "postman-send-message", "LICENSE")); err != nil || len(data) == 0 {
					t.Fatal("installed Skill is missing its license", err)
				}
				filename := filepath.Join(first.Target, "postman-send-message", "SKILL.md")
				before, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				second, err := Install(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				if len(second.Actions) != 1 || second.Actions[0].Outcome != "already_available" {
					t.Fatalf("unexpected repeat %+v", second)
				}
				after, err := os.ReadFile(filename)
				if err != nil || string(before) != string(after) {
					t.Fatal("repeat changed the installed entry")
				}
				entries, err := os.ReadDir(first.Target)
				if err != nil || len(entries) != 1 {
					t.Fatalf("installed unselected entries %v %v", entries, err)
				}
			})
		}
	}
}

func TestInstallDoesNotShadowLegacyCodexDeclarativeEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".codex", "skills")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("declarative instruction"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(legacy, "postman-usage")); err != nil {
		t.Fatal(err)
	}
	_, err := Install(context.Background(), InstallOptions{Client: "codex", Scope: "user", Names: []string{"postman-send-message", "postman-usage"}})
	if err == nil {
		t.Fatal("installed a duplicate of a declaratively owned Codex Skill")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatal("legacy conflict created a second Skill tree")
	}
	data, err := os.ReadFile(filepath.Join(outside, "SKILL.md"))
	if err != nil || string(data) != "declarative instruction" {
		t.Fatal("changed legacy source", err)
	}
}

func TestInstallPreflightsWholeSelectionAndPreservesCustomAndSymlinkEntries(t *testing.T) {
	for _, fault := range []string{"custom", "symlink", "incomplete", "managed-root"} {
		t.Run(fault, func(t *testing.T) {
			target := t.TempDir()
			name := "postman-usage"
			outside := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(outside, []byte("user content"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "custom":
				if err := os.Mkdir(filepath.Join(target, name), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, name, "SKILL.md"), []byte("custom policy"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Dir(outside), filepath.Join(target, name)); err != nil {
					t.Fatal(err)
				}
			case "incomplete":
				if err := os.Mkdir(filepath.Join(target, name), 0o755); err != nil {
					t.Fatal(err)
				}
			case "managed-root":
				if err := os.WriteFile(filepath.Join(target, ".agent-skills-managed.json"), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Install(context.Background(), InstallOptions{ExportDir: target, Names: []string{"postman-send-message", name}, Version: "v1.0.0"})
			if err == nil {
				t.Fatal("conflicting selection was installed")
			}
			if _, err := os.Lstat(filepath.Join(target, "postman-send-message")); !os.IsNotExist(err) {
				t.Fatal("selection partially installed before reporting conflict")
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != "user content" {
				t.Fatal("unrelated content changed")
			}
		})
	}
}

func TestInstallRejectsUnknownSelectionAndScopeBeforeMutation(t *testing.T) {
	for _, names := range [][]string{nil, {"../escape"}, {"postman-usage", "postman-usage"}} {
		target := filepath.Join(t.TempDir(), "absent")
		if _, err := Install(context.Background(), InstallOptions{ExportDir: target, Names: names}); err == nil {
			t.Fatal("accepted invalid selection")
		}
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatal("invalid selection wrote a target")
		}
	}
	for _, opts := range []InstallOptions{{Client: "unknown", Scope: "user"}, {Client: "claude", Scope: "unknown"}, {Client: "codex", Scope: "project", Project: "relative"}, {ExportDir: "/nix/store/not-a-target"}} {
		opts.Names = []string{"postman-usage"}
		if _, err := Install(context.Background(), opts); err == nil {
			t.Fatal("accepted invalid target")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := filepath.Join(t.TempDir(), "absent")
	if _, err := Install(ctx, InstallOptions{ExportDir: target, Names: []string{"postman-usage"}}); err == nil {
		t.Fatal("cancelled install succeeded")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("cancelled install wrote files")
	}
}

func TestInstallDoesNotFollowTargetOrSkillFileSymlinks(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), InstallOptions{ExportDir: target, Names: []string{"postman-usage"}}); err == nil {
		t.Fatal("followed target symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote through target symlink")
	}
	target = t.TempDir()
	name := filepath.Join(target, "postman-usage")
	if err := os.Mkdir(name, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "user")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(name, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), InstallOptions{ExportDir: target, Names: []string{"postman-usage"}}); err == nil {
		t.Fatal("followed Skill file symlink")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "original" {
		t.Fatal("overwrote symlink target")
	}
}
