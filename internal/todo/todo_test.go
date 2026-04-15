package todo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDocument_CountsCheckboxesAndInvalidSyntax(t *testing.T) {
	summary := ParseDocument("# TODO\n\n- [ ] first\n- [x] second\n- [y] invalid\n")
	if !summary.Invalid {
		t.Fatal("ParseDocument().Invalid = false, want true")
	}
	if summary.Checked != 1 {
		t.Fatalf("ParseDocument().Checked = %d, want 1", summary.Checked)
	}
	if summary.Total != 2 {
		t.Fatalf("ParseDocument().Total = %d, want 2", summary.Total)
	}
}

func TestWriteOwnerFile_RejectsCrossNodeWrite(t *testing.T) {
	sessionDir := t.TempDir()
	err := WriteOwnerFile(sessionDir, "worker", "orchestrator", "# TODO\n")
	if err == nil {
		t.Fatal("WriteOwnerFile() error = nil, want owner-only rejection")
	}
}

func TestSummaries_IncludeConfiguredNodesAndExtraFiles(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sessionDir, "todo"), 0o700); err != nil {
		t.Fatalf("MkdirAll(todo): %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "todo", "worker.md"), []byte("- [x] shipped\n- [ ] follow-up\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(worker.md): %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "todo", "reviewer.md"), []byte("- [x] approved\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(reviewer.md): %v", err)
	}

	summaries, err := Summaries(sessionDir, []string{"messenger", "worker"})
	if err != nil {
		t.Fatalf("Summaries(): %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("Summaries() len = %d, want 3", len(summaries))
	}
	if summaries[0].Node != "messenger" || summaries[0].Token() != "[·]" {
		t.Fatalf("summaries[0] = %+v, want messenger empty token", summaries[0])
	}
	if summaries[1].Node != "worker" || summaries[1].Token() != "[-]" || summaries[1].Checked != 1 || summaries[1].Total != 2 {
		t.Fatalf("summaries[1] = %+v, want worker partial 1/2", summaries[1])
	}
	if summaries[2].Node != "reviewer" || summaries[2].Token() != "[x]" || summaries[2].Checked != 1 || summaries[2].Total != 1 {
		t.Fatalf("summaries[2] = %+v, want reviewer done 1/1", summaries[2])
	}
}

func TestReadFile_RejectsInvalidNodeName(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sessionDir, "read"), 0o700); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "read", "secret.md"), []byte("secret-from-read\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret.md): %v", err)
	}

	for _, node := range []string{"../read/secret", "worker!"} {
		_, err := ReadFile(sessionDir, node)
		if err == nil {
			t.Fatalf("ReadFile(%q) error = nil, want invalid-name rejection", node)
		}
		if !strings.Contains(err.Error(), "invalid todo node name") {
			t.Fatalf("ReadFile(%q) error = %q, want invalid todo node name", node, err.Error())
		}
	}
}
