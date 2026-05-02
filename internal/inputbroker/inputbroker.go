package inputbroker

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

const DefaultLeaseTTL = 2 * time.Minute

type Request struct {
	PaneID   string
	NodeName string
	Owner    string
	TTL      time.Duration
}

type Broker struct {
	sessionDir string
	now        func() time.Time
}

type Lease struct {
	path  string
	token string
}

type leaseRecord struct {
	PaneID     string `json:"pane_id"`
	NodeName   string `json:"node_name,omitempty"`
	Owner      string `json:"owner,omitempty"`
	Token      string `json:"token"`
	AcquiredAt string `json:"acquired_at"`
	ExpiresAt  string `json:"expires_at"`
}

func New(sessionDir string) *Broker {
	return NewWithClock(sessionDir, time.Now)
}

func NewWithClock(sessionDir string, now func() time.Time) *Broker {
	if now == nil {
		now = time.Now
	}
	return &Broker{sessionDir: sessionDir, now: now}
}

func (b *Broker) Acquire(req Request) (Lease, bool, error) {
	if b == nil || b.sessionDir == "" {
		return Lease{}, true, nil
	}
	if req.PaneID == "" {
		return Lease{}, false, fmt.Errorf("pane id is required")
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}

	lockDir := filepath.Join(b.sessionDir, "input-locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return Lease{}, false, fmt.Errorf("creating input lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, lockFilename(req.PaneID))
	now := b.now().UTC()

	if current, ok := readLeaseRecord(lockPath); ok {
		if expiresAt, err := time.Parse(time.RFC3339Nano, current.ExpiresAt); err == nil && expiresAt.After(now) {
			return Lease{}, false, nil
		}
		_ = os.Remove(lockPath)
	} else if _, err := os.Stat(lockPath); err == nil {
		_ = os.Remove(lockPath)
	}

	token, err := randomToken()
	if err != nil {
		return Lease{}, false, err
	}
	record := leaseRecord{
		PaneID:     req.PaneID,
		NodeName:   req.NodeName,
		Owner:      req.Owner,
		Token:      token,
		AcquiredAt: now.Format(time.RFC3339Nano),
		ExpiresAt:  now.Add(ttl).Format(time.RFC3339Nano),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Lease{}, false, err
	}
	data = append(data, '\n')

	file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return Lease{}, false, nil
		}
		return Lease{}, false, fmt.Errorf("creating input lock: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(lockPath)
		return Lease{}, false, fmt.Errorf("writing input lock: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(lockPath)
		return Lease{}, false, fmt.Errorf("closing input lock: %w", err)
	}

	return Lease{path: lockPath, token: token}, true, nil
}

func (l Lease) Release() error {
	if l.path == "" || l.token == "" {
		return nil
	}
	record, ok := readLeaseRecord(l.path)
	if !ok || record.Token != l.token {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ActiveLocks(sessionDir string, now time.Time) ([]status.InputLock, error) {
	if sessionDir == "" {
		return []status.InputLock{}, nil
	}
	lockDir := filepath.Join(sessionDir, "input-locks")
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []status.InputLock{}, nil
		}
		return nil, err
	}

	now = now.UTC()
	locks := make([]status.InputLock, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(lockDir, entry.Name())
		record, ok := readLeaseRecord(path)
		if !ok {
			_ = os.Remove(path)
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
		if err != nil || !expiresAt.After(now) {
			_ = os.Remove(path)
			continue
		}
		locks = append(locks, status.InputLock{
			PaneID:    record.PaneID,
			NodeName:  record.NodeName,
			Owner:     record.Owner,
			ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
		})
	}

	sort.Slice(locks, func(i, j int) bool {
		if locks[i].PaneID != locks[j].PaneID {
			return locks[i].PaneID < locks[j].PaneID
		}
		return locks[i].Owner < locks[j].Owner
	})
	return locks, nil
}

func readLeaseRecord(path string) (leaseRecord, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return leaseRecord{}, false
	}
	var record leaseRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return leaseRecord{}, false
	}
	if record.PaneID == "" || record.Token == "" || record.ExpiresAt == "" {
		return leaseRecord{}, false
	}
	return record, true
}

func lockFilename(paneID string) string {
	return hex.EncodeToString([]byte(paneID)) + ".json"
}

func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generating input lock token: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}
