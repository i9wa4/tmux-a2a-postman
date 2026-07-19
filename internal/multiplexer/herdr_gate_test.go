package multiplexer

import (
	"errors"
	"testing"
)

func TestValidateHerdrReadGatePassesWithAllowlistedRuntimeAndEnvelope(t *testing.T) {
	err := ValidateHerdrReadGate(validHerdrGatePolicy(), validHerdrRuntime(), validHerdrEnvelope())
	if err != nil {
		t.Fatalf("ValidateHerdrReadGate() error = %v", err)
	}
}

func TestValidateHerdrReadGateFailsClosed(t *testing.T) {
	policy := validHerdrGatePolicy()
	policy.ReadEnabled = false

	err := ValidateHerdrReadGate(policy, validHerdrRuntime(), validHerdrEnvelope())
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "read_enabled", HerdrGateFailureClosed)
}

func TestValidateHerdrReadGateAllowsDiscoveryWithoutPaneTarget(t *testing.T) {
	runtime := validHerdrRuntime()
	runtime.TabID = ""
	runtime.PaneID = ""

	err := ValidateHerdrReadGate(validHerdrGatePolicy(), runtime, validHerdrEnvelope())
	if err != nil {
		t.Fatalf("ValidateHerdrReadGate() error = %v", err)
	}
}

func TestValidateHerdrReadGateRequiresOrderedDiscoveryRuntimeIdentity(t *testing.T) {
	err := ValidateHerdrReadGate(validHerdrGatePolicy(), HerdrRuntimeIdentity{}, validHerdrEnvelope())
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "socket_path", HerdrGateFailureMissingRuntime)
}

func TestValidateHerdrPaneReadGateRequiresPaneTargetIdentity(t *testing.T) {
	policy := validHerdrGatePolicy()
	policy.ReadScope = HerdrReadScopePane
	runtime := validHerdrRuntime()
	runtime.PaneID = ""

	err := ValidateHerdrReadGate(policy, runtime, validHerdrEnvelope())
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "pane_id", HerdrGateFailureMissingRuntime)
}

func TestValidateHerdrReadGateRejectsUnknownReadScope(t *testing.T) {
	policy := validHerdrGatePolicy()
	policy.ReadScope = HerdrReadScope("pane-target")
	runtime := validHerdrRuntime()
	runtime.PaneID = ""

	err := ValidateHerdrReadGate(policy, runtime, validHerdrEnvelope())
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "read_scope", HerdrGateFailureUnsupportedScope)
}

func TestValidateHerdrReadGateRequiresAllowlistedRuntime(t *testing.T) {
	runtime := validHerdrRuntime()
	runtime.WorkspaceID = "workspace-other"

	err := ValidateHerdrReadGate(validHerdrGatePolicy(), runtime, validHerdrEnvelope())
	assertHerdrGateError(t, err, HerdrAccessPhaseRead, "workspace_id", HerdrGateFailureNotAllowlisted)
}

func TestValidateHerdrReadGateRequiresSupportedProtocolAndSchema(t *testing.T) {
	t.Run("protocol", func(t *testing.T) {
		envelope := validHerdrEnvelope()
		envelope.ProtocolVersion = "99"

		err := ValidateHerdrReadGate(validHerdrGatePolicy(), validHerdrRuntime(), envelope)
		assertHerdrGateError(t, err, HerdrAccessPhaseRead, "protocol_version", HerdrGateFailureUnsupportedProtocol)
	})

	t.Run("schema", func(t *testing.T) {
		envelope := validHerdrEnvelope()
		envelope.SchemaVersion = 99

		err := ValidateHerdrReadGate(validHerdrGatePolicy(), validHerdrRuntime(), envelope)
		assertHerdrGateError(t, err, HerdrAccessPhaseRead, "schema_version", HerdrGateFailureUnsupportedSchema)
	})

	for _, protocol := range []string{"", "0", "abc"} {
		t.Run("invalid protocol "+protocol, func(t *testing.T) {
			envelope := validHerdrEnvelope()
			envelope.ProtocolVersion = protocol

			err := ValidateHerdrReadGate(validHerdrGatePolicy(), validHerdrRuntime(), envelope)
			assertHerdrGateError(t, err, HerdrAccessPhaseRead, "protocol_version", HerdrGateFailureUnsupportedProtocol)
		})
	}
}

