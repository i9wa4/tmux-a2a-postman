package projection

func ProjectPendingPostEnqueueTimes(sessionDir string) (map[string]string, bool, error) {
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return nil, false, nil
	}
	events, err := replayCurrentSessionEvents(sessionDir, state.SessionKey, state.Generation)
	if err != nil {
		return nil, false, err
	}

	enqueuedAt := make(map[string]string)
	sawLease := false
	sawResolution := false
	for _, event := range events {
		switch event.Type {
		case "lease_acquired":
			sawLease = true
			continue
		case "session_resolved":
			sawResolution = true
			continue
		case MailboxProjectionPostedEventType, MailboxProjectionPostConsumedEventType, MailboxProjectionDeadLetteredEventType:
		default:
			continue
		}

		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok {
			continue
		}
		switch event.Type {
		case MailboxProjectionPostedEventType:
			if isAllowedProjectionPath(payload.Path) {
				enqueuedAt[pathKey(payload.Path)] = event.OccurredAt
			}
		case MailboxProjectionPostConsumedEventType:
			delete(enqueuedAt, pathKey(payload.Path))
		case MailboxProjectionDeadLetteredEventType:
			delete(enqueuedAt, pathKey(payload.SourcePath))
		}
	}
	if !sawLease || !sawResolution {
		return nil, false, nil
	}
	return enqueuedAt, true, nil
}
