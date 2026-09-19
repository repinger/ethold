package ethol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ScannerStatus struct {
	StartTime        time.Time
	LastScanTime     time.Time
	LastScanDuration time.Duration
	LastScanErr      error
	LastAttended     int
	ScanPlan         ScanPlan
	TotalAttended    int
	TodayAttended    int
	Paused           bool
	WorkerCount      int
	MinDelay         time.Duration
	MaxDelay         time.Duration
	MinStagger       time.Duration
	MaxStagger       time.Duration
	User             *UserInfo
	ActiveCourse     string
	CourseCount      int
	Goroutines       int
}

type commandState struct {
	mu             sync.Mutex
	lastRelogin    time.Time
	lastRosterTime time.Time
	lastRosterMsg  string
}

type Scanner struct {
	auth         *AuthManager
	courses      *CourseManager
	presence     *PresenceEngine
	academic     *AcademicManager
	state        *StateManager
	notifier     *TelegramNotifier
	concurr      int
	lastMode     string
	startTime    time.Time
	scanMu       sync.Mutex
	statusMu     sync.RWMutex
	lastScanTime time.Time
	lastScanDur  time.Duration
	lastScanErr  error
	lastAttended int
	currentPlan  ScanPlan
	paused       atomic.Bool
	minDelay     time.Duration
	maxDelay     time.Duration
	delayMu      sync.RWMutex
	minStagger   time.Duration
	maxStagger   time.Duration
	staggerMu    sync.RWMutex
	cmdState     commandState
	serverDown   atomic.Bool
	authFailed   atomic.Bool
}

func NewScanner(
	auth *AuthManager,
	courses *CourseManager,
	presence *PresenceEngine,
	academic *AcademicManager,
	state *StateManager,
	notifier *TelegramNotifier,
	concurrency int,
) *Scanner {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Scanner{
		auth:       auth,
		courses:    courses,
		presence:   presence,
		academic:   academic,
		state:      state,
		notifier:   notifier,
		concurr:    concurrency,
		startTime:  time.Now(),
		minDelay:   2 * time.Second,
		maxDelay:   10 * time.Second,
		minStagger: 50 * time.Millisecond,
		maxStagger: 250 * time.Millisecond,
	}
}

func (s *Scanner) Status() ScannerStatus {
	s.statusMu.RLock()
	lastScanTime := s.lastScanTime
	lastScanDur := s.lastScanDur
	lastScanErr := s.lastScanErr
	lastAttended := s.lastAttended
	plan := s.currentPlan
	s.statusMu.RUnlock()

	if plan.Interval == 0 {
		plan = NextScanPlan(NowWIB(), nil)
	}

	minD, maxD := s.PresenceDelay()
	minS, maxS := s.WorkerStagger()

	totalAttended := 0
	todayAttended := 0
	if s.state != nil {
		totalAttended = s.state.Count()
		todayAttended = s.state.CountWithPrefix(TodayDate(NowWIB()))
	}

	var user *UserInfo
	if s.auth != nil {
		user = s.auth.User()
	}

	courseCount := 0
	activeCourse := "Tidak ada"
	if s.courses != nil {
		courseCount = s.courses.CachedCount()
		if s.academic != nil {
			if y, sem, ok := s.courses.CachedActivePeriod(); ok {
				cachedCourses := s.courses.CachedCourses()
				if active := s.academic.CachedActiveCourse(NowWIB(), y, sem, cachedCourses); active != nil {
					activeCourse = active.CourseName()
				}
			}
		}
	}

	return ScannerStatus{
		StartTime:        s.startTime,
		LastScanTime:     lastScanTime,
		LastScanDuration: lastScanDur,
		LastScanErr:      lastScanErr,
		LastAttended:     lastAttended,
		ScanPlan:         plan,
		TotalAttended:    totalAttended,
		TodayAttended:    todayAttended,
		Paused:           s.paused.Load(),
		WorkerCount:      s.concurr,
		MinDelay:         minD,
		MaxDelay:         maxD,
		MinStagger:       minS,
		MaxStagger:       maxS,
		User:             user,
		ActiveCourse:     activeCourse,
		CourseCount:      courseCount,
		Goroutines:       runtime.NumGoroutine(),
	}
}

func (s *Scanner) PresenceDelay() (time.Duration, time.Duration) {
	s.delayMu.RLock()
	defer s.delayMu.RUnlock()
	return s.minDelay, s.maxDelay
}