func TestValidateHerdrWriteGateRequiresReadGate(t *testing.T) {
	t.Run("read disabled", func(t *testing.T) {
		policy := validHerdrGatePolicy()
		policy.ReadEnabled = false
		policy.WriteEnabled = true

		err := ValidateHerdrWriteGate(policy, validHerdrRuntime(), validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "read_enabled", HerdrGateFailureClosed)
	})

	t.Run("pane target", func(t *testing.T) {
		runtime := validHerdrRuntime()
		runtime.PaneID = ""

		err := ValidateHerdrWriteGate(validHerdrGatePolicy(), runtime, validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "pane_id", HerdrGateFailureMissingRuntime)
	})

	t.Run("workspace allowlist", func(t *testing.T) {
		runtime := validHerdrRuntime()
		runtime.WorkspaceID = "workspace-other"

		err := ValidateHerdrWriteGate(validHerdrGatePolicy(), runtime, validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "workspace_id", HerdrGateFailureNotAllowlisted)
	})

	t.Run("protocol", func(t *testing.T) {
		envelope := validHerdrEnvelope()
		envelope.ProtocolVersion = "99"

		err := ValidateHerdrWriteGate(validHerdrGatePolicy(), validHerdrRuntime(), envelope)
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "protocol_version", HerdrGateFailureUnsupportedProtocol)
	})

	t.Run("overrides invalid read scope", func(t *testing.T) {
		policy := validHerdrGatePolicy()
		policy.ReadScope = HerdrReadScope("pane-target")
		runtime := validHerdrRuntime()
		runtime.PaneID = ""

		err := ValidateHerdrWriteGate(policy, runtime, validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "pane_id", HerdrGateFailureMissingRuntime)
	})
}

func TestValidateHerdrWriteGateRequiresWriteSpecificGates(t *testing.T) {
	t.Run("closed", func(t *testing.T) {
		policy := validHerdrGatePolicy()
		policy.WriteEnabled = false

		err := ValidateHerdrWriteGate(policy, validHerdrRuntime(), validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "", HerdrGateFailureClosed)
	})

	t.Run("sanitizer", func(t *testing.T) {
		policy := validHerdrGatePolicy()
		policy.InputSanitizerReady = false

		err := ValidateHerdrWriteGate(policy, validHerdrRuntime(), validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "input_sanitizer", HerdrGateFailureSanitizerMissing)
	})

	t.Run("compliance", func(t *testing.T) {
		policy := validHerdrGatePolicy()
		policy.ComplianceDecision = HerdrComplianceDecisionUnset

		err := ValidateHerdrWriteGate(policy, validHerdrRuntime(), validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "compliance_decision", HerdrGateFailureComplianceUnresolved)
	})

	t.Run("review-only compliance does not authorize writes", func(t *testing.T) {
		policy := validHerdrGatePolicy()
		policy.ComplianceDecision = HerdrComplianceDecisionReviewOnly

		err := ValidateHerdrWriteGate(policy, validHerdrRuntime(), validHerdrEnvelope())
		assertHerdrGateError(t, err, HerdrAccessPhaseWrite, "compliance_decision", HerdrGateFailureComplianceUnresolved)
	})
}

