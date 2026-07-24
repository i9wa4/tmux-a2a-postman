package traceid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func NewCorrelationID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func ValidateCorrelationID(id string) error {
	if len(id) != 32 {
		return fmt.Errorf("must be exactly 32 lowercase hex characters")
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return fmt.Errorf("must be exactly 32 lowercase hex characters")
		}
	}
	return nil
}
