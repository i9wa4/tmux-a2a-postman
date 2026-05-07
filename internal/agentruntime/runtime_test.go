package agentruntime

import "testing"

func TestRuntimeDefinitions(t *testing.T) {
	if got := Normalize(" Codex "); got != Codex {
		t.Fatalf("Normalize() = %q, want %q", got, Codex)
	}
	if !IsSupported(Claude) || !IsSupported(Codex) {
		t.Fatalf("supported runtimes missing Claude or Codex: %#v", Supported())
	}
	if IsSupported("other") {
		t.Fatal("other should not be a supported runtime")
	}
	def, ok := Lookup(Codex)
	if !ok {
		t.Fatal("Lookup(Codex) failed")
	}
	if def.ProductName != "Codex CLI" {
		t.Fatalf("Codex ProductName = %q, want Codex CLI", def.ProductName)
	}
	if def.ConventionalSkillDir != "$HOME/.codex/skills" {
		t.Fatalf("Codex ConventionalSkillDir = %q, want $HOME/.codex/skills", def.ConventionalSkillDir)
	}
}
