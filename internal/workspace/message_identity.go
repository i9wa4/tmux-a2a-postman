package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/messageid"
)

type mailIdentity struct {
	ID        string
	CreatedAt string
}

func newMailIdentity() (mailIdentity, error) {
	at := time.Now().UTC()
	id, err := messageid.New(at)
	return mailIdentity{ID: id, CreatedAt: at.Format(time.RFC3339Nano)}, err
}

func inputRequestID(id string) string {
	suffix := messageid.Suffix(id)
	if suffix == "" {
		return ""
	}
	if len(suffix) == 32 {
		return "ireq_" + suffix // Preserve the exact legacy mapping.
	}
	sum := sha256.Sum256([]byte(id))
	return "ireq_" + hex.EncodeToString(sum[:16])
}
