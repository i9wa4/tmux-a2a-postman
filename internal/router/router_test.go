package router

import "testing"

func TestResolve_BareNamesStayInSourceSession(t *testing.T) {
	nodes := map[string]bool{
		"review:worker": true,
		"other:worker":  true,
	}

	got := Resolve("worker", "review", func(key string) bool { return nodes[key] }, nil)

	if !got.Found {
		t.Fatalf("Found = false, want true: %#v", got)
	}
	if got.Address != "review:worker" {
		t.Fatalf("Address = %q, want review:worker", got.Address)
	}
	if got.ExplicitSession {
		t.Fatal("ExplicitSession = true, want false")
	}
}

func TestResolve_BareNamesDoNotFallbackAcrossSessions(t *testing.T) {
	nodes := map[string]bool{
		"other:worker": true,
	}

	got := Resolve("worker", "review", func(key string) bool { return nodes[key] }, nil)

	if got.Found {
		t.Fatalf("Found = true, want false: %#v", got)
	}
	if got.FailureReason != FailureUnknownNode {
		t.Fatalf("FailureReason = %q, want %q", got.FailureReason, FailureUnknownNode)
	}
}

func TestResolve_ExplicitSessionUsesExactAddress(t *testing.T) {
	nodes := map[string]bool{
		"other:worker": true,
	}

	got := Resolve("other:worker", "review", func(key string) bool { return nodes[key] }, nil)

	if !got.Found {
		t.Fatalf("Found = false, want true: %#v", got)
	}
	if got.Address != "other:worker" {
		t.Fatalf("Address = %q, want other:worker", got.Address)
	}
	if got.SessionName != "other" || got.NodeName != "worker" {
		t.Fatalf("session/node = %q/%q, want other/worker", got.SessionName, got.NodeName)
	}
	if !got.ExplicitSession {
		t.Fatal("ExplicitSession = false, want true")
	}
}

func TestResolve_ExplicitUnknownSessionIsDistinctFromUnknownNode(t *testing.T) {
	nodes := map[string]bool{
		"review:worker": true,
	}
	sessions := map[string]bool{
		"review": true,
	}

	got := Resolve("missing:worker", "review", func(key string) bool { return nodes[key] }, func(session string) bool { return sessions[session] })
	if got.Found {
		t.Fatalf("Found = true, want false: %#v", got)
	}
	if got.FailureReason != FailureUnknownSession {
		t.Fatalf("FailureReason = %q, want %q", got.FailureReason, FailureUnknownSession)
	}

	got = Resolve("review:missing", "review", func(key string) bool { return nodes[key] }, func(session string) bool { return sessions[session] })
	if got.Found {
		t.Fatalf("Found = true, want false: %#v", got)
	}
	if got.FailureReason != FailureUnknownNode {
		t.Fatalf("FailureReason = %q, want %q", got.FailureReason, FailureUnknownNode)
	}
}
