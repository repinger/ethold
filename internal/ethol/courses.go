package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Course struct {
	Nomor          int    `json:"nomor"`
	JenisSchema    int    `json:"jenisSchema"`
	KuliahAsal     int    `json:"kuliah_asal"`
	Dosen          string `json:"dosen"`
	NomorDosen     any    `json:"nomor_dosen"`
	NamaMatakuliah any    `json:"nama_matakuliah"`
	Matakuliah     any    `json:"matakuliah"`
}

func parseCourseNameVal(val any) string {
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok && s != "" {
		return s
	}
	if m, ok := val.(map[string]any); ok {
		if name, ok := m["nama"].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

func (c Course) CourseName() string {
	if name := parseCourseNameVal(c.NamaMatakuliah); name != "" {
		return name
	}
	if name := parseCourseNameVal(c.Matakuliah); name != "" {
		return name
	}
	return fmt.Sprintf("Kuliah #%d", c.Nomor)
}

type authConfigResponse struct {
	TahunAktif    any `json:"tahun_aktif"`
	SemesterAktif any `json:"semester_aktif"`
}

type CourseManager struct {
	mu             sync.RWMutex
	refreshMu      sync.Mutex
	client         *http.Client
	baseURL        string
	cache          []Course
	activeYear     int
	activeSemester int
	lastUpdated    time.Time
	ttl            time.Duration
}

func NewCourseManager(client *http.Client, baseURL string, ttl time.Duration) *CourseManager {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &CourseManager{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		ttl:     ttl,
	}
}

func (cm *CourseManager) CachedCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.cache)
}

func (cm *CourseManager) CachedCourses() []Course {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.cache
}

func (cm *CourseManager) CachedActivePeriod() (int, int, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.activeYear > 0 {
		return cm.activeYear, cm.activeSemester, true
	}
	return 0, 0, false
}

func (cm *CourseManager) CacheAge() (time.Duration, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.lastUpdated.IsZero() {
		return 0, false
	}
	return time.Since(cm.lastUpdated), true
}

func (cm *CourseManager) GetCourses(ctx context.Context) ([]Course, error) {
	cm.mu.RLock()
	if len(cm.cache) > 0 && time.Since(cm.lastUpdated) < cm.ttl {
		courses := cm.cache
		cm.mu.RUnlock()
		return courses, nil
	}
	cm.mu.RUnlock()

	return cm.Refresh(ctx)
}

func (cm *CourseManager) Refresh(ctx context.Context) ([]Course, error) {
	start := time.Now()
	cm.refreshMu.Lock()
	defer cm.refreshMu.Unlock()

	cm.mu.RLock()
	if len(cm.cache) > 0 && cm.lastUpdated.After(start) {
		courses := cm.cache
		cm.mu.RUnlock()
		return courses, nil
	}
	cm.mu.RUnlock()

	return cm.refreshLocked(ctx)
}

func (cm *CourseManager) refreshLocked(ctx context.Context) ([]Course, error) {
	// 1. Fetch active academic config
	confURL := cm.baseURL + "/api/auth/config"
	reqConf, err := http.NewRequestWithContext(ctx, http.MethodGet, confURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create auth config req: %w", err)
	}

	respConf, err := cm.client.Do(reqConf)
	if err != nil {
		return nil, fmt.Errorf("fetch auth config: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(respConf.Body, 4096))
		_ = respConf.Body.Close()
	}()

	tahun := 0
	semester := 0
	if respConf.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if respConf.StatusCode == http.StatusOK {
		var conf authConfigResponse
		if err := json.NewDecoder(io.LimitReader(respConf.Body, 64*1024)).Decode(&conf); err == nil {
			tahun = parseCount(conf.TahunAktif)
			semester = parseCount(conf.SemesterAktif)
		}
	}

	// 2. Fetch enrolled courses
	coursesURL := fmt.Sprintf("%s/api/kuliah?tahun=%d&semester=%d", cm.baseURL, tahun, semester)
	reqCourses, err := http.NewRequestWithContext(ctx, http.MethodGet, coursesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create kuliah req: %w", err)
	}

	respCourses, err := cm.client.Do(reqCourses)
	if err != nil {
		return nil, fmt.Errorf("fetch kuliah: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(respCourses.Body, 4096))
		_ = respCourses.Body.Close()
	}()

	if respCourses.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if respCourses.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch kuliah failed: HTTP %d", respCourses.StatusCode)
	}

	var courses []Course
	if err := json.NewDecoder(io.LimitReader(respCourses.Body, 512*1024)).Decode(&courses); err != nil {
		return nil, fmt.Errorf("decode kuliah: %w", err)
	}

	for i := range courses {
		if courses[i].KuliahAsal == 0 {
			courses[i].KuliahAsal = courses[i].Nomor
		}
		if courses[i].Dosen == "" {
			courses[i].Dosen = "Dosen Pengampu"
		}
		name := courses[i].CourseName()
		courses[i].Matakuliah = name
		courses[i].NamaMatakuliah = nil
		if courses[i].NomorDosen != nil {
			courses[i].NomorDosen = parseCount(courses[i].NomorDosen)
		}
	}

	cm.mu.Lock()
	cm.cache = courses
	cm.activeYear = tahun
	cm.activeSemester = semester
	cm.lastUpdated = time.Now()
	cm.mu.Unlock()

	slog.Info("Course cache updated", "count", len(courses), "year", tahun, "semester", semester)
	return courses, nil
}

func (cm *CourseManager) ActivePeriod(ctx context.Context) (int, int, error) {
	cm.mu.RLock()
	if cm.activeYear > 0 && time.Since(cm.lastUpdated) < cm.ttl {
		y, s := cm.activeYear, cm.activeSemester
		cm.mu.RUnlock()
		return y, s, nil
	}
	cm.mu.RUnlock()

	_, err := cm.GetCourses(ctx)
	if err != nil {
		return 0, 0, err
	}
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.activeYear, cm.activeSemester, nil
}