func (s *Scanner) calculateDelay() time.Duration {
	s.delayMu.RLock()
	minD, maxD := s.minDelay, s.maxDelay
	s.delayMu.RUnlock()

	if maxD <= 0 {
		return 0
	}
	if maxD <= minD {
		return minD
	}
	diff := maxD - minD
	// ponytail: uniform random jitter; upgrade to configurable distribution if required.
	return minD + time.Duration(rand.Int64N(int64(diff)+1))
}

func (s *Scanner) WorkerStagger() (time.Duration, time.Duration) {
	s.staggerMu.RLock()
	defer s.staggerMu.RUnlock()
	return s.minStagger, s.maxStagger
}

func (s *Scanner) calculateWorkerStagger() time.Duration {
	s.staggerMu.RLock()
	minD, maxD := s.minStagger, s.maxStagger
	s.staggerMu.RUnlock()

	if maxD <= 0 {
		return 0
	}
	if maxD <= minD {
		return minD
	}
	diff := maxD - minD
	// ponytail: uniform random jitter; upgrade to configurable distribution if required.
	return minD + time.Duration(rand.Int64N(int64(diff)+1))
}

func (s *Scanner) calculateScanInterval(base time.Duration) time.Duration {
	return calculateJitter(base, 0.15)
}

func prepareCourseQueue(courses []Course, activeNomor int) []Course {
	queue := make([]Course, len(courses))
	copy(queue, courses)
	if len(queue) <= 1 {
		return queue
	}

	startIdx := 0
	if activeNomor > 0 {
		for i, c := range queue {
			if c.Nomor == activeNomor {
				if i > 0 {
					activeCourse := queue[i]
					copy(queue[1:i+1], queue[0:i])
					queue[0] = activeCourse
				}
				startIdx = 1
				break
			}
		}
	}

	// ponytail: uniform shuffle for remaining courses; preserves active course priority at index 0.
	toShuffle := queue[startIdx:]
	rand.Shuffle(len(toShuffle), func(i, j int) {
		toShuffle[i], toShuffle[j] = toShuffle[j], toShuffle[i]
	})

	return queue
}

func (s *Scanner) ScanOnce(ctx context.Context) (int, error) {
	return s.ScanCourses(ctx, nil)
}

func (s *Scanner) TryScanOnce(ctx context.Context) (int, bool, error) {
	return s.TryScanCourses(ctx, nil)
}

func (s *Scanner) TryScanCourses(ctx context.Context, targetCourses []Course) (int, bool, error) {
	if s.presence == nil {
		return 0, false, errors.New("auto-presence is disabled")
	}

	if !s.scanMu.TryLock() {
		return 0, true, nil
	}
	defer s.scanMu.Unlock()

	scanStart := time.Now()
	attended, err := s.scanCoursesInternal(ctx, targetCourses)
	scanDur := time.Since(scanStart)

	s.statusMu.Lock()
	s.lastScanTime = scanStart
	s.lastScanDur = scanDur
	s.lastScanErr = err
	s.lastAttended = attended
	s.statusMu.Unlock()

	return attended, false, err
}

func (s *Scanner) ScanCourses(ctx context.Context, targetCourses []Course) (int, error) {
	if s.presence == nil {
		return 0, errors.New("auto-presence is disabled")
	}

	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	scanStart := time.Now()
	attended, err := s.scanCoursesInternal(ctx, targetCourses)
	scanDur := time.Since(scanStart)

	s.statusMu.Lock()
	s.lastScanTime = scanStart
	s.lastScanDur = scanDur
	s.lastScanErr = err
	s.lastAttended = attended
	s.statusMu.Unlock()

	return attended, err
}

