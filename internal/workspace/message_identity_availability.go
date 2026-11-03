package workspace

import (
	"errors"
	"os"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/messageid"
)

// availableMailIdentity retries only before the candidate appears in any
// durable mail or execution namespace. Callers hold the verified workspace
// lock for every workspace supplied here, through their first durable write.
func availableMailIdentity(generate func() (mailIdentity, error), workspaces ...*os.Root) (mailIdentity, error) {
	if len(workspaces) == 0 {
		return mailIdentity{}, ErrUnknown
	}
	for range 64 {
		candidate, err := generate()
		if err != nil {
			return mailIdentity{}, err
		}
		if !messageid.Valid(candidate.ID) || len(messageid.Suffix(candidate.ID)) != 6 {
			return mailIdentity{}, ErrUnknown
		}
		occupied := false
		for _, ws := range workspaces {
			if ws == nil {
				return mailIdentity{}, ErrUnknown
			}
			found, err := mailIdentityExists(ws, candidate.ID)
			if err != nil {
				return mailIdentity{}, err
			}
			occupied = occupied || found
		}
		if !occupied {
			return candidate, nil
		}
	}
	return mailIdentity{}, errors.New("unable to allocate a unique mail identity")
}

func newAvailableMailIdentity(workspaces ...*os.Root) (mailIdentity, error) {
	return availableMailIdentity(newMailIdentity, workspaces...)
}

func mailIdentityExists(ws *os.Root, id string) (bool, error) {
	agents, err := privateDir(ws, "agents", false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if agents != nil {
		defer func() { _ = agents.Close() }()
		roles, err := boundedEntries(agents)
		if err != nil {
			return false, err
		}
		for _, role := range roles {
			agent, err := privateDir(agents, role.Name(), false)
			if err != nil {
				return false, err
			}
			for _, boxName := range []string{"draft", "inbox", "read", "sent", "failed", "executions"} {
				box, err := privateDir(agent, boxName, false)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					_ = agent.Close()
					return false, err
				}
				name := id + ".md"
				if boxName == "executions" {
					name = id + ".json"
				}
				_, statErr := box.Lstat(name)
				if statErr == nil {
					_ = box.Close()
					_ = agent.Close()
					return true, nil
				}
				if !errors.Is(statErr, os.ErrNotExist) {
					_ = box.Close()
					_ = agent.Close()
					return false, statErr
				}
				if boxName == "executions" {
					found, err := decisionIdentityExists(box, id)
					if err != nil || found {
						_ = box.Close()
						_ = agent.Close()
						return found, err
					}
				}
				_ = box.Close()
			}
			_ = agent.Close()
		}
	}
	journal, err := privateDir(ws, "transactions", false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = journal.Close() }()
	entries, err := boundedEntries(journal)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "-"+id+".json") {
			return true, nil
		}
	}
	return false, nil
}

// A decision reserves its outgoing message ID before mail staging begins.
// Its filename is the request ID, so inspect the immutable payload as well.
func decisionIdentityExists(executions *os.Root, id string) (bool, error) {
	decisions, err := privateDir(executions, "decisions", false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = decisions.Close() }()
	entries, err := boundedEntries(decisions)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		requestID := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Name() != requestID+".json" || !messageID(requestID) {
			return false, ErrUnknown
		}
		var reservation struct {
			MessageID string           `json:"message_id"`
			Decision  ApprovalDecision `json:"decision"`
		}
		if err := readJSON(decisions, entry.Name(), &reservation); err != nil {
			return false, err
		}
		if !messageID(reservation.MessageID) {
			return false, ErrUnknown
		}
		if reservation.MessageID == id {
			return true, nil
		}
	}
	return false, nil
}
