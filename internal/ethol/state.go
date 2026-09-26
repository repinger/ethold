package ethol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultRetentionDays is the default duration for state record retention (approx 1 semester).
const DefaultRetentionDays = 90

type PresenceRecord struct {
	Key        string `json:"key"`
	CourseName string `json:"course_name,omitempty"`
	Dosen      string `json:"dosen,omitempty"`
	Time       string `json:"time,omitempty"`
	Date       string `json:"date,omitempty"`
}

type stateFile struct {
	AttendedKeys []string                  `json:"attended_keys"`
	Records      map[string]PresenceRecord `json:"records,omitempty"`
	LastUpdated  string                    `json:"last_updated"`
}

type StateManager struct {
	mu            sync.RWMutex
	path          string
	retentionDays int
	records       map[string]PresenceRecord
	keyList       []string
	recs          map[string]PresenceRecord
	bw            *bufio.Writer
}

func parseRecordDate(key string, rec PresenceRecord) (time.Time, bool) {
	if rec.Date != "" {
		if t, err := time.Parse("2006-01-02", rec.Date); err == nil {
			return t, true
		}
	}
	if len(key) >= 10 && key[4] == '-' && key[7] == '-' {
		if t, err := time.Parse("2006-01-02", key[:10]); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func NewStateManager(path string, retentionDays ...int) (*StateManager, error) {
	retention := DefaultRetentionDays
	if len(retentionDays) > 0 {
		retention = retentionDays[0]
	}

	sm := &StateManager{
		path:          path,
		retentionDays: retention,
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
			sm.records = make(map[string]PresenceRecord)
			return sm, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		sm.records = make(map[string]PresenceRecord)
		return sm, nil
	}

	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse state file: %w", err)
	}

	capacity := len(sf.AttendedKeys)
	if len(sf.Records) > capacity {
		capacity = len(sf.Records)
	}
	sm.records = make(map[string]PresenceRecord, capacity)

	for k, rec := range sf.Records {
		sm.records[k] = rec
	}
	for _, k := range sf.AttendedKeys {
		if _, ok := sm.records[k]; !ok {
			sm.records[k] = PresenceRecord{Key: k}
		}
	}

	if sm.retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -sm.retentionDays)
		sm.pruneLocked(cutoff)
	}

	return sm, nil
}

func (sm *StateManager) Path() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.path
}

func (sm *StateManager) pruneLocked(cutoff time.Time) int {
	pruned := 0
	for k, rec := range sm.records {
		if recDate, ok := parseRecordDate(k, rec); ok && recDate.Before(cutoff) {
			delete(sm.records, k)
			pruned++
		}
	}
	return pruned
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

func (sm *StateManager) CountWithPrefix(prefix string) int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := 0
	for k := range sm.records {
		if strings.HasPrefix(k, prefix) {
			count++
		}
	}
	return count
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
	slices.SortFunc(matched, func(a, b PresenceRecord) int {
		return strings.Compare(a.Key, b.Key)
	})
	return matched
}

// AddRecord adds presence records with course metadata.
// ponytail: flat JSON state; upgrade to SQLite when state exceeds 10k items.
func (sm *StateManager) AddRecord(recs ...PresenceRecord) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	nowDate := time.Now().Format("2006-01-02")
	for _, rec := range recs {
		if rec.Date == "" {
			if t, ok := parseRecordDate(rec.Key, rec); ok {
				rec.Date = t.Format("2006-01-02")
			} else {
				rec.Date = nowDate
			}
		}
		sm.records[rec.Key] = rec
	}

	return sm.saveLocked()
}

func (sm *StateManager) saveLocked() error {
	if sm.retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -sm.retentionDays)
		sm.pruneLocked(cutoff)
	}

	sm.keyList = sm.keyList[:0]
	if sm.recs == nil {
		sm.recs = make(map[string]PresenceRecord)
	} else {
		clear(sm.recs)
	}
	for k, rec := range sm.records {
		sm.keyList = append(sm.keyList, k)
		if rec.CourseName != "" || rec.Dosen != "" || rec.Time != "" {
			sm.recs[k] = rec
		}
	}
	slices.Sort(sm.keyList)

	sf := stateFile{
		AttendedKeys: sm.keyList,
		Records:      sm.recs,
		LastUpdated:  time.Now().Format(time.RFC3339),
	}

	dir := filepath.Dir(sm.path)
	tmpFile, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmpFile.Name()

	if sm.bw == nil {
		sm.bw = bufio.NewWriterSize(tmpFile, 8192)
	} else {
		sm.bw.Reset(tmpFile)
	}
	enc := json.NewEncoder(sm.bw)
	if err := enc.Encode(sf); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		sm.bw.Reset(nil)
		return fmt.Errorf("encode state file: %w", err)
	}

	if err := sm.bw.Flush(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		sm.bw.Reset(nil)
		return fmt.Errorf("flush temp state file: %w", err)
	}
	sm.bw.Reset(nil)

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp state file: %w", err)
	}

	if err := os.Rename(tmpName, sm.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("atomic rename state file: %w", err)
	}

	return nil
}
