package workspace

import (
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

func inputRequestID(id string) string { return "ireq_" + messageid.Suffix(id) }