func (s *Scanner) scanCoursesInternal(ctx context.Context, targetCourses []Course) (int, error) {
	if err := s.auth.EnsureSession(ctx); err != nil {
		return 0, fmt.Errorf("ensure session: %w", err)
	}

	var courses []Course
	if len(targetCourses) > 0 {
		courses = targetCourses
	} else {
		var err error
		courses, err = s.courses.GetCourses(ctx)
		if err != nil {
			return 0, fmt.Errorf("get courses: %w", err)
		}
	}
	if len(courses) == 0 {
		slog.Debug("No courses to scan")
		return 0, nil
	}

	activeNomor := 0
	if s.academic != nil {
		tahun, semester, _ := s.courses.ActivePeriod(ctx)
		if active := s.academic.GetActiveCourse(ctx, NowWIB(), tahun, semester, courses); active != nil {
			activeNomor = active.Nomor
		}
	}
	courseQueue := prepareCourseQueue(courses, activeNomor)

	type checkResult struct {
		course Course
		key    string
		open   bool
		err    error
	}

	jobs := make(chan Course, len(courseQueue))
	results := make(chan checkResult, len(courseQueue))

	var wg sync.WaitGroup
	workers := s.concurr
	if workers > len(courseQueue) {
		workers = len(courseQueue)
	}
	devLog("Dispatching scan workers", "courses", len(courseQueue), "workers", workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				if stagger := s.calculateWorkerStagger(); stagger > 0 {
					timer := time.NewTimer(stagger)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
				key, open, err := s.presence.CheckCourse(ctx, c)
				if errors.Is(err, ErrUnauthorized) {
					// Session expired mid-check: re-login and retry once
					if reErr := s.auth.EnsureSession(ctx); reErr == nil {
						key, open, err = s.presence.CheckCourse(ctx, c)
					}
				}
				results <- checkResult{course: c, key: key, open: open, err: err}
			}
		}()
	}

	for _, c := range courseQueue {
		jobs <- c
	}
	close(jobs)

	wg.Wait()
	close(results)

	if err := ctx.Err(); err != nil {
		return 0, err
	}

	attendedCount := 0
	todayStr := TodayDate(NowWIB())

	for res := range results {
		if res.err != nil {
			slog.Warn("Failed checking course presence", "course", res.course.CourseName(), "error", res.err)
			continue
		}
		if !res.open || res.key == "" {
			continue
		}

		todayKey := todayStr + "_" + res.key
		if s.state.Has(res.key) || s.state.Has(todayKey) {
			slog.Debug("Active presence already recorded", "course", res.course.CourseName(), "key", res.key)
			continue
		}

		slog.Info("Open presence discovered!", "course", res.course.CourseName(), "key", res.key)

		if delay := s.calculateDelay(); delay > 0 {
			slog.Info("Delaying presence submission...", "course", res.course.CourseName(), "delay", delay)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return attendedCount, ctx.Err()
			case <-timer.C:
			}
		}

		user := s.auth.User()
		studentID := 0
		if user != nil {
			studentID = user.Nomor
		}

		msg, isSuccess, submitErr := s.presence.Submit(ctx, res.course, res.key, studentID)
		if errors.Is(submitErr, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, isSuccess, submitErr = s.presence.Submit(ctx, res.course, res.key, studentID)
			}
		}

		if submitErr != nil {
			slog.Error("Failed to submit presence", "course", res.course.CourseName(), "error", submitErr)
			continue
		}

		if isSuccess {
			slog.Info("Presence recorded successfully", "course", res.course.CourseName(), "msg", msg)
			timeStr := NowWIB().Format("15:04")
			rec := PresenceRecord{
				Key:        todayKey,
				CourseName: res.course.CourseName(),
				Dosen:      res.course.Dosen,
				Time:       timeStr,
			}
			rawRec := PresenceRecord{
				Key:        res.key,
				CourseName: res.course.CourseName(),
				Dosen:      res.course.Dosen,
				Time:       timeStr,
			}
			if err := s.state.AddRecord(rawRec, rec); err != nil {
				slog.Error("Failed to persist presence state", "course", res.course.CourseName(), "key", res.key, "error", err)
			}
			if s.academic != nil {
				s.academic.InvalidateAttendanceCache()
			}
			respText := msg
			if s.academic != nil {
				if attendees, total, rErr := s.academic.GetAttendanceRoster(ctx, res.course, res.key); rErr == nil && len(attendees) > 0 {
					if total > 0 {
						respText = fmt.Sprintf("%s (%d/%d hadir)", msg, len(attendees), total)
					} else {
						respText = fmt.Sprintf("%s (%d hadir)", msg, len(attendees))
					}
				}
			}
			_ = s.notifier.NotifyPresenceSuccess(ctx, res.course.CourseName(), res.course.Dosen, res.key, respText)
			attendedCount++
		} else {
			slog.Warn("Presence response status not recognized as success", "course", res.course.CourseName(), "msg", msg)
		}
	}

	if s.academic != nil {
		s.academic.SweepExpired()
	}

	s.cmdState.mu.Lock()
	if !s.cmdState.lastRosterTime.IsZero() && time.Since(s.cmdState.lastRosterTime) >= 20*time.Second {
		s.cmdState.lastRosterMsg = ""
	}
	s.cmdState.mu.Unlock()

	return attendedCount, nil
}

