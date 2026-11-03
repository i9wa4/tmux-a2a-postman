// Package messageid owns canonical identifiers shared by mail and diagnostics.
package messageid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const Layout = "20060102T150405.000000000Z"

// New records UTC creation time followed by 128 bits of independent entropy.
// Equal-time IDs are unique without promising FIFO or a monotonic wall clock.
func New(at time.Time) (string, error) {
	at = at.UTC()
	if at.Year() < 1 || at.Year() > 9999 {
		return "", errors.New("message creation time is outside the canonical year range")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "m-" + at.Format(Layout) + "-" + hex.EncodeToString(random[:]), nil
}

func entropy(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Timestamp validates a new ID and returns its recorded creation time. Legacy
// random-only IDs have no timestamp and return false.
func Timestamp(id string) (time.Time, bool) {
	const end = 2 + len(Layout)
	if len(id) != end+1+32 || !strings.HasPrefix(id, "m-") || id[end] != '-' || !entropy(id[end+1:]) {
		return time.Time{}, false
	}
	stamp := id[2:end]
	at, err := time.Parse(Layout, stamp)
	if err != nil || at.Year() < 1 || at.Format(Layout) != stamp {
		return time.Time{}, false
	}
	return at, true
}

func Valid(id string) bool {
	if strings.HasPrefix(id, "m-") && len(id) == 34 {
		return entropy(id[2:])
	}
	_, ok := Timestamp(id)
	return ok
}

// Suffix retains the fixed-width correlation token without including time.
func Suffix(id string) string {
	if !Valid(id) {
		return ""
	}
	return id[len(id)-32:]
}
