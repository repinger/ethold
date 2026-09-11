package ethol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type PresenceRecord struct {
	Key        string `json:"key"`
	CourseName string `json:"course_name,omitempty"`
	Dosen      string `json:"dosen,omitempty"`
	Time       string `json:"time,omitempty"`
}

type stateFile struct {
	AttendedKeys []string                  `json:"attended_keys"`
	Records      map[string]PresenceRecord `json:"records,omitempty"`
	LastUpdated  string                    `json:"last_updated"`
}

type StateManager struct {
	mu      sync.RWMutex
	path    string
	records map[string]PresenceRecord
}

func NewStateManager(path string) (*StateManager, error) {
	sm := &StateManager{
		path:    path,
		records: make(map[string]PresenceRecord),
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("ensure state directory: %w", err)
		}
	}

	probeFile, err := os.CreateTemp(dir, ".state-probe-*")
	if err != nil {
		return nil, fmt.Errorf("state directory not writable: %w", err)
	}
	probeName := probeFile.Name()
	_ = probeFile.Close()
	_ = os.Remove(probeName)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sm, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return sm, nil
	}

	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}

	for _, k := range sf.AttendedKeys {
		sm.records[k] = PresenceRecord{Key: k}
	}
	for k, rec := range sf.Records {
		sm.records[k] = rec
	}

	return sm, nil
}

func (sm *StateManager) Path() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.path
}

func (sm *StateManager) Has(key string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.records[key]
	return ok
}

func (sm *StateManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.records)
}

func (sm *StateManager) KeysWithPrefix(prefix string) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var matched []string
	for k := range sm.records {
		if strings.HasPrefix(k, prefix) {
			matched = append(matched, k)
		}
	}
	sort.Strings(matched)
	return matched
}

// RecordsWithPrefix returns all presence records whose key starts with prefix, sorted by key.
func (sm *StateManager) RecordsWithPrefix(prefix string) []PresenceRecord {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var matched []PresenceRecord
	for k, rec := range sm.records {
		if strings.HasPrefix(k, prefix) {
			matched = append(matched, rec)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Key < matched[j].Key
	})
	return matched
}

func (sm *StateManager) Add(keys ...string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, k := range keys {
		if _, exists := sm.records[k]; !exists {
			sm.records[k] = PresenceRecord{Key: k}
		}
	}

	return sm.saveLocked()
}

// AddRecord adds presence records with course metadata.
// ponytail: flat JSON state; upgrade to SQLite when state exceeds 10k items.
func (sm *StateManager) AddRecord(recs ...PresenceRecord) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for _, rec := range recs {
		sm.records[rec.Key] = rec
	}

	return sm.saveLocked()
}

func (sm *StateManager) saveLocked() error {
	keyList := make([]string, 0, len(sm.records))
	recs := make(map[string]PresenceRecord)
	for k, rec := range sm.records {
		keyList = append(keyList, k)
		if rec.CourseName != "" || rec.Dosen != "" || rec.Time != "" {
			recs[k] = rec
		}
	}
	sort.Strings(keyList)

	sf := stateFile{
		AttendedKeys: keyList,
		Records:      recs,
		LastUpdated:  time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	dir := filepath.Dir(sm.path)
	tmpFile, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp state file: %w", err)
	}

	if err := os.Rename(tmpName, sm.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("atomic rename state file: %w", err)
	}

	return nil
}
