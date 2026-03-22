package binding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeToml writes content to a temp file and returns the path.
// mode is the desired file permission (e.g., 0o600).
func writeToml(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bindings.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeToml: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return path
}

// validBinding is a minimal valid TOML binding entry (row 3: assigned, pane_title match).
const validBinding = `
[[binding]]
channel_id        = "ch1"
node_name         = "worker"
context_id        = "ctx1"
session_name      = "my-session"
pane_title        = "worker-pane"
pane_node_name    = ""
active            = true
permitted_senders = ["boss"]
`

func TestLoad_ValidRoundTrip(t *testing.T) {
	path := writeToml(t, validBinding, 0o600)
	reg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reg.Bindings) != 1 {
		t.Fatalf("expected 1 binding, got %d", len(reg.Bindings))
	}
	if reg.Bindings[0].NodeName != "worker" {
		t.Errorf("expected node_name=worker, got %q", reg.Bindings[0].NodeName)
	}
}

// --- Permission tests ---

func TestLoad_WorldReadable(t *testing.T) {
	path := writeToml(t, validBinding, 0o644)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for world-readable file, got nil")
	}
	if !strings.Contains(err.Error(), "world-readable") && !strings.Contains(err.Error(), "group- or world-readable") {
		t.Errorf("expected permission error message, got: %v", err)
	}
}

func TestLoad_GroupReadable(t *testing.T) {
	path := writeToml(t, validBinding, 0o640)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for group-readable file, got nil")
	}
}

// --- Field validation tests ---

func TestLoad_InvalidChannelID(t *testing.T) {
	toml := strings.ReplaceAll(validBinding, `channel_id        = "ch1"`, `channel_id = "ab cd"`)
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid channel_id")
	}
}

func TestLoad_InvalidNodeName(t *testing.T) {
	toml := strings.ReplaceAll(validBinding, `node_name         = "worker"`, `node_name = "bad node"`)
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid node_name")
	}
}

func TestLoad_InvalidContextID(t *testing.T) {
	toml := strings.ReplaceAll(validBinding, `context_id        = "ctx1"`, `context_id = "../etc"`)
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid context_id (path traversal)")
	}
}

func TestLoad_InvalidPaneNodeName(t *testing.T) {
	// pane_node_name validation applies only when non-empty
	base := `
[[binding]]
channel_id        = "ch1"
node_name         = "worker"
context_id        = "ctx1"
session_name      = "my-session"
pane_title        = ""
pane_node_name    = "bad node name"
active            = true
permitted_senders = ["boss"]
`
	path := writeToml(t, base, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid pane_node_name")
	}
}

func TestLoad_InvalidPermittedSender(t *testing.T) {
	toml := strings.ReplaceAll(validBinding, `permitted_senders = ["boss"]`, `permitted_senders = ["bad node"]`)
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid permitted_senders entry")
	}
}

func TestLoad_EmptySenders_Error(t *testing.T) {
	toml := strings.ReplaceAll(validBinding, `permitted_senders = ["boss"]`, `permitted_senders = []`)
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty permitted_senders without AllowEmptySenders()")
	}
}

func TestLoad_EmptySenders_AllowOption(t *testing.T) {
	toml := strings.ReplaceAll(validBinding, `permitted_senders = ["boss"]`, `permitted_senders = []`)
	path := writeToml(t, toml, 0o600)
	_, err := Load(path, AllowEmptySenders())
	if err != nil {
		t.Fatalf("unexpected error with AllowEmptySenders(): %v", err)
	}
}

// --- Duplicate detection (Constraint 12 — boss condition: both branches) ---

func TestLoad_DuplicateChannelID(t *testing.T) {
	toml := `
[[binding]]
channel_id        = "ch1"
node_name         = "worker"
context_id        = "ctx1"
session_name      = "sess"
pane_title        = "worker-pane"
pane_node_name    = ""
active            = true
permitted_senders = ["boss"]

[[binding]]
channel_id        = "ch1"
node_name         = "worker2"
context_id        = "ctx1"
session_name      = "sess"
pane_title        = "worker2-pane"
pane_node_name    = ""
active            = true
permitted_senders = ["boss"]
`
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate channel_id")
	}
	if !strings.Contains(err.Error(), "duplicate channel_id") {
		t.Errorf("expected 'duplicate channel_id' in error, got: %v", err)
	}
}