func TestHerdrRuntimeIdentityFromEnv(t *testing.T) {
	values := map[string]string{
		"HERDR_SOCKET_PATH":  "/tmp/herdr.sock",
		"HERDR_SESSION":      "work",
		"HERDR_WORKSPACE_ID": "workspace-1",
		"HERDR_TAB_ID":       "tab-1",
		"HERDR_PANE_ID":      "pane-1",
	}

	got := HerdrRuntimeIdentityFromEnv(func(key string) string {
		return values[key]
	})

	if got.SocketPath != "/tmp/herdr.sock" ||
		got.SessionName != "work" ||
		got.WorkspaceID != "workspace-1" ||
		got.TabID != "tab-1" ||
		got.PaneID != "pane-1" {
		t.Fatalf("HerdrRuntimeIdentityFromEnv() = %#v", got)
	}
}

func TestHerdrResourceIDs(t *testing.T) {
	if got := HerdrPaneID("pane-1"); got.Backend != BackendKindHerdr || got.Kind != ResourceKindPane || got.Native != "pane-1" {
		t.Fatalf("HerdrPaneID() = %#v", got)
	}
	if got := HerdrWorkspaceID("workspace-1"); got.Backend != BackendKindHerdr || got.Kind != ResourceKindWorkspace || got.Native != "workspace-1" {
		t.Fatalf("HerdrWorkspaceID() = %#v", got)
	}
	if got := HerdrTabID("tab-1"); got.Backend != BackendKindHerdr || got.Kind != ResourceKindTab || got.Native != "tab-1" {
		t.Fatalf("HerdrTabID() = %#v", got)
	}
}

func TestHerdrSchemaVersion(t *testing.T) {
	got, err := HerdrSchemaVersion("1")
	if err != nil {
		t.Fatalf("HerdrSchemaVersion() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("HerdrSchemaVersion() = %d, want 1", got)
	}

	if _, err := HerdrSchemaVersion("0"); err == nil {
		t.Fatal("HerdrSchemaVersion(0) error = nil, want error")
	}
}

func TestHerdrProtocolVersion(t *testing.T) {
	got, err := HerdrProtocolVersion("1")
	if err != nil {
		t.Fatalf("HerdrProtocolVersion() error = %v", err)
	}
	if got != 1 {
		t.Fatalf("HerdrProtocolVersion() = %d, want 1", got)
	}

	for _, version := range []string{"", "0", "abc"} {
		if _, err := HerdrProtocolVersion(version); err == nil {
			t.Fatalf("HerdrProtocolVersion(%q) error = nil, want error", version)
		}
	}
}

func validHerdrGatePolicy() HerdrGatePolicy {
	return HerdrGatePolicy{
		ReadEnabled:             true,
		WriteEnabled:            true,
		AllowedSocketPaths:      []string{"/tmp/herdr.sock"},
		AllowedSessions:         []string{"work"},
		AllowedWorkspaceIDs:     []string{"workspace-1"},
		AllowedProtocolVersions: []string{"1"},
		AllowedSchemaVersions:   []int{1},
		InputSanitizerReady:     true,
		ComplianceDecision:      HerdrComplianceDecisionCommercial,
	}
}

func validHerdrRuntime() HerdrRuntimeIdentity {
	return HerdrRuntimeIdentity{
		SocketPath:  "/tmp/herdr.sock",
		SessionName: "work",
		WorkspaceID: "workspace-1",
		TabID:       "tab-1",
		PaneID:      "pane-1",
	}
}

func validHerdrEnvelope() HerdrResponseEnvelope {
	return HerdrResponseEnvelope{
		ProtocolVersion: "1",
		SchemaVersion:   1,
	}
}

func assertHerdrGateError(t *testing.T, err error, phase HerdrAccessPhase, field string, failure HerdrGateFailure) {
	t.Helper()
	if err == nil {
		t.Fatal("gate error = nil")
	}
	var gateErr HerdrGateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("error = %T %v, want HerdrGateError", err, err)
	}
	if gateErr.Phase != phase || gateErr.Field != field || gateErr.Failure != failure {
		t.Fatalf("gate error = %#v, want phase=%q field=%q failure=%q", gateErr, phase, field, failure)
	}
}
