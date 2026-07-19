package multiplexer

import (
	"fmt"
	"slices"
	"strconv"
)

type HerdrAccessPhase string

const (
	HerdrAccessPhaseRead  HerdrAccessPhase = "read"
	HerdrAccessPhaseWrite HerdrAccessPhase = "write"
)

type HerdrComplianceDecision string

const (
	HerdrComplianceDecisionUnset      HerdrComplianceDecision = ""
	HerdrComplianceDecisionAGPL       HerdrComplianceDecision = "agpl-3.0-or-later"
	HerdrComplianceDecisionCommercial HerdrComplianceDecision = "commercial"
	HerdrComplianceDecisionReviewOnly HerdrComplianceDecision = "review-only"
)

type HerdrGateFailure string

const (
	HerdrGateFailureClosed               HerdrGateFailure = "closed"
	HerdrGateFailureMissingRuntime       HerdrGateFailure = "missing_runtime"
	HerdrGateFailureNotAllowlisted       HerdrGateFailure = "not_allowlisted"
	HerdrGateFailureUnsupportedProtocol  HerdrGateFailure = "unsupported_protocol"
	HerdrGateFailureUnsupportedSchema    HerdrGateFailure = "unsupported_schema"
	HerdrGateFailureSanitizerMissing     HerdrGateFailure = "sanitizer_missing"
	HerdrGateFailureComplianceUnresolved HerdrGateFailure = "compliance_unresolved"
)

type HerdrGateError struct {
	Phase   HerdrAccessPhase
	Field   string
	Failure HerdrGateFailure
}

func (e HerdrGateError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("herdr %s gate %s", e.Phase, e.Failure)
	}
	return fmt.Sprintf("herdr %s gate %s for %s", e.Phase, e.Failure, e.Field)
}

type HerdrRuntimeIdentity struct {
	SocketPath  string
	SessionName string
	WorkspaceID string
	TabID       string
	PaneID      string
}

type HerdrGatePolicy struct {
	ReadEnabled             bool
	WriteEnabled            bool
	AllowedSocketPaths      []string
	AllowedSessions         []string
	AllowedWorkspaceIDs     []string
	AllowedProtocolVersions []string
	AllowedSchemaVersions   []int
	InputSanitizerReady     bool
	ComplianceDecision      HerdrComplianceDecision
}

type HerdrResponseEnvelope struct {
	ProtocolVersion string
	SchemaVersion   int
}

func HerdrRuntimeIdentityFromEnv(getenv func(string) string) HerdrRuntimeIdentity {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return HerdrRuntimeIdentity{
		SocketPath:  getenv("HERDR_SOCKET_PATH"),
		SessionName: getenv("HERDR_SESSION"),
		WorkspaceID: getenv("HERDR_WORKSPACE_ID"),
		TabID:       getenv("HERDR_TAB_ID"),
		PaneID:      getenv("HERDR_PANE_ID"),
	}
}

func ValidateHerdrReadGate(policy HerdrGatePolicy, runtime HerdrRuntimeIdentity, envelope HerdrResponseEnvelope) error {
	if !policy.ReadEnabled {
		return herdrGateError(HerdrAccessPhaseRead, "", HerdrGateFailureClosed)
	}
	if err := validateHerdrRuntime(HerdrAccessPhaseRead, policy, runtime); err != nil {
		return err
	}
	return validateHerdrEnvelope(HerdrAccessPhaseRead, policy, envelope)
}

func ValidateHerdrWriteGate(policy HerdrGatePolicy, runtime HerdrRuntimeIdentity, envelope HerdrResponseEnvelope) error {
	if !policy.WriteEnabled {
		return herdrGateError(HerdrAccessPhaseWrite, "", HerdrGateFailureClosed)
	}
	if err := validateHerdrRuntime(HerdrAccessPhaseWrite, policy, runtime); err != nil {
		return err
	}
	if err := validateHerdrEnvelope(HerdrAccessPhaseWrite, policy, envelope); err != nil {
		return err
	}
	if !policy.InputSanitizerReady {
		return herdrGateError(HerdrAccessPhaseWrite, "input_sanitizer", HerdrGateFailureSanitizerMissing)
	}
	if !isAcceptedHerdrComplianceDecision(policy.ComplianceDecision) {
		return herdrGateError(HerdrAccessPhaseWrite, "compliance_decision", HerdrGateFailureComplianceUnresolved)
	}
	return nil
}

func validateHerdrRuntime(phase HerdrAccessPhase, policy HerdrGatePolicy, runtime HerdrRuntimeIdentity) error {
	required := map[string]string{
		"socket_path":  runtime.SocketPath,
		"session_name": runtime.SessionName,
		"workspace_id": runtime.WorkspaceID,
		"tab_id":       runtime.TabID,
		"pane_id":      runtime.PaneID,
	}
	for field, value := range required {
		if value == "" {
			return herdrGateError(phase, field, HerdrGateFailureMissingRuntime)
		}
	}
	if !contains(policy.AllowedSocketPaths, runtime.SocketPath) {
		return herdrGateError(phase, "socket_path", HerdrGateFailureNotAllowlisted)
	}
	if !contains(policy.AllowedSessions, runtime.SessionName) {
		return herdrGateError(phase, "session_name", HerdrGateFailureNotAllowlisted)
	}
	if !contains(policy.AllowedWorkspaceIDs, runtime.WorkspaceID) {
		return herdrGateError(phase, "workspace_id", HerdrGateFailureNotAllowlisted)
	}
	return nil
}

func validateHerdrEnvelope(phase HerdrAccessPhase, policy HerdrGatePolicy, envelope HerdrResponseEnvelope) error {
	if envelope.ProtocolVersion == "" || !contains(policy.AllowedProtocolVersions, envelope.ProtocolVersion) {
		return herdrGateError(phase, "protocol_version", HerdrGateFailureUnsupportedProtocol)
	}
	if envelope.SchemaVersion <= 0 || !containsInt(policy.AllowedSchemaVersions, envelope.SchemaVersion) {
		return herdrGateError(phase, "schema_version", HerdrGateFailureUnsupportedSchema)
	}
	return nil
}

func isAcceptedHerdrComplianceDecision(decision HerdrComplianceDecision) bool {
	switch decision {
	case HerdrComplianceDecisionAGPL, HerdrComplianceDecisionCommercial:
		return true
	default:
		return false
	}
}

func herdrGateError(phase HerdrAccessPhase, field string, failure HerdrGateFailure) HerdrGateError {
	return HerdrGateError{Phase: phase, Field: field, Failure: failure}
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func containsInt(values []int, value int) bool {
	return slices.Contains(values, value)
}

func HerdrPaneID(paneID string) ResourceID {
	return ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindPane, Native: paneID}
}

func HerdrWorkspaceID(workspaceID string) ResourceID {
	return ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindWorkspace, Native: workspaceID}
}

func HerdrTabID(tabID string) ResourceID {
	return ResourceID{Backend: BackendKindHerdr, Kind: ResourceKindTab, Native: tabID}
}

func HerdrSchemaVersion(version string) (int, error) {
	n, err := strconv.Atoi(version)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid Herdr schema version %q", version)
	}
	return n, nil
}
