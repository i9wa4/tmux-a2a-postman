// Package messageid owns canonical identifiers shared by mail and diagnostics.
package messageid

import (
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	Layout        = "20060102T150405.000000000Z"
	shortAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// New records UTC creation time followed by six uniformly sampled base62 digits.
// Equal-time IDs are checked for collisions by the workspace before use.
func New(at time.Time) (string, error) {
	at = at.UTC()
	if at.Year() < 1 || at.Year() > 9999 {
		return "", errors.New("message creation time is outside the canonical year range")
	}
	suffix, err := randomSuffix(rand.Reader)
	if err != nil {
		return "", err
	}
	return "m-" + at.Format(Layout) + "-" + suffix, nil
}

func randomSuffix(reader io.Reader) (string, error) {
	var suffix [6]byte
	var random [6]byte
	for used := 0; used < len(suffix); {
		if _, err := io.ReadFull(reader, random[:]); err != nil {
			return "", err
		}
		for _, value := range random {
			if value >= 248 {
				continue // Reject the incomplete final group to avoid modulo bias.
			}
			suffix[used] = shortAlphabet[int(value)%len(shortAlphabet)]
			used++
			if used == len(suffix) {
				break
			}
		}
	}
	return string(suffix[:]), nil
}

func legacyEntropy(s string) bool {
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

func shortEntropy(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// Timestamp validates a new ID and returns its recorded creation time. Legacy
// random-only IDs have no timestamp and return false.
func Timestamp(id string) (time.Time, bool) {
	const end = 2 + len(Layout)
	if len(id) <= end+1 || !strings.HasPrefix(id, "m-") || id[end] != '-' ||
		!(shortEntropy(id[end+1:]) || legacyEntropy(id[end+1:])) {
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
		return legacyEntropy(id[2:])
	}
	_, ok := Timestamp(id)
	return ok
}

// Suffix returns the random portion of a valid ID, without its recorded time.
func Suffix(id string) string {
	if !Valid(id) {
		return ""
	}
	if len(id) == 34 {
		return id[2:]
	}
	return id[2+len(Layout)+1:]
}
