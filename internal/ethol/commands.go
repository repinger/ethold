package ethol

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
)

func (s *Scanner) HandleTelegramCommand(ctx context.Context, cmd string) string {
	devLog("Handling Telegram command", "cmd", cmd)
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
				"• /ujian - Jadwal ujian online (UTS & UAS)\n" +
				"• /tugas - Daftar tugas perkuliahan aktif\n" +
				"• /materi - Materi & dokumen perkuliahan\n" +
				"• /pengumuman - Pengumuman resmi kampus\n" +
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
			"• /ujian - Jadwal ujian online (UTS & UAS)\n" +
			"• /tugas - Daftar tugas perkuliahan aktif\n" +
			"• /materi - Materi & dokumen perkuliahan\n" +
			"• /pengumuman - Pengumuman resmi kampus\n" +
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

	case "/ujian":
		if s.academic == nil {
			return "❌ Fitur jadwal ujian tidak tersedia."
		}
		loadUjian := func() (string, error) {
			tahun, semester, err := s.courses.ActivePeriod(ctx)
			if err != nil {
				return "", err
			}
			return s.academic.FormatExamsText(ctx, tahun, semester)
		}
		msg, err := loadUjian()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadUjian()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Jadwal Ujian:</b> %s", html.EscapeString(err.Error()))
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

	case "/pengumuman":
		if s.academic == nil {
			return "❌ Fitur pengumuman kampus tidak tersedia."
		}
		loadPengumuman := func() (string, error) {
			return s.academic.FormatAnnouncementsText(ctx)
		}
		msg, err := loadPengumuman()
		if errors.Is(err, ErrUnauthorized) {
			if reErr := s.auth.EnsureSession(ctx); reErr == nil {
				msg, err = loadPengumuman()
			}
		}
		if err != nil {
			return fmt.Sprintf("❌ <b>Gagal Mengambil Pengumuman:</b> %s", html.EscapeString(err.Error()))
		}
		return msg

	case "/presensi_kelas":
		if s.academic == nil || s.presence == nil {
			return "❌ Fitur presensi kelas tidak tersedia."
		}
		s.cmdState.mu.Lock()
		if !s.cmdState.lastRosterTime.IsZero() && time.Since(s.cmdState.lastRosterTime) < 20*time.Second && s.cmdState.lastRosterMsg != "" {
			cachedMsg := s.cmdState.lastRosterMsg
			s.cmdState.mu.Unlock()
			return cachedMsg
		}
		s.cmdState.mu.Unlock()

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
		s.cmdState.mu.Lock()
		s.cmdState.lastRosterTime = time.Now()
		s.cmdState.lastRosterMsg = msg
		s.cmdState.mu.Unlock()
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
		s.cmdState.mu.Lock()
		if !s.cmdState.lastRelogin.IsZero() && time.Since(s.cmdState.lastRelogin) < 30*time.Second {
			remaining := (30*time.Second - time.Since(s.cmdState.lastRelogin)).Round(time.Second)
			s.cmdState.mu.Unlock()
			return fmt.Sprintf("⏳ <b>Relogin Dibatasi</b>\nSesi CAS baru saja diperbarui. Harap tunggu %v sebelum mencoba relogin lagi.", remaining)
		}
		s.cmdState.mu.Unlock()

		user, err := s.auth.Relogin(ctx)
		if err != nil {
			return fmt.Sprintf("❌ <b>Relogin Gagal:</b> %s", html.EscapeString(err.Error()))
		}
		s.cmdState.mu.Lock()
		s.cmdState.lastRelogin = time.Now()
		s.cmdState.mu.Unlock()

		nama := ""
		if user != nil {
			nama = user.Nama
		}
		return fmt.Sprintf("🔄 <b>Relogin Berhasil!</b>\nSesi CAS SSO diperbarui untuk <b>%s</b>.", html.EscapeString(nama))

	default:
		return "❓ Perintah tidak dikenal. Ketik /help untuk daftar perintah."
	}
}
