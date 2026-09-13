package ethol

import (
	"context"
	"errors"
	"fmt"
	"html"
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
	cmdRateMu      sync.Mutex
	lastRelogin    time.Time
	lastRosterTime time.Time
	lastRosterMsg  string
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
		minDelay:   10 * time.Second,
		maxDelay:   30 * time.Second,
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



func (s *Scanner) HandleTelegramCommand(ctx context.Context, cmd string) string {
	switch cmd {
	case "/ping":
		return "🏓 Pong!"

	case "/help", "/start":
		if s.presence == nil {
			return "🤖 <b>ETHOL Bot (Info Akademik)</b>\n\n" +
				"Perintah yang tersedia:\n" +
				"• /status - Status daemon dan sistem\n" +
				"• /debug - Diagnostik sistem dan informasi debug\n" +
				"• /courses - Daftar mata kuliah yang terdaftar\n" +
				"• /jadwal - Jadwal perkuliahan hari ini & minggu ini\n" +
				"• /tugas - Daftar tugas perkuliahan aktif\n" +
				"• /materi - Materi & dokumen perkuliahan\n" +
				"• /presensi_kelas - Daftar kehadiran sesi presensi aktif\n" +
				"• /rekap - Rekap kehadiran semester aktif\n" +
				"• /whoami - Informasi akun ETHOL yang terhubung\n" +
				"• /relogin - Perbarui sesi login CAS\n" +
				"• /ping - Cek koneksi bot\n" +
				"• /help - Tampilkan pesan bantuan"
		}
		return "🤖 <b>Ethold Bot</b>\n\n" +
			"Perintah yang tersedia:\n" +
			"• /status - Status daemon dan scanner\n" +
			"• /debug - Diagnostik sistem dan informasi debug\n" +
			"• /check - Jalankan scanning presensi sekarang\n" +
			"• /courses - Daftar mata kuliah yang terdaftar\n" +
			"• /jadwal - Jadwal perkuliahan hari ini & minggu ini\n" +
			"• /tugas - Daftar tugas perkuliahan aktif\n" +
			"• /materi - Materi & dokumen perkuliahan\n" +
			"• /presensi_kelas - Daftar kehadiran sesi presensi aktif\n" +
			"• /rekap - Rekap kehadiran semester aktif\n" +
			"• /whoami - Informasi akun ETHOL yang terhubung\n" +
			"• /today - Presensi yang tercatat hari ini\n" +
			"• /relogin - Perbarui sesi login CAS\n" +
			"• /pause - Pause scanning otomatis\n" +
			"• /resume - Lanjutkan scanning otomatis\n" +
			"• /ping - Cek koneksi bot\n" +
			"• /help - Tampilkan pesan bantuan"

	case "/status":
		status := s.Status()

		stateStr := "Aktif"
		if s.presence == nil {
			stateStr = "Tidak Aktif"
		} else if status.Paused {
			stateStr = "Dijeda ⏸️"
		}

		userStr := "Belum login"
		if status.User != nil {
			userStr = fmt.Sprintf("%s (%s)", html.EscapeString(status.User.Nama), html.EscapeString(status.User.NipNrp))
		}

		uptime := time.Since(status.StartTime).Truncate(time.Second)

		lastScanStr := "Belum ada"
		resultStr := "-"
		if s.presence == nil {
			lastScanStr = "Tidak aktif"
			resultStr = "Tidak aktif"
		} else if !status.LastScanTime.IsZero() {
			durStr := status.LastScanDuration.Round(time.Millisecond).String()
			lastScanStr = fmt.Sprintf("%s (%s)", status.LastScanTime.In(WIBLocation).Format("15:04:05 WIB"), durStr)
			if status.LastScanErr != nil {
				resultStr = fmt.Sprintf("❌ Gagal (%s)", html.EscapeString(status.LastScanErr.Error()))
			} else {
				resultStr = fmt.Sprintf("✅ Berhasil (%d sesi baru)", status.LastAttended)
			}
		}

		activeCourseStr := "Tidak ada"
		if status.ActiveCourse != "" {
			activeCourseStr = html.EscapeString(status.ActiveCourse)
		}

		modeStr := "Background Sweep"
		if status.ScanPlan.InWindow {
			modeStr = "Active Session"
		}

		return fmt.Sprintf(
			"📊 <b>Status Sistem</b>\n\n"+
				"👤 <b>Pengguna:</b> %s\n"+
				"⚙️ <b>Status:</b> %s\n"+
				"⏱️ <b>Uptime:</b> %s\n"+
				"⚡ <b>Mode:</b> %s (interval %v)\n\n"+
				"🔍 <b>Aktivitas Scan:</b>\n"+
				"• <b>Terakhir:</b> %s\n"+
				"• <b>Hasil:</b> %s\n"+
				"• <b>Hari Ini:</b> %d entri\n"+
				"• <b>Total Tersimpan:</b> %d entri\n\n"+
				"📅 <b>Jadwal & Data:</b>\n"+
				"• <b>Active Session:</b> %s\n"+
				"• <b>Total Terdaftar:</b> %d item",
			userStr,
			stateStr,
			uptime,
			modeStr,
			status.ScanPlan.Interval,
			lastScanStr,
			resultStr,
			status.TodayAttended,
			status.TotalAttended,
			activeCourseStr,
			status.CourseCount,
		)

	case "/debug":
		return s.handleDebug(ctx)

	case "/pause":
		if s.presence == nil {
			return "⚠️ <b>Auto-presensi tidak aktif.</b>"
		}
		s.paused.Store(true)
		return "⏸️ <b>Scanning Otomatis Dijeda</b>\nBot tidak akan melakukan scanning secara otomatis. Ketik /resume untuk mengaktifkan kembali."

	case "/resume":
		if s.presence == nil {
			return "⚠️ <b>Auto-presensi tidak aktif.</b>"
		}
		s.paused.Store(false)
		return "▶️ <b>Scanning Otomatis Dilanjutkan</b>\nBot akan kembali melakukan scanning sesuai jadwal."

	case "/whoami":
		user := s.auth.User()
		if user == nil {
			if err := s.auth.EnsureSession(ctx); err != nil {
				return fmt.Sprintf("❌ <b>Gagal Mendapatkan Profil:</b> %s", html.EscapeString(err.Error()))
			}
			user = s.auth.User()
		}
		if user == nil {
			return "❌ Data pengguna tidak tersedia."
		}
		return fmt.Sprintf(
			"👤 <b>Profil Akun ETHOL</b>\n\n"+
				"• <b>Nama:</b> %s\n"+
				"• <b>NRP/NIP:</b> %s\n"+
				"• <b>ID Mahasiswa:</b> %d",
			html.EscapeString(user.Nama),
			html.EscapeString(user.NipNrp),
			user.Nomor,
		)

	case "/courses":
		loadCourses := func() ([]Course, error) {
			return s.courses.GetCourses(ctx)
		}
		courses, err := loadCourses()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				courses, err = loadCourses()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Mata Kuliah:</b> %s", html.EscapeString(err.Error()))
		}
		if len(courses) == 0 {
			return "ℹ️ Tidak ada mata kuliah terdaftar."
		}
		var b strings.Builder
		b.Grow(len(courses) * 64)
		fmt.Fprintf(&b, "📚 <b>Daftar Mata Kuliah (%d):</b>\n\n", len(courses))
		for i, c := range courses {
			fmt.Fprintf(&b, "%d. <b>%s</b>\n   👨‍🏫 %s\n", i+1, html.EscapeString(c.CourseName()), html.EscapeString(c.Dosen))
		}
		return strings.TrimRight(b.String(), "\n")

	case "/today":
		if s.presence == nil || s.state == nil {
			return "⚠️ <b>Auto-presensi tidak aktif.</b>"
		}
		todayStr := TodayDate(NowWIB())
		todayRecs := s.state.RecordsWithPrefix(todayStr + "_")
		if len(todayRecs) == 0 {
			return fmt.Sprintf("📅 <b>Presensi Hari Ini (%s):</b>\nBelum ada presensi yang tercatat hari ini.", todayStr)
		}
		var b strings.Builder
		b.Grow(len(todayRecs) * 64)
		fmt.Fprintf(&b, "📅 <b>Presensi Hari Ini (%s) - Total %d:</b>\n\n", todayStr, len(todayRecs))
		for i, rec := range todayRecs {
			cleanKey := strings.TrimPrefix(rec.Key, todayStr+"_")
			if rec.CourseName != "" {
				line := fmt.Sprintf("%d. <b>%s</b>", i+1, html.EscapeString(rec.CourseName))
				if rec.Dosen != "" {
					line += fmt.Sprintf(" - %s", html.EscapeString(rec.Dosen))
				}
				line += fmt.Sprintf(" (<code>%s</code>)", html.EscapeString(cleanKey))
				if rec.Time != "" {
					line += fmt.Sprintf(" • %s", html.EscapeString(rec.Time))
				}
				fmt.Fprintln(&b, line)
			} else {
				fmt.Fprintf(&b, "%d. <code>%s</code>\n", i+1, html.EscapeString(cleanKey))
			}
		}
		return strings.TrimRight(b.String(), "\n")

	case "/check":
		if s.presence == nil {
			return "⚠️ <b>Auto-presensi tidak aktif.</b>"
		}
		s.statusMu.RLock()
		lastTime := s.lastScanTime
		s.statusMu.RUnlock()
		if !lastTime.IsZero() && time.Since(lastTime) < 15*time.Second {
			return fmt.Sprintf("ℹ️ <b>Pemindaian Baru Saja Selesai</b>\nPemindaian baru saja dijalankan %v yang lalu. Gunakan /status untuk melihat hasil.", time.Since(lastTime).Round(time.Second))
		}
		attended, inProgress, err := s.TryScanOnce(ctx)
		if inProgress {
			return "⏳ <b>Pemindaian Sedang Berlangsung</b>\nDaemon sedang menjalankan pemindaian presensi. Harap tunggu hingga selesai."
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Pemindaian Gagal:</b> %s", html.EscapeString(err.Error()))
		}
		if attended > 0 {
			return fmt.Sprintf("🎉 <b>Pemindaian Selesai!</b>\nBerhasil mencatat %d presensi baru.", attended)
		}
		return "✅ <b>Pemindaian Selesai!</b>\nTidak ada presensi aktif baru yang ditemukan."

	case "/jadwal":
		if s.academic == nil {
			return "❌ Fitur jadwal akademik tidak tersedia."
		}
		loadJadwal := func() (string, error) {
			tahun, semester, err := s.courses.ActivePeriod(ctx)
			if err != nil {
				return "", err
			}
			return s.academic.FormatScheduleText(ctx, NowWIB(), tahun, semester)
		}
		msg, err := loadJadwal()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadJadwal()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Jadwal:</b> %s", html.EscapeString(err.Error()))
		}
		return msg

	case "/tugas":
		if s.academic == nil {
			return "❌ Fitur tugas akademik tidak tersedia."
		}
		loadTugas := func() (string, error) {
			courses, err := s.courses.GetCourses(ctx)
			if err != nil {
				return "", err
			}
			return s.academic.FormatTasksText(ctx, courses)
		}
		msg, err := loadTugas()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadTugas()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Tugas:</b> %s", html.EscapeString(err.Error()))
		}
		return msg

	case "/materi":
		if s.academic == nil {
			return "❌ Fitur materi kuliah tidak tersedia."
		}
		loadMateri := func() (string, error) {
			courses, err := s.courses.GetCourses(ctx)
			if err != nil {
				return "", err
			}
			return s.academic.FormatMaterialsText(ctx, courses)
		}
		msg, err := loadMateri()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadMateri()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Materi:</b> %s", html.EscapeString(err.Error()))
		}
		return msg

	case "/presensi_kelas":
		if s.academic == nil || s.presence == nil {
			return "❌ Fitur presensi kelas tidak tersedia."
		}
		s.cmdRateMu.Lock()
		if !s.lastRosterTime.IsZero() && time.Since(s.lastRosterTime) < 20*time.Second && s.lastRosterMsg != "" {
			cachedMsg := s.lastRosterMsg
			s.cmdRateMu.Unlock()
			return cachedMsg
		}
		s.cmdRateMu.Unlock()

		loadRoster := func() (string, error) {
			courses, err := s.courses.GetCourses(ctx)
			if err != nil {
				return "", err
			}
			var activeReports []string
			for _, c := range courses {
				key, open, checkErr := s.presence.CheckCourse(ctx, c)
				if checkErr == nil && open && key != "" {
					attendees, total, rErr := s.academic.GetAttendanceRoster(ctx, c, key)
					if rErr == nil {
						activeReports = append(activeReports, FormatRosterText(c, key, attendees, total))
					}
				}
			}
			if len(activeReports) == 0 {
				return "ℹ️ <b>Presensi Kelas:</b>\nTidak ada sesi presensi yang sedang aktif saat ini.", nil
			}
			return strings.Join(activeReports, "\n\n───────────────\n\n"), nil
		}
		msg, err := loadRoster()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadRoster()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Presensi Kelas:</b> %s", html.EscapeString(err.Error()))
		}
		s.cmdRateMu.Lock()
		s.lastRosterTime = time.Now()
		s.lastRosterMsg = msg
		s.cmdRateMu.Unlock()
		return msg

	case "/rekap":
		if s.academic == nil {
			return "❌ Fitur rekap presensi tidak tersedia."
		}
		loadRekap := func() (string, error) {
			user := s.auth.User()
			if user == nil {
				if err := s.auth.EnsureSession(ctx); err != nil {
					return "", err
				}
				user = s.auth.User()
			}
			studentID := 0
			if user != nil {
				studentID = user.Nomor
			}
			courses, err := s.courses.GetCourses(ctx)
			if err != nil {
				return "", err
			}
			tahun, semester, err := s.courses.ActivePeriod(ctx)
			if err != nil {
				return "", err
			}
			return s.academic.FormatAttendanceStatsText(ctx, NowWIB(), tahun, semester, studentID, courses)
		}
		msg, err := loadRekap()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadRekap()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Rekap:</b> %s", html.EscapeString(err.Error()))
		}
		return msg

	case "/relogin":
		s.cmdRateMu.Lock()
		if !s.lastRelogin.IsZero() && time.Since(s.lastRelogin) < 30*time.Second {
			remaining := (30*time.Second - time.Since(s.lastRelogin)).Round(time.Second)
			s.cmdRateMu.Unlock()
			return fmt.Sprintf("⏳ <b>Relogin Dibatasi</b>\nSesi CAS baru saja diperbarui. Harap tunggu %v sebelum mencoba relogin lagi.", remaining)
		}
		s.cmdRateMu.Unlock()

		user, err := s.auth.Relogin(ctx)
		if err != nil {
			return fmt.Sprintf("❌ <b>Relogin Gagal:</b> %s", html.EscapeString(err.Error()))
		}
		s.cmdRateMu.Lock()
		s.lastRelogin = time.Now()
		s.cmdRateMu.Unlock()

		nama := ""
		if user != nil {
			nama = user.Nama
		}
		return fmt.Sprintf("🔄 <b>Relogin Berhasil!</b>\nSesi CAS SSO diperbarui untuk <b>%s</b>.", html.EscapeString(nama))

	default:
		return "❓ Perintah tidak dikenal. Ketik /help untuk daftar perintah."
	}
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

		if s.paused.Load() {
			slog.Debug("Auto-presence scanner is paused, skipping cycle")
		} else {
			scanStart := time.Now()
			attended, err := s.ScanCourses(ctx, plan.Courses)
			if err != nil {
				slog.Error("Scan cycle error", "error", err)
			} else if attended > 0 {
				slog.Info("Scan cycle completed", "attended", attended, "duration", time.Since(scanStart))
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
