package ethol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateManager(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "attended_keys.json")

	sm, err := NewStateManager(statePath)
	if err != nil {
		t.Fatalf("new state manager: %v", err)
	}

	if sm.Has("key1") {
		t.Fatal("expected key1 to be absent")
	}

	if err := sm.Add("key1", "key2"); err != nil {
		t.Fatalf("add keys: %v", err)
	}

	if !sm.Has("key1") || !sm.Has("key2") {
		t.Fatal("expected key1 and key2 to be present")
	}
	if sm.Count() != 2 {
		t.Fatalf("expected count 2, got %d", sm.Count())
	}

	// Reload from disk
	sm2, err := NewStateManager(statePath)
	if err != nil {
		t.Fatalf("reload state manager: %v", err)
	}
	if !sm2.Has("key1") || !sm2.Has("key2") {
		t.Fatal("persisted keys not found after reload")
	}

	if err := sm2.Add("2026-09-10_abc", "2026-09-10_def", "2026-09-09_xyz"); err != nil {
		t.Fatalf("add prefixed keys: %v", err)
	}

	todayKeys := sm2.KeysWithPrefix("2026-09-10_")
	if len(todayKeys) != 2 {
		t.Fatalf("expected 2 keys with prefix, got %d", len(todayKeys))
	}
	if todayKeys[0] != "2026-09-10_abc" || todayKeys[1] != "2026-09-10_def" {
		t.Fatalf("unexpected keys: %v", todayKeys)
	}
}

func TestStateManager_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested", "sub", "attended_keys.json")

	sm, err := NewStateManager(nested)
	if err != nil {
		t.Fatalf("expected nested directory to be created: %v", err)
	}
	if err := sm.Add("test-key"); err != nil {
		t.Fatalf("failed to add key: %v", err)
	}
}

func TestStateManager_Records(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "attended_keys.json")

	sm, err := NewStateManager(statePath)
	if err != nil {
		t.Fatalf("new state manager: %v", err)
	}

	rec := PresenceRecord{
		Key:        "2026-09-10_key1",
		CourseName: "Pemrograman Web",
		Dosen:      "Dr. Tech",
		Time:       "08:30",
	}

	if err := sm.AddRecord(rec); err != nil {
		t.Fatalf("add record: %v", err)
	}

	if !sm.Has("2026-09-10_key1") {
		t.Fatal("expected key to exist")
	}

	recs := sm.RecordsWithPrefix("2026-09-10_")
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].CourseName != "Pemrograman Web" || recs[0].Dosen != "Dr. Tech" {
		t.Fatalf("unexpected record details: %+v", recs[0])
	}

	// Reload and verify persistence
	sm2, err := NewStateManager(statePath)
	if err != nil {
		t.Fatalf("reload state manager: %v", err)
	}
	recs2 := sm2.RecordsWithPrefix("2026-09-10_")
	if len(recs2) != 1 || recs2[0].CourseName != "Pemrograman Web" {
		t.Fatalf("unexpected reloaded records: %+v", recs2)
	}
}

func TestStateManager_LegacyMigration(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "legacy.json")

	// Write old-style JSON without records field
	oldJSON := `{"attended_keys":["legacy-key","2026-09-10_legacy"],"last_updated":"2026-09-10T00:00:00Z"}`
	if err := os.WriteFile(statePath, []byte(oldJSON), 0644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	sm, err := NewStateManager(statePath)
	if err != nil {
		t.Fatalf("load legacy state: %v", err)
	}

	if !sm.Has("legacy-key") || !sm.Has("2026-09-10_legacy") {
		t.Fatal("expected legacy keys to exist")
	}

	recs := sm.RecordsWithPrefix("2026-09-10_")
	if len(recs) != 1 {
		t.Fatalf("expected 1 legacy record, got %d", len(recs))
	}
	if recs[0].Key != "2026-09-10_legacy" || recs[0].CourseName != "" {
		t.Fatalf("unexpected legacy record: %+v", recs[0])
	}
}

func TestStateManager_CountWithPrefix(t *testing.T) {
	sm := &StateManager{
		records: make(map[string]PresenceRecord),
	}
	sm.records["2026-09-11_a"] = PresenceRecord{Key: "2026-09-11_a"}
	sm.records["2026-09-11_b"] = PresenceRecord{Key: "2026-09-11_b"}
	sm.records["2026-09-10_c"] = PresenceRecord{Key: "2026-09-10_c"}

	if cnt := sm.CountWithPrefix("2026-09-11_"); cnt != 2 {
		t.Fatalf("expected 2, got %d", cnt)
	}
	if cnt := sm.CountWithPrefix("2026-09-12_"); cnt != 0 {
		t.Fatalf("expected 0, got %d", cnt)
	}
}

func BenchmarkStateManager_KeysWithPrefix(b *testing.B) {
	sm := &StateManager{
		records: make(map[string]PresenceRecord),
	}
	for i := 0; i < 100; i++ {
		k := "2026-09-11_" + string(rune('A'+i))
		sm.records[k] = PresenceRecord{Key: k}
	}
	for i := 0; i < 50; i++ {
		k := "2026-09-10_" + string(rune('A'+i))
		sm.records[k] = PresenceRecord{Key: k}
	}
	b.ResetTimer()
	for b.Loop() {
		_ = sm.KeysWithPrefix("2026-09-11_")
	}
}

func BenchmarkStateManager_CountWithPrefix(b *testing.B) {
	sm := &StateManager{
		records: make(map[string]PresenceRecord),
	}
	for i := 0; i < 100; i++ {
		k := "2026-09-11_" + string(rune('A'+i))
		sm.records[k] = PresenceRecord{Key: k}
	}
	for i := 0; i < 50; i++ {
		k := "2026-09-10_" + string(rune('A'+i))
		sm.records[k] = PresenceRecord{Key: k}
	}
	b.ResetTimer()
	for b.Loop() {
		_ = sm.CountWithPrefix("2026-09-11_")
	}
}

