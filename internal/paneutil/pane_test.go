package paneutil

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCaptureContentWithRunner(t *testing.T) {
	var gotArgs []string
	run := func(args ...string) ([]byte, error) {
		gotArgs = append(gotArgs, args...)
		return []byte("pane content"), nil
	}

	got, err := captureContent("%11", 0, run)
	if err != nil {
		t.Fatalf("captureContent: %v", err)
	}
	if got != "pane content" {
		t.Fatalf("captureContent() = %q, want %q", got, "pane content")
	}
	wantArgs := []string{"capture-pane", "-p", "-t", "%11"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestCaptureRecentContentWithRunner(t *testing.T) {
	var gotArgs []string
	run := func(args ...string) ([]byte, error) {
		gotArgs = append(gotArgs, args...)
		return []byte("recent content"), nil
	}

	got, err := captureContent("%11", 100, run)
	if err != nil {
		t.Fatalf("captureContent: %v", err)
	}
	if got != "recent content" {
		t.Fatalf("captureContent() = %q, want %q", got, "recent content")
	}
	wantArgs := []string{"capture-pane", "-p", "-t", "%11", "-S", "-100"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestCaptureHistoryContentWithRunner(t *testing.T) {
	var gotArgs []string
	run := func(args ...string) ([]byte, error) {
		gotArgs = append(gotArgs, args...)
		return []byte("history content"), nil
	}

	got, err := captureHistoryContent("%11", run)
	if err != nil {
		t.Fatalf("captureHistoryContent: %v", err)
	}
	if got != "history content" {
		t.Fatalf("captureHistoryContent() = %q, want %q", got, "history content")
	}
	wantArgs := []string{"capture-pane", "-p", "-t", "%11", "-S", "-"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestCaptureContentWithRunner_PropagatesFailure(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		return nil, errors.New("tmux failed")
	}

	got, err := captureContent("%11", 0, run)
	if err == nil {
		t.Fatal("captureContent() error = nil, want error")
	}
	if got != "" {
		t.Fatalf("captureContent() = %q, want empty string on failure", got)
	}
	if !strings.Contains(err.Error(), "capturing pane %11") || !strings.Contains(err.Error(), "tmux failed") {
		t.Fatalf("error = %q, want capture context and source error", err.Error())
	}
}

func TestCaptureHistoryContentWithRunner_PropagatesFailure(t *testing.T) {
	run := func(args ...string) ([]byte, error) {
		return nil, errors.New("tmux failed")
	}

	got, err := captureHistoryContent("%11", run)
	if err == nil {
		t.Fatal("captureHistoryContent() error = nil, want error")
	}
	if got != "" {
		t.Fatalf("captureHistoryContent() = %q, want empty string on failure", got)
	}
	if !strings.Contains(err.Error(), "capturing pane %11 history") || !strings.Contains(err.Error(), "tmux failed") {
		t.Fatalf("error = %q, want capture context and source error", err.Error())
	}
}
