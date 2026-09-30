package ethol

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	items     T
	timestamp time.Time
}

type (
	scheduleCacheEntry     = cacheEntry[[]ScheduleItem]
	taskCacheEntry         = cacheEntry[[]TaskItem]
	materialCacheEntry     = cacheEntry[[]MaterialItem]
	videoCacheEntry        = cacheEntry[[]VideoItem]
	attendanceCacheEntry   = cacheEntry[*AttendanceStats]
	examCacheEntry         = cacheEntry[[]ExamItem]
	announcementCacheEntry = cacheEntry[[]AnnouncementItem]
)

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
	examCache           map[string]examCacheEntry       // ponytail: in-memory map with TTL sufficient for single-user daemon; upgrade to bounded LRU if multi-tenant
	announcementCache   announcementCacheEntry
	processedNotifIDs   map[string]struct{} // ponytail: in-memory set bounded to maxNotifHistory; upgrade to persistent cache if multi-instance
	processedNotifQueue []string
	notifHead           int
	notifBaselineDone   bool
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
		examCache:         make(map[string]examCacheEntry),
		processedNotifIDs: make(map[string]struct{}),
	}
}

func (am *AcademicManager) InvalidateAttendanceCache() {
	am.mu.Lock()
	defer am.mu.Unlock()
	clear(am.attendanceCache)
}

func (am *AcademicManager) SweepExpired() {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.sweepExpiredLocked(time.Now())
}

func sweepMap[K comparable, V any](m map[K]cacheEntry[V], ttl time.Duration, now time.Time) {
	for k, e := range m {
		if now.Sub(e.timestamp) >= ttl {
			delete(m, k)
		}
	}
}

func countValid[K comparable, V any](m map[K]cacheEntry[V], ttl time.Duration, now time.Time) int {
	n := 0
	for _, e := range m {
		if now.Sub(e.timestamp) < ttl {
			n++
		}
	}
	return n
}

func (am *AcademicManager) sweepExpiredLocked(now time.Time) {
	sweepMap(am.scheduleCache, am.ttl, now)
	sweepMap(am.taskCache, am.ttl, now)
	sweepMap(am.materialCache, am.ttl, now)
	sweepMap(am.videoCache, am.ttl, now)
	sweepMap(am.attendanceCache, am.ttl, now)
	sweepMap(am.examCache, am.ttl, now)
	if !am.announcementCache.timestamp.IsZero() && now.Sub(am.announcementCache.timestamp) >= am.ttl {
		am.announcementCache = announcementCacheEntry{}
	}
}

type AcademicCacheStats struct {
	SchedulesCount     int
	TasksCount         int
	MaterialsCount     int
	VideosCount        int
	AttendanceCount    int
	ExamsCount         int
	AnnouncementsCount int
	ProcessedNotifs    int
}

func (am *AcademicManager) CacheStats() AcademicCacheStats {
	am.mu.RLock()
	defer am.mu.RUnlock()
	now := time.Now()

	annCount := 0
	if !am.announcementCache.timestamp.IsZero() && now.Sub(am.announcementCache.timestamp) < am.ttl {
		annCount = len(am.announcementCache.items)
	}
	return AcademicCacheStats{
		SchedulesCount:     countValid(am.scheduleCache, am.ttl, now),
		TasksCount:         countValid(am.taskCache, am.ttl, now),
		MaterialsCount:     countValid(am.materialCache, am.ttl, now),
		VideosCount:        countValid(am.videoCache, am.ttl, now),
		AttendanceCount:    countValid(am.attendanceCache, am.ttl, now),
		ExamsCount:         countValid(am.examCache, am.ttl, now),
		AnnouncementsCount: annCount,
		ProcessedNotifs:    len(am.processedNotifIDs),
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
