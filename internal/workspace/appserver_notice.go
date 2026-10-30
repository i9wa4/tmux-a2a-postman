package workspace

import (
	"context"
	"errors"
	"strconv"
)

// NotifyCommittedAppServerAfterCommit attempts an explicit, bounded notice
// after mail commit. Turn acceptance does not prove recipient POP.
func (s Store) NotifyCommittedAppServerAfterCommit(ctx context.Context, rt Runtime, msg Message) NoticeResult {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PostCommitNoticeBudget)
	defer cancel()
	result := NoticeResult{Status: "unavailable"}
	if rt == nil || !messageID(msg.ID) || msg.FromSession != "" || msg.ToSession != "" {
		result.Reason = "app-server notice requires a committed same-session message"
		return result
	}
	// Keep the old daemon excluded across every short state transition, app-server
	// write, turn submission, and terminal binding audit. A daemon that starts
	// between mail commit and this lease makes the notice unavailable.
	lease, err := s.acquireLegacyLease()
	if err != nil {
		result.Reason = "legacy daemon lease unavailable: " + err.Error()
		return result
	}
	defer lease.Close()
	before, err := rt.Snapshot(ctx)
	if err != nil || validateSnapshotForNotice(before, msg) != nil {
		result.Reason = "sender binding unavailable"
		return result
	}
	reservation, err := s.ReserveAppServerNotice(ctx, rt, msg)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			result.Status = "unknown"
			result.Reason = "prior notice attempt may have reached app-server"
		} else {
			result.Reason = err.Error()
		}
		return result
	}
	result.Pane = reservation.Target.Owner.Pane
	if !sameNoticeTarget(before, msg.To, reservation.Target) {
		result.Reason = "recipient binding changed before notice"
		return result
	}
	panePID, err := strconv.Atoi(reservation.Target.PanePID)
	if err != nil || panePID <= 0 {
		result.Reason = "recipient process unavailable"
		return result
	}
	// Connect and verify the recipient-owned peer before any app-server write.
	socket, err := connectCodexSocket(ctx, reservation.Socket, panePID)
	if err != nil {
		if markErr := s.MarkAppServerNoticeDialFailed(ctx, rt, msg, reservation.AttemptID); markErr != nil {
			result.Status, result.Reason = "unknown", "cannot durably record pre-write failure: "+markErr.Error()
			return result
		}
		result.Reason = "app-server connection unavailable: " + err.Error()
		return result
	}
	defer socket.close()
	// Upgrade is the first write. Mark before it; a partial upgrade or
	// later RPC cannot be automatically replayed.
	if err := s.MarkAppServerNoticeRPCStarted(ctx, rt, msg, reservation.AttemptID); err != nil {
		result.Status, result.Reason = "unknown", "cannot durably start notice: "+err.Error()
		return result
	}
	result.Status = "unknown"
	if err := socket.upgrade(); err != nil {
		result.Reason = "app-server connection outcome unknown: " + err.Error()
		return result
	}
	if err := socket.initialize(ctx); err != nil {
		result.Reason = "app-server initialization outcome unknown: " + err.Error()
		return result
	}
	threadID, err := socket.startThread(ctx, before.Root)
	if err != nil {
		result.Reason = "app-server thread outcome unknown: " + err.Error()
		return result
	}
	if err := s.RecordAppServerNoticeThreadStarted(ctx, rt, msg, reservation.AttemptID, threadID); err != nil {
		result.Reason = "cannot durably record app-server thread: " + err.Error()
		return result
	}
	turnID, err := socket.startBarePopTurn(ctx, threadID, before.Root)
	if err != nil {
		result.Reason = "app-server turn outcome unknown: " + err.Error()
		return result
	}
	if err := s.RecordAppServerNoticeTurnAccepted(ctx, rt, msg, reservation.AttemptID, threadID, turnID); err != nil {
		result.Reason = "cannot durably record app-server turn: " + err.Error()
		return result
	}
	after, err := rt.Snapshot(ctx)
	if err != nil || validateSnapshotForNotice(after, msg) != nil || !sameNoticeTarget(after, msg.To, reservation.Target) {
		result.Reason = "recipient binding changed after turn acceptance"
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Reason = "notice deadline elapsed after turn acceptance: " + err.Error()
		return result
	}
	result.Status = "accepted"
	result.Reason = "recipient-owned app-server accepted bare POP turn; recipient POP not observed"
	return result
}

func sameNoticeTarget(snapshot Snapshot, role string, target Binding) bool {
	for _, item := range snapshot.Bindings {
		if item.Role == role && item == target {
			return true
		}
	}
	return false
}
