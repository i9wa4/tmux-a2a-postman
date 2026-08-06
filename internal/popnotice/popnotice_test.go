package popnotice

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func msg(filename, from, to string) message.MessageInfo {
	return message.MessageInfo{Filename: filename, From: from, To: to}
}

func TestBuildStaleBacklogNoticeCapsOrderedSameRouteIDs(t *testing.T) {
	popped := msg("20260729-080000-s0000-r0000-from-orchestrator-to-worker.md", "orchestrator", "worker")
	remaining := []message.MessageInfo{
		msg("20260729-080001-s0000-r0001-from-orchestrator-to-worker.md", "orchestrator", "worker"),
		msg("20260729-080002-s0000-r0002-from-orchestrator-to-worker.md", "orchestrator", "worker"),
		msg("20260729-080003-s0000-r0003-from-orchestrator-to-worker.md", "orchestrator", "worker"),
		msg("20260729-080004-s0000-r0004-from-orchestrator-to-worker.md", "orchestrator", "worker"),
		msg("20260729-080005-s0000-r0005-from-orchestrator-to-worker.md", "orchestrator", "worker"),
		msg("20260729-080006-s0000-r0006-from-orchestrator-to-worker.md", "orchestrator", "worker"),
	}

	notice := BuildStaleBacklogNotice(popped, remaining)
	if notice == nil {
		t.Fatal("BuildStaleBacklogNotice() = nil, want notice")
	}
	if notice.NewerUnreadCount != 6 {
		t.Fatalf("NewerUnreadCount = %d, want 6", notice.NewerUnreadCount)
	}
	if notice.NewestMessageID != remaining[5].Filename {
		t.Fatalf("NewestMessageID = %q, want %q", notice.NewestMessageID, remaining[5].Filename)
	}
	if len(notice.NewerMessageIDs) != 5 {
		t.Fatalf("NewerMessageIDs length = %d, want cap 5", len(notice.NewerMessageIDs))
	}
	for idx, got := range notice.NewerMessageIDs {
		if got != remaining[idx].Filename {
			t.Fatalf("NewerMessageIDs[%d] = %q, want %q", idx, got, remaining[idx].Filename)
		}
	}
}

func TestBuildStaleBacklogNoticeFiltersRouteAndMalformedPoppedMessage(t *testing.T) {
	if got := BuildStaleBacklogNotice(message.MessageInfo{}, []message.MessageInfo{
		msg("20260729-080001-s0000-r0001-from-orchestrator-to-worker.md", "orchestrator", "worker"),
	}); got != nil {
		t.Fatalf("BuildStaleBacklogNotice(malformed popped) = %#v, want nil", got)
	}

	popped := msg("20260729-080000-s0000-r0000-from-orchestrator-to-worker.md", "orchestrator", "worker")
	remaining := []message.MessageInfo{
		msg("20260729-080001-s0000-r0001-from-critic-to-worker.md", "critic", "worker"),
		msg("20260729-080002-s0000-r0002-from-orchestrator-to-critic.md", "orchestrator", "critic"),
		msg("20260729-075959-s0000-rffff-from-orchestrator-to-worker.md", "orchestrator", "worker"),
	}
	if got := BuildStaleBacklogNotice(popped, remaining); got != nil {
		t.Fatalf("BuildStaleBacklogNotice(route filtered) = %#v, want nil", got)
	}
}
