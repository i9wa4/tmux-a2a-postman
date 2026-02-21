package diplomat

import (
	"path/filepath"
	"time"
)

// ContextMessage represents a message exchanged between diplomat contexts.
type ContextMessage struct {
	From      string
	To        string
	Timestamp time.Time
	Body      string
}

// DiplomatRef holds a reference to another context for mutual communication.
type DiplomatRef struct {
	ContextID string
	BaseDir   string
}

// SharedDir returns the shared directory path for a given context.
// Convention: BaseDir/diplomat/<contextID>/
func (r DiplomatRef) SharedDir() string {
	return filepath.Join(r.BaseDir, "diplomat", r.ContextID)
}
