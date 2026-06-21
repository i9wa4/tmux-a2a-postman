package runtimeprofile

import (
	"strings"
	"testing"
)

func TestCaptureRejectsUnknownProfileType(t *testing.T) {
	if _, err := Capture("thread", DefaultMaxBytes); err == nil {
		t.Fatal("Capture() error = nil, want unsupported profile type")
	}
}

func TestCaptureGoroutineProfileIsBoundedExplicitSnapshot(t *testing.T) {
	data, err := Capture(KindGoroutine, DefaultMaxBytes)
	if err != nil {
		t.Fatalf("Capture(goroutine): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Capture(goroutine) returned empty profile")
	}
	if len(data) > int(DefaultMaxBytes) {
		t.Fatalf("profile bytes = %d, want <= %d", len(data), DefaultMaxBytes)
	}
}

func TestCaptureErrorsWhenProfileExceedsLimit(t *testing.T) {
	_, err := Capture(KindGoroutine, 1)
	if err == nil {
		t.Fatal("Capture() error = nil, want max-bytes error")
	}
	if !strings.Contains(err.Error(), "exceeded 1 bytes") {
		t.Fatalf("Capture() error = %q, want max-bytes context", err)
	}
}
