package verdictbackfill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectGeneralizesVerdictMarkersAcrossNodePairs(t *testing.T) {
	sessionDir := t.TempDir()
	readDir := filepath.Join(sessionDir, "read")
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}

	writeArchive(t, readDir, "m1.md", "orchestrator", "critic", "thread-1", "2026-07-13T10:00:00Z", "APPROVED: looks good\n")
	writeArchive(t, readDir, "m2.md", "worker", "orchestrator", "thread-2", "2026-07-13T10:00:01Z", "BLOCKED: missing evidence\n")
	writeArchive(t, readDir, "m3.md", "guardian", "messenger", "thread-3", "2026-07-13T10:00:02Z", "DONE: shipped\n")
	writeArchive(t, readDir, "m4.md", "critic", "worker-alt", "thread-4", "2026-07-13T10:00:03Z", "NOT APPROVED: rework needed\n")
	writeArchive(t, readDir, "ignored.md", "worker", "messenger", "thread-5", "2026-07-13T10:00:04Z", "status update\n")

	rows, err := Collect(Options{SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("Collect() rows = %d, want 4: %#v", len(rows), rows)
	}

	want := []struct {
		from    string
		to      string
		marker  string
		verdict string
	}{
		{"orchestrator", "critic", "PASS", "pass"},
		{"worker", "orchestrator", "BLOCKED", "blocked"},
		{"guardian", "messenger", "DONE", "done"},
		{"critic", "worker-alt", "NOT APPROVED", "not_approved"},
	}
	for i, row := range rows {
		if row.EventType != EventType {
			t.Fatalf("row[%d].EventType = %q, want %q", i, row.EventType, EventType)
		}
		if row.Source != Source {
			t.Fatalf("row[%d].Source = %q, want %q", i, row.Source, Source)
		}
		if row.FromNode != want[i].from || row.ToNode != want[i].to {
			t.Fatalf("row[%d] node pair = %s -> %s, want %s -> %s", i, row.FromNode, row.ToNode, want[i].from, want[i].to)
		}
		if row.Marker != want[i].marker || row.Verdict != want[i].verdict {
			t.Fatalf("row[%d] marker/verdict = %q/%q, want %q/%q", i, row.Marker, row.Verdict, want[i].marker, want[i].verdict)
		}
		if row.Evidence != "" {
			t.Fatalf("row[%d].Evidence = %q, want explicit empty", i, row.Evidence)
		}
		for name, value := range map[string]string{
			"model":               row.Model,
			"instruction_version": row.InstructionVersion,
			"runtime_context_id":  row.RuntimeContextID,
		} {
			if value != UnknownIdentity {
				t.Fatalf("row[%d].%s = %q, want %q", i, name, value, UnknownIdentity)
			}
		}
	}
}

func TestCollectIsIdempotent(t *testing.T) {
	sessionDir := t.TempDir()
	readDir := filepath.Join(sessionDir, "read")
	if err := os.MkdirAll(readDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	writeArchive(t, readDir, "m1.md", "worker", "guardian", "thread-1", "2026-07-13T10:00:00Z", "PASS: verified\n")

	first, err := Collect(Options{SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}
	second, err := Collect(Options{SessionDir: sessionDir})
	if err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("row counts = %d/%d, want 1/1", len(first), len(second))
	}
	if first[0].EventID == "" {
		t.Fatal("EventID is empty")
	}
	if first[0].EventID != second[0].EventID {
		t.Fatalf("EventID changed across runs: %q != %q", first[0].EventID, second[0].EventID)
	}
}

func writeArchive(t *testing.T, dir, name, from, to, threadID, timestamp, body string) {
	t.Helper()
	content := "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + name + "\n" +
		"  thread_id: " + threadID + "\n" +
		"  timestamp: " + timestamp + "\n" +
		"---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}
