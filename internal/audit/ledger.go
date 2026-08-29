// Package audit implements append-only audit records with a hash chain.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Event struct {
	Sequence      int64          `json:"sequence"`
	TenantID      string         `json:"tenant_id"`
	WorkspaceID   string         `json:"workspace_id"`
	ActorType     string         `json:"actor_type"`
	ActorID       string         `json:"actor_id"`
	Action        string         `json:"action"`
	CorrelationID string         `json:"correlation_id"`
	Payload       map[string]any `json:"payload"`
	PreviousHash  string         `json:"previous_hash,omitempty"`
	ContentHash   string         `json:"content_hash"`
	CreatedAt     time.Time      `json:"created_at"`
}

type Ledger struct {
	mu     sync.RWMutex
	events []Event
	path   string
}

func NewLedger() *Ledger { return &Ledger{} }

func NewPersistentLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path}
	if path == "" {
		return l, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read audit state: %w", err)
	}
	if err := json.Unmarshal(data, &l.events); err != nil {
		return nil, fmt.Errorf("decode audit state: %w", err)
	}
	if err := l.Verify(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Ledger) Flush() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.path == "" {
		return nil
	}
	data, err := json.Marshal(l.events)
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-audit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, l.path)
}
func (l *Ledger) Append(input Event) (Event, error) {
	if input.TenantID == "" || input.ActorID == "" || input.Action == "" {
		return Event{}, errors.New("tenant_id, actor_id and action are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	input.Sequence = int64(len(l.events) + 1)
	input.CreatedAt = time.Now().UTC()
	if len(l.events) > 0 {
		input.PreviousHash = l.events[len(l.events)-1].ContentHash
	}
	payload, _ := json.Marshal(struct {
		Sequence                                                         int64 `json:"sequence"`
		TenantID, WorkspaceID, ActorType, ActorID, Action, CorrelationID string
		Payload                                                          map[string]any
		PreviousHash                                                     string `json:"previous_hash"`
		CreatedAt                                                        time.Time
	}{input.Sequence, input.TenantID, input.WorkspaceID, input.ActorType, input.ActorID, input.Action, input.CorrelationID, input.Payload, input.PreviousHash, input.CreatedAt})
	sum := sha256.Sum256(payload)
	input.ContentHash = hex.EncodeToString(sum[:])
	l.events = append(l.events, input)
	_ = l.persistLocked()
	return input, nil
}

func (l *Ledger) persistLocked() error {
	if l.path == "" {
		return nil
	}
	data, err := json.Marshal(l.events)
	if err != nil {
		return err
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adro-audit-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, l.path)
}
func (l *Ledger) List() []Event {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Event(nil), l.events...)
}
func (l *Ledger) Verify() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var previous string
	for i, e := range l.events {
		if e.Sequence != int64(i+1) || e.PreviousHash != previous {
			return fmt.Errorf("audit chain broken at sequence %d", e.Sequence)
		}
		previous = e.ContentHash
	}
	return nil
}