func (s *Scanner) computeScanPlan(ctx context.Context, now time.Time) ScanPlan {
	if s.academic == nil || s.courses == nil {
		return NextScanPlan(now, nil)
	}

	tahun, semester, err := s.courses.ActivePeriod(ctx)
	if errors.Is(err, ErrUnauthorized) && s.auth != nil {
		if reErr := s.auth.EnsureSession(ctx); reErr == nil {
			tahun, semester, err = s.courses.ActivePeriod(ctx)
		}
	}
	if err != nil {
		slog.Warn("Failed to get active period for scan plan", "error", err)
		return NextScanPlan(now, nil)
	}

	items, err := s.academic.GetSchedule(ctx, tahun, semester)
	if errors.Is(err, ErrUnauthorized) && s.auth != nil {
		if reErr := s.auth.EnsureSession(ctx); reErr == nil {
			items, err = s.academic.GetSchedule(ctx, tahun, semester)
		}
	}
	if err != nil {
		slog.Warn("Failed to get schedule for scan plan, falling back to active interval", "error", err)
		return ScanPlan{
			Interval: ActiveInterval,
			Courses:  nil,
			InWindow: false,
		}
	}

	courses, err := s.courses.GetCourses(ctx)
	if errors.Is(err, ErrUnauthorized) && s.auth != nil {
		if reErr := s.auth.EnsureSession(ctx); reErr == nil {
			courses, err = s.courses.GetCourses(ctx)
		}
	}
	if err != nil {
		slog.Warn("Failed to get courses for scan plan", "error", err)
		return NextScanPlan(now, nil)
	}

	windows := ComputeScanWindows(items, courses, now)
	return NextScanPlan(now, windows)
}

func (s *Scanner) Run(ctx context.Context) error {
	if s.presence == nil {
		slog.Info("Auto-presence daemon loop disabled")
		<-ctx.Done()
		return nil
	}

	slog.Info("Starting auto-presence daemon loop")

	for {
		select {
		case <-ctx.Done():
			slog.Info("Auto-presence daemon loop stopped")
			return nil
		default:
		}

		now := NowWIB()
		plan := s.computeScanPlan(ctx, now)

		s.statusMu.Lock()
		s.currentPlan = plan
		s.statusMu.Unlock()

		modeName := "BACKGROUND"
		if plan.InWindow {
			modeName = "AKTIF"
		}
		if modeName != s.lastMode {
			slog.Info("Operational mode changed", "mode", modeName, "interval", plan.Interval, "courses", len(plan.Courses))
			s.lastMode = modeName
		}
		devLog("Scan plan evaluated", "in_window", plan.InWindow, "interval", plan.Interval, "courses", len(plan.Courses))

		if s.paused.Load() {
			slog.Debug("Auto-presence scanner is paused, skipping cycle")
		} else {
			scanStart := time.Now()
			attended, err := s.ScanCourses(ctx, plan.Courses)
			if err != nil {
				slog.Error("Scan cycle error", "error", err)
				s.handleScanError(ctx, err)
			} else {
				s.handleScanSuccess(ctx)
				if attended > 0 {
					slog.Info("Scan cycle completed", "attended", attended, "duration", time.Since(scanStart))
				}
			}
		}

		waitInterval := plan.Interval
		if plan.InWindow || plan.Interval >= BackgroundInterval {
			waitInterval = s.calculateScanInterval(plan.Interval)
		}

		timer := time.NewTimer(waitInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (s *Scanner) handleScanError(ctx context.Context, err error) {
	if s.notifier == nil || err == nil {
		return
	}
	if isAuthFailure(err) {
		if !s.authFailed.Swap(true) {
			_ = s.notifier.NotifyAuthFailure(ctx, err)
		}
		return
	}
	if !s.serverDown.Swap(true) {
		_ = s.notifier.NotifyServerError(ctx, err)
	}
}

func (s *Scanner) handleScanSuccess(ctx context.Context) {
	s.authFailed.Store(false)
	if s.serverDown.Swap(false) && s.notifier != nil {
		_ = s.notifier.NotifyServerRecovery(ctx)
	}
}

func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnauthorized) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "kredensial") ||
		strings.Contains(errStr, "token validation failed")
}
