package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// TestBuildInterfaceNodeStatus (#764 S1): get-status carries the fail-closed
// marker exactly when no usable interface node is configured, and nothing when
// one is.
func TestBuildInterfaceNodeStatus(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		wantMarker bool
	}{
		{name: "nil config", cfg: nil, wantMarker: true},
		{name: "unset", cfg: &config.Config{}, wantMarker: true},
		{name: "blank", cfg: &config.Config{InterfaceNode: "  "}, wantMarker: true},
		{name: "embedded default", cfg: config.DefaultConfig(), wantMarker: true},
		{name: "configured", cfg: &config.Config{InterfaceNode: "messenger"}, wantMarker: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildInterfaceNodeStatus(tt.cfg)
			if (got != nil) != tt.wantMarker {
				t.Fatalf("buildInterfaceNodeStatus() = %#v, want marker=%v", got, tt.wantMarker)
			}
			if got == nil {
				return
			}
			if got.Configured {
				t.Fatalf("marker.Configured = true, want false")
			}
			if got.Diagnostic != config.NoInterfaceNodeDiagnostic {
				t.Fatalf("marker.Diagnostic = %q, want %q", got.Diagnostic, config.NoInterfaceNodeDiagnostic)
			}
		})
	}
}

// TestBuildInterfaceNodeStatus_JSONShape pins the exact public JSON of the
// marker so a consumer can rely on it.
func TestBuildInterfaceNodeStatus_JSONShape(t *testing.T) {
	raw, err := json.Marshal(buildInterfaceNodeStatus(&config.Config{}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"configured":false,"diagnostic":"no interface_node configured: no verdict exemption, no escalation push target"}`
	if string(raw) != want {
		t.Fatalf("marker JSON = %s, want %s", raw, want)
	}
}

// TestInterfaceNodeDiagnosticText (#764 S1) pins the operator-visible wording.
func TestInterfaceNodeDiagnosticText(t *testing.T) {
	for _, want := range []string{"no interface_node configured", "no verdict exemption", "no escalation push target"} {
		if !strings.Contains(config.NoInterfaceNodeDiagnostic, want) {
			t.Fatalf("diagnostic %q missing %q", config.NoInterfaceNodeDiagnostic, want)
		}
	}
}
