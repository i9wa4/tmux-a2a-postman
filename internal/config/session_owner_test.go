package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func installSessionOwnerTmux(t *testing.T, owners map[string]string) {
	t.Helper()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	var builder strings.Builder
	builder.WriteString("#!/bin/sh\n")
	builder.WriteString("if [ \"$1 $2\" = \"show-options -gqv\" ]; then\n")
	builder.WriteString("  case \"$3\" in\n")

	keys := make([]string, 0, len(owners))
	for key := range owners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString("    @a2a_session_on_" + key + ")\n")
		builder.WriteString("      printf '%s\\n' '" + owners[key] + "'\n")
		builder.WriteString("      exit 0\n")
		builder.WriteString("      ;;\n")
	}

	builder.WriteString("    *)\n")
	builder.WriteString("      exit 0\n")
	builder.WriteString("      ;;\n")
	builder.WriteString("  esac\n")
	builder.WriteString("fi\n")
	builder.WriteString("exit 1\n")

	if err := os.WriteFile(scriptPath, []byte(builder.String()), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}

	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolveContextIDFromSession_IgnoresManagedForeignSessionWithoutEnabledMarker(t *testing.T) {
	baseDir := t.TempDir()
	writeLivePID(t, baseDir, "ctx-owner", "daemon-session")
	if err := os.MkdirAll(filepath.Join(baseDir, "ctx-owner", "managed-session"), 0o755); err != nil {
		t.Fatalf("MkdirAll(managed-session): %v", err)
	}
	installSessionOwnerTmux(t, map[string]string{})

	_, err := ResolveContextIDFromSession(baseDir, "managed-session")
	if err == nil {
		t.Fatal("expected no active postman without enabled-session marker, got nil")
	}
	if !strings.Contains(err.Error(), "no active postman found") {
		t.Fatalf("ResolveContextIDFromSession() error = %q, want no active postman wording", err)
	}
}

func TestResolveContextIDFromSession_UsesEnabledMarkerForManagedForeignSession(t *testing.T) {
	baseDir := t.TempDir()
	writeLivePID(t, baseDir, "ctx-owner", "daemon-session")
	if err := os.MkdirAll(filepath.Join(baseDir, "ctx-owner", "managed-session"), 0o755); err != nil {
		t.Fatalf("MkdirAll(managed-session): %v", err)
	}
	installSessionOwnerTmux(t, map[string]string{
		"managed-session": "ctx-owner:43210",
	})

	got, err := ResolveContextIDFromSession(baseDir, "managed-session")
	if err != nil {
		t.Fatalf("ResolveContextIDFromSession() error = %v", err)
	}
	if got != "ctx-owner" {
		t.Fatalf("ResolveContextIDFromSession() = %q, want %q", got, "ctx-owner")
	}
}

func TestFindSessionOwner_IgnoresLiveForeignContextWithoutEnabledMarker(t *testing.T) {
	baseDir := t.TempDir()
	writeLivePID(t, baseDir, "ctx-owner", "daemon-session")
	if err := os.MkdirAll(filepath.Join(baseDir, "ctx-owner", "managed-session"), 0o755); err != nil {
		t.Fatalf("MkdirAll(managed-session): %v", err)
	}
	installSessionOwnerTmux(t, map[string]string{})

	if got := FindSessionOwner(baseDir, "managed-session", "ctx-self"); got != "" {
		t.Fatalf("FindSessionOwner() = %q, want empty without enabled-session marker", got)
	}
}

func TestContextOwnsSession_LiveDaemonSessionRemainsOwnedWithoutMarker(t *testing.T) {
	baseDir := t.TempDir()
	writeLivePID(t, baseDir, "ctx-owner", "daemon-session")
	installSessionOwnerTmux(t, map[string]string{})

	if !ContextOwnsSession(baseDir, "ctx-owner", "daemon-session") {
		t.Fatal("expected live daemon session to remain owned without explicit marker")
	}
}

func TestContextOwnsSession_IgnoresStaleEnabledMarkerForLiveDaemonSession(t *testing.T) {
	baseDir := t.TempDir()
	writeLivePID(t, baseDir, "ctx-live", "daemon-session")
	installSessionOwnerTmux(t, map[string]string{
		"daemon-session": "ctx-stale:12345",
	})

	if !ContextOwnsSession(baseDir, "ctx-live", "daemon-session") {
		t.Fatal("expected live daemon session to remain owned when enabled marker points to a dead context")
	}
}

func TestResolveContextIDFromSession_IgnoresStaleEnabledMarkerForLiveDaemonSession(t *testing.T) {
	baseDir := t.TempDir()
	writeLivePID(t, baseDir, "ctx-live", "daemon-session")
	installSessionOwnerTmux(t, map[string]string{
		"daemon-session": "ctx-stale:12345",
	})

	got, err := ResolveContextIDFromSession(baseDir, "daemon-session")
	if err != nil {
		t.Fatalf("ResolveContextIDFromSession() error = %v", err)
	}
	if got != "ctx-live" {
		t.Fatalf("ResolveContextIDFromSession() = %q, want %q", got, "ctx-live")
	}
}
