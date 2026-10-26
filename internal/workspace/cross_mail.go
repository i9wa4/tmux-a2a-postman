package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// SendCrossWithReply publishes a declared interface message between two live
// native sessions. The caller must authorize the exact route from topology
// before invoking this method; both runtimes are reverified under their own
// workspace locks before any durable intent is written.
func (m Mail) SendCrossWithReply(ctx context.Context, source, destination NamedRuntime, body, replyTo, fills string, needsReply bool) (Message, error) {
	var msg Message
	if source.Name() == destination.Name() || source.server != destination.server ||
		source.incarnation != destination.incarnation || source.sessionID == destination.sessionID {
		return msg, ErrUnknown
	}
	err := m.Store.WithVerified(ctx, source, func(sourceWS *os.Root, from Snapshot) error {
		return m.Store.WithVerified(ctx, destination, func(destinationWS *os.Root, to Snapshot) error {
			if from.Root == to.Root || from.Caller.Owner.Session == to.Caller.Owner.Session ||
				from.Caller.Owner.Server != to.Caller.Owner.Server ||
				from.Caller.Owner.Incarnation != to.Caller.Owner.Incarnation {
				return ErrUnknown
			}
			if err := m.recover(destinationWS); err != nil {
				return err
			}
			if err := m.recoverCross(sourceWS, destinationWS, from, to, source.Name(), destination.Name()); err != nil {
				return err
			}
			if replyTo != "" {
				read, err := openOwnBox(sourceWS, from.Caller.Role, "read")
				if err != nil {
					return err
				}
				defer read.Close()
				original, err := readMessage(read, replyTo+".md")
				if err != nil || original.To != from.Caller.Role || original.ToSession != source.Name() ||
					original.From != to.Caller.Role || original.FromSession != destination.Name() {
					return errors.New("reply does not identify a claimed cross-session conversation")
				}
				var receipt ClaimReceipt
				if readJSON(read, replyTo+".pop.json", &receipt) != nil || receipt.Version != 1 ||
					receipt.ID != replyTo || receipt.Role != from.Caller.Role {
					return errors.New("reply source lacks a matching POP receipt")
				}
				if fills != "" && (!original.NeedsReply || original.InputRequestID != fills) {
					return errors.New("reply fills a different request")
				}
			}
			nonce, err := randomName()
			if err != nil {
				return err
			}
			msg = Message{
				ID:   "m-" + strings.TrimPrefix(nonce, ".new-"),
				From: from.Caller.Role, FromSession: source.Name(),
				To: to.Caller.Role, ToSession: destination.Name(),
				ReplyTo: replyTo, FillsInputRequestID: fills,
				NeedsReply: needsReply, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Body: body,
			}
			if needsReply {
				msg.InputRequestID = "ireq_" + strings.TrimPrefix(msg.ID, "m-")
			}
			if err := validateMessage(msg); err != nil {
				return err
			}
			tx := mailTransaction{
				Version: 1, Kind: "cross_send", Message: msg, Body: body,
				SourceRoot: from.Root, DestinationRoot: to.Root,
			}
			raw, err := json.Marshal(tx)
			if err != nil || len(raw)+1 > 60*1024 {
				return errors.New("cross-session intent exceeds durable record limit")
			}
			journal, err := privateDir(sourceWS, "transactions", true)
			if err != nil {
				return err
			}
			defer journal.Close()
			if err := writeJSON(journal, tx.Kind+"-"+msg.ID+".json", tx); err != nil {
				return err
			}
			if err := syncDirectory(journal); err != nil {
				return err
			}
			if err := syncDirectory(sourceWS); err != nil {
				return err
			}
			if err := m.step("cross-intent"); err != nil {
				return err
			}
			return m.finishCross(sourceWS, destinationWS, journal, tx)
		})
	})
	return msg, err
}

// recoverCross only finishes intents for this exact live source/peer pair.
// Another peer's pending intent stays in the journal until that peer is verified.
func (m Mail) recoverCross(sourceWS, destinationWS *os.Root, from, to Snapshot, sourceName, destinationName string) error {
	journal, err := privateDir(sourceWS, "transactions", true)
	if err != nil {
		return err
	}
	defer journal.Close()
	dir, err := journal.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(1025)
	dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if len(entries) > 1024 {
		return errors.New("too many pending mail transactions")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		var tx mailTransaction
		if err := readJSON(journal, entry.Name(), &tx); err != nil {
			return err
		}
		tx.Message.Body = tx.Body
		if tx.Version != 1 || entry.Name() != tx.Kind+"-"+tx.Message.ID+".json" ||
			validateMessage(tx.Message) != nil {
			return errors.New("invalid pending mail transaction")
		}
		if tx.Kind != "cross_send" {
			if tx.Kind != "send" && tx.Kind != "claim" || tx.SourceRoot != "" || tx.DestinationRoot != "" ||
				(tx.Kind == "send" && tx.Message.FromSession != "") {
				return errors.New("invalid local transaction")
			}
			if tx.Kind == "claim" {
				if _, err := time.Parse(time.RFC3339Nano, tx.ClaimedAt); err != nil {
					return err
				}
			}
			if err := m.finish(sourceWS, journal, tx); err != nil {
				return err
			}
			continue
		}
		if !validCrossIntent(tx) || tx.SourceRoot != from.Root {
			return errors.New("invalid pending cross-session intent for source workspace")
		}
		if tx.DestinationRoot != to.Root || tx.Message.FromSession != sourceName ||
			tx.Message.ToSession != destinationName || tx.Message.From != from.Caller.Role ||
			tx.Message.To != to.Caller.Role {
			continue
		}
		if err := m.finishCross(sourceWS, destinationWS, journal, tx); err != nil {
			return err
		}
	}
	return nil
}

func validCrossIntent(tx mailTransaction) bool {
	if tx.Kind != "cross_send" || tx.ClaimedAt != "" || tx.SourceRoot == "" ||
		tx.DestinationRoot == "" || tx.SourceRoot == tx.DestinationRoot ||
		tx.Message.FromSession == "" || tx.Message.ToSession == "" {
		return false
	}
	_, sourceErr := Name(tx.SourceRoot)
	_, destinationErr := Name(tx.DestinationRoot)
	return sourceErr == nil && destinationErr == nil
}

func (m Mail) finishCross(sourceWS, destinationWS, journal *os.Root, tx mailTransaction) error {
	msg := tx.Message
	inbox, err := mailBox(destinationWS, msg.To, "inbox")
	if err != nil {
		return err
	}
	defer inbox.Close()
	read, err := mailBox(destinationWS, msg.To, "read")
	if err != nil {
		return err
	}
	defer read.Close()
	archived, readErr := readMessage(read, msg.ID+".md")
	if readErr == nil {
		if !sameMessage(archived, msg) {
			return errors.New("archived cross-session message conflicts with intent")
		}
		if _, err := inbox.Lstat(msg.ID + ".md"); !errors.Is(err, os.ErrNotExist) {
			return errors.New("cross-session delivery exists in inbox and read")
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		if err := ensureMessage(inbox, msg); err != nil {
			return err
		}
	} else {
		return readErr
	}
	if err := m.step("cross-inbox"); err != nil {
		return err
	}
	sent, err := mailBox(sourceWS, msg.From, "sent")
	if err != nil {
		return err
	}
	defer sent.Close()
	if err := ensureMessage(sent, msg); err != nil {
		return err
	}
	if err := m.step("cross-sent"); err != nil {
		return err
	}
	if err := journal.Remove(tx.Kind + "-" + msg.ID + ".json"); err != nil {
		return err
	}
	return syncDirectory(journal)
}
