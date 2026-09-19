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

type examCacheEntry struct {
	items     []ExamItem
	timestamp time.Time
}

type announcementCacheEntry struct {
	items     []AnnouncementItem
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

func (am *AcademicManager) sweepExpiredLocked(now time.Time) {
	for k, e := range am.scheduleCache {
		if now.Sub(e.timestamp) >= am.ttl {
			delete(am.scheduleCache, k)
		}
	}
	for k, e := range am.taskCache {
		if now.Sub(e.timestamp) >= am.ttl {
			delete(am.taskCache, k)
		}
	}
	for k, e := range am.materialCache {
		if now.Sub(e.timestamp) >= am.ttl {
			delete(am.materialCache, k)
		}
	}
	for k, e := range am.videoCache {
		if now.Sub(e.timestamp) >= am.ttl {
			delete(am.videoCache, k)
		}
	}
	for k, e := range am.attendanceCache {
		if now.Sub(e.timestamp) >= am.ttl {
			delete(am.attendanceCache, k)
		}
	}
	for k, e := range am.examCache {
		if now.Sub(e.timestamp) >= am.ttl {
			delete(am.examCache, k)
		}
	}
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

	schedCount := 0
	for _, e := range am.scheduleCache {
		if now.Sub(e.timestamp) < am.ttl {
			schedCount++
		}
	}
	taskCount := 0
	for _, e := range am.taskCache {
		if now.Sub(e.timestamp) < am.ttl {
			taskCount++
		}
	}
	matCount := 0
	for _, e := range am.materialCache {
		if now.Sub(e.timestamp) < am.ttl {
			matCount++
		}
	}
	vidCount := 0
	for _, e := range am.videoCache {
		if now.Sub(e.timestamp) < am.ttl {
			vidCount++
		}
	}
	attCount := 0
	for _, e := range am.attendanceCache {
		if now.Sub(e.timestamp) < am.ttl {
			attCount++
		}
	}
	examCount := 0
	for _, e := range am.examCache {
		if now.Sub(e.timestamp) < am.ttl {
			examCount++
		}
	}
	annCount := 0
	if !am.announcementCache.timestamp.IsZero() && now.Sub(am.announcementCache.timestamp) < am.ttl {
		annCount = len(am.announcementCache.items)
	}
	return AcademicCacheStats{
		SchedulesCount:     schedCount,
		TasksCount:         taskCount,
		MaterialsCount:     matCount,
		VideosCount:        vidCount,
		AttendanceCount:    attCount,
		ExamsCount:         examCount,
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