func TestLoad_DuplicateNodeName(t *testing.T) {
	toml := `
[[binding]]
channel_id        = "ch1"
node_name         = "worker"
context_id        = "ctx1"
session_name      = "sess"
pane_title        = "worker-pane"
pane_node_name    = ""
active            = true
permitted_senders = ["boss"]

[[binding]]
channel_id        = "ch2"
node_name         = "worker"
context_id        = "ctx1"
session_name      = "sess"
pane_title        = "worker-pane2"
pane_node_name    = ""
active            = true
permitted_senders = ["boss"]
`
	path := writeToml(t, toml, 0o600)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate node_name")
	}
	if !strings.Contains(err.Error(), "duplicate node_name") {
		t.Errorf("expected 'duplicate node_name' in error, got: %v", err)
	}
}

// --- Seven-row state validity table: 9 invalid combinations ---

func makeStateToml(active bool, sessionName, paneTitle, paneNodeName string) string {
	activeStr := "false"
	if active {
		activeStr = "true"
	}
	return `
[[binding]]
channel_id        = "ch1"
node_name         = "worker"
context_id        = "ctx1"
session_name      = "` + sessionName + `"
pane_title        = "` + paneTitle + `"
pane_node_name    = "` + paneNodeName + `"
active            = ` + activeStr + `
permitted_senders = ["boss"]
`
}

func TestLoad_InvalidStateRows(t *testing.T) {
	// 9 invalid combinations
	cases := []struct {
		name         string
		active       bool
		sessionName  string
		paneTitle    string
		paneNodeName string
	}{
		// active=true, session_name="" (4 rows)
		{"active_no_session_no_title_no_node", true, "", "", ""},
		{"active_no_session_no_title_with_node", true, "", "", "node1"},
		{"active_no_session_with_title_no_node", true, "", "title1", ""},
		{"active_no_session_with_title_with_node", true, "", "title1", "node1"},
		// active=true, session set, no pane identity
		{"active_session_no_title_no_node", true, "sess", "", ""},
		// active=false, no session, non-empty pane fields (3 rows)
		{"inactive_no_session_no_title_with_node", false, "", "", "node1"},
		{"inactive_no_session_with_title_no_node", false, "", "title1", ""},
		{"inactive_no_session_with_title_with_node", false, "", "title1", "node1"},
		// active=false, session set, no pane identity
		{"inactive_session_no_title_no_node", false, "sess", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := makeStateToml(tc.active, tc.sessionName, tc.paneTitle, tc.paneNodeName)
			path := writeToml(t, content, 0o600)
			_, err := Load(path, AllowEmptySenders())
			if err == nil {
				t.Errorf("expected state validation error for %s, got nil", tc.name)
			}
		})
	}
}

// Verify all 7 valid state rows are accepted.
func TestLoad_ValidStateRows(t *testing.T) {
	cases := []struct {
		name         string
		active       bool
		sessionName  string
		paneTitle    string
		paneNodeName string
	}{
		{"row1_unassigned", false, "", "", ""},
		{"row2_assigned_node", true, "sess", "", "node1"},
		{"row3_assigned_title", true, "sess", "title1", ""},
		{"row4_assigned_both", true, "sess", "title1", "node1"},
		{"row5_inactive_node", false, "sess", "", "node1"},
		{"row6_inactive_title", false, "sess", "title1", ""},
		{"row7_inactive_both", false, "sess", "title1", "node1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := makeStateToml(tc.active, tc.sessionName, tc.paneTitle, tc.paneNodeName)
			path := writeToml(t, content, 0o600)
			_, err := Load(path, AllowEmptySenders())
			if err != nil {
				t.Errorf("unexpected error for valid row %s: %v", tc.name, err)
			}
		})
	}
}
