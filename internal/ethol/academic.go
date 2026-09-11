package ethol

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type scheduleCacheEntry struct {
	items     []ScheduleItem
	timestamp time.Time
}

type taskCacheEntry struct {
	items     []TaskItem
	timestamp time.Time
}

type materialCacheEntry struct {
	items     []MaterialItem
	timestamp time.Time
}

type videoCacheEntry struct {
	items     []VideoItem
	timestamp time.Time
}

type attendanceCacheEntry struct {
	stats     *AttendanceStats
	timestamp time.Time
}

type AcademicManager struct {
	mu                  sync.RWMutex
	client              *http.Client
	baseURL             string
	ttl                 time.Duration
	scheduleCache       map[string]scheduleCacheEntry   // ponytail: in-memory map with TTL sufficient for single-user daemon; upgrade to bounded LRU if multi-tenant
	taskCache           map[int]taskCacheEntry          // ponytail: in-memory map with TTL sufficient for single-user daemon; upgrade to bounded LRU if multi-tenant
	materialCache       map[int]materialCacheEntry      // ponytail: in-memory map with TTL sufficient for single-user daemon; upgrade to bounded LRU if multi-tenant
	videoCache          map[int]videoCacheEntry         // ponytail: in-memory map with TTL sufficient for single-user daemon; upgrade to bounded LRU if multi-tenant
	attendanceCache     map[string]attendanceCacheEntry // ponytail: in-memory map with TTL sufficient for single-user daemon; upgrade to bounded LRU if multi-tenant
	processedNotifIDs   map[int]struct{}                // ponytail: in-memory set bounded to maxNotifHistory; upgrade to persistent cache if multi-instance
	processedNotifQueue []int
}

const (
	maxNotifHistory        = 1000
	maxAcademicConcurrency = 6
)

func NewAcademicManager(client *http.Client, baseURL string, ttl time.Duration) *AcademicManager {
	if client == nil {
		client = http.DefaultClient
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &AcademicManager{
		client:            client,
		baseURL:           strings.TrimRight(baseURL, "/"),
		ttl:               ttl,
		scheduleCache:     make(map[string]scheduleCacheEntry),
		taskCache:         make(map[int]taskCacheEntry),
		materialCache:     make(map[int]materialCacheEntry),
		videoCache:        make(map[int]videoCacheEntry),
		attendanceCache:   make(map[string]attendanceCacheEntry),
		processedNotifIDs: make(map[int]struct{}),
	}
}

func (am *AcademicManager) InvalidateTasksCache() {
	am.mu.Lock()
	defer am.mu.Unlock()
	clear(am.taskCache)
}

func (am *AcademicManager) InvalidateMaterialsCache() {
	am.mu.Lock()
	defer am.mu.Unlock()
	clear(am.materialCache)
	clear(am.videoCache)
}

func (am *AcademicManager) InvalidateAttendanceCache() {
	am.mu.Lock()
	defer am.mu.Unlock()
	clear(am.attendanceCache)
}

type AcademicCacheStats struct {
	SchedulesCount  int
	TasksCount      int
	MaterialsCount  int
	VideosCount     int
	AttendanceCount int
	ProcessedNotifs int
}

func (am *AcademicManager) CacheStats() AcademicCacheStats {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return AcademicCacheStats{
		SchedulesCount:  len(am.scheduleCache),
		TasksCount:      len(am.taskCache),
		MaterialsCount:  len(am.materialCache),
		VideosCount:     len(am.videoCache),
		AttendanceCount: len(am.attendanceCache),
		ProcessedNotifs: len(am.processedNotifIDs),
	}
}

func parseCount(v any) int {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(val))
		return n
	default:
		return 0
	}
}
