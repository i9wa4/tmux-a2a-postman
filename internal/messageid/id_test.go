package messageid

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestNewIDsSortByRecordedTimeAndRemainUniqueAtEqualTimes(t *testing.T) {
	at := time.Date(2026, 11, 2, 20, 0, 1, 123456789, time.FixedZone("east", 9*60*60))
	seen := map[string]bool{}
	for range 1000 {
		id, err := New(at)
		stamp, ok := Timestamp(id)
		if err != nil || !ok || !stamp.Equal(at) || seen[id] || len(Suffix(id)) != 6 {
			t.Fatal("invalid or repeated same-time identity", id, err)
		}
		seen[id] = true
		next, err := New(at.Add(time.Nanosecond))
		if err != nil || id >= next {
			t.Fatal("new filenames must follow recorded UTC time", id, next, err)
		}
	}
}

func TestLegacyAndStrictCalendarValidation(t *testing.T) {
	legacy := "m-" + strings.Repeat("a", 32)
	good := "m-20240229T235959.000000001Z-" + strings.Repeat("b", 32)
	short := "m-20240229T235959.000000001Z-aA09Zz"
	if !Valid(legacy) || !Valid(good) || !Valid(short) || Suffix(legacy) != strings.Repeat("a", 32) || Suffix(short) != "aA09Zz" {
		t.Fatal("legacy and leap-day IDs must remain valid")
	}
	for _, bad := range []string{
		"", legacy + "x", strings.ToUpper(legacy), "../" + legacy,
		strings.Replace(good, "20240229", "20230229", 1),
		strings.Replace(good, "20240229", "20241301", 1),
		strings.Replace(good, "235959", "240000", 1),
		strings.Replace(good, "000000001Z", "1Z", 1),
		strings.Replace(good, "Z-", "z-", 1),
		strings.Replace(good, "T", "/", 1),
		strings.Replace(good, "2024", "0000", 1),
		good[:len(good)-1] + ".",
		strings.Replace(short, "aA09Zz", "-A09Zz", 1),
		strings.Replace(short, "aA09Zz", "aA09Z_", 1),
		strings.Replace(short, "aA09Zz", "aA09Z", 1),
	} {
		if Valid(bad) || Suffix(bad) != "" {
			t.Fatal("accepted malformed/traversal identity", bad)
		}
	}
	if _, ok := Timestamp(legacy); ok {
		t.Fatal("legacy entropy is not a creation timestamp")
	}
}

func TestRandomSuffixRejectsModuloBias(t *testing.T) {
	// 248..255 must be discarded; zero maps to the first base62 digit.
	suffix, err := randomSuffix(bytes.NewReader([]byte{248, 249, 250, 251, 252, 253, 0, 1, 61, 62, 123, 247}))
	if err != nil || suffix != "01z0zz" {
		t.Fatal(suffix, err)
	}
}
