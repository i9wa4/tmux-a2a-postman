package runtimeprofile

import (
	"bytes"
	"fmt"
	"runtime/pprof"
)

const (
	KindHeap      = "heap"
	KindGoroutine = "goroutine"
)

const DefaultMaxBytes int64 = 16 << 20

type limitWriter struct {
	buf   *bytes.Buffer
	limit int64
	n     int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.limit <= 0 {
		return 0, fmt.Errorf("profile capture max bytes must be positive")
	}
	remaining := w.limit - w.n
	if remaining <= 0 {
		return 0, fmt.Errorf("profile capture exceeded %d bytes", w.limit)
	}
	if int64(len(p)) > remaining {
		_, _ = w.buf.Write(p[:remaining])
		w.n += remaining
		return int(remaining), fmt.Errorf("profile capture exceeded %d bytes", w.limit)
	}
	n, err := w.buf.Write(p)
	w.n += int64(n)
	return n, err
}

func NormalizeKind(kind string) (string, error) {
	switch kind {
	case KindHeap, KindGoroutine:
		return kind, nil
	default:
		return "", fmt.Errorf("unsupported profile type %q (want heap or goroutine)", kind)
	}
}

func Capture(kind string, maxBytes int64) ([]byte, error) {
	normalized, err := NormalizeKind(kind)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	var buf bytes.Buffer
	writer := &limitWriter{buf: &buf, limit: maxBytes}
	profile := pprof.Lookup(normalized)
	if profile == nil {
		return nil, fmt.Errorf("profile type %q is unavailable", normalized)
	}
	if err := profile.WriteTo(writer, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
