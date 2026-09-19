package ethol

import (
	"context"
	"fmt"
	"html"
	"os"
	"runtime"
	"time"
)

func (s *Scanner) handleDebug(ctx context.Context) string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	allocMB := float64(m.Alloc) / (1024 * 1024)
	totalAllocMB := float64(m.TotalAlloc) / (1024 * 1024)
	sysMB := float64(m.Sys) / (1024 * 1024)
	heapInuseMB := float64(m.HeapInuse) / (1024 * 1024)

	status := s.Status()
	stateStr := "Aktif"
	if s.presence == nil {
		stateStr = "Tidak Aktif"
	} else if status.Paused {
		stateStr = "Dijeda ⏸️"
	}
	uptime := time.Since(status.StartTime).Truncate(time.Second)

	modeStr := "Background Sweep"
	if s.presence == nil {
		modeStr = "Disabled"
	} else if status.ScanPlan.InWindow {
		modeStr = "Active Session"
	}

	lastScanStr := "Belum pernah"
	if !status.LastScanTime.IsZero() {
		durStr := status.LastScanDuration.Round(time.Millisecond).String()
		resStr := fmt.Sprintf("✅ %d sesi baru", status.LastAttended)
		if status.LastScanErr != nil {
			resStr = fmt.Sprintf("❌ %s", html.EscapeString(status.LastScanErr.Error()))
		}
		lastScanStr = fmt.Sprintf("%s (%s) - %s", status.LastScanTime.In(WIBLocation).Format("15:04:05 WIB"), durStr, resStr)
	}

	statePath := "Tidak tersedia"
	if s.state != nil {
		statePath = s.state.Path()
	}

	coursesCacheStr := "Tidak tersedia"
	if s.courses != nil {
		ageStr := "belum ada"
		if age, ok := s.courses.CacheAge(); ok {
			ageStr = age.Round(time.Second).String()
		}
		periodStr := "-"
		if y, sem, ok := s.courses.CachedActivePeriod(); ok {
			periodStr = fmt.Sprintf("%d/%d", y, sem)
		}
		coursesCacheStr = fmt.Sprintf("%d item (usia: %s, periode: %s)", s.courses.CachedCount(), ageStr, periodStr)
	}

	acadCacheStr := "Tidak tersedia"
	if s.academic != nil {
		cStats := s.academic.CacheStats()
		acadCacheStr = fmt.Sprintf("Jadwal: %d, Ujian: %d, Tugas: %d, Materi: %d, Video: %d, Presensi: %d, Notif: %d",
			cStats.SchedulesCount, cStats.ExamsCount, cStats.TasksCount, cStats.MaterialsCount, cStats.VideosCount, cStats.AttendanceCount, cStats.ProcessedNotifs)
	}

	userStr := "Tidak tersedia"
	if s.auth != nil {
		user := s.auth.User()
		if user == nil {
			_ = s.auth.EnsureSession(ctx)
			user = s.auth.User()
		}
		if user != nil {
			userStr = fmt.Sprintf("%s (%s) [ID: %d]", html.EscapeString(user.Nama), html.EscapeString(user.NipNrp), user.Nomor)
		}
	}

	return fmt.Sprintf(
		"🛠️ <b>Diagnostik Sistem & Debug</b>\n\n"+
			"🖥️ <b>Runtime Environment:</b>\n"+
			"• <b>Go Version:</b> %s\n"+
			"• <b>OS / Arch:</b> %s / %s\n"+
			"• <b>PID:</b> <code>%d</code>\n"+
			"• <b>CPUs:</b> %d\n"+
			"• <b>Goroutines:</b> %d\n"+
			"• <b>Memory (Alloc):</b> %.2f MB (Total: %.2f MB, Sys: %.2f MB)\n"+
			"• <b>Heap:</b> %.2f MB in-use, %d objects\n"+
			"• <b>GC:</b> %d cycles\n\n"+
			"⚙️ <b>Daemon & Scanner:</b>\n"+
			"• <b>Status:</b> %s\n"+
			"• <b>Uptime:</b> %s\n"+
			"• <b>Workers:</b> %d\n"+
			"• <b>Delay:</b> %v - %v\n"+
			"• <b>Stagger:</b> %v - %v\n"+
			"• <b>Mode:</b> %s (interval: %v)\n"+
			"• <b>Last Scan:</b> %s\n\n"+
			"💾 <b>Storage & Cache:</b>\n"+
			"• <b>State File:</b> <code>%s</code>\n"+
			"• <b>Recorded Keys:</b> Total: %d, Hari ini: %d\n"+
			"• <b>Courses Cache:</b> %s\n"+
			"• <b>Academic Cache:</b> %s\n\n"+
			"👤 <b>CAS Session:</b>\n"+
			"• <b>Akun:</b> %s",
		runtime.Version(),
		runtime.GOOS, runtime.GOARCH,
		os.Getpid(),
		runtime.NumCPU(),
		runtime.NumGoroutine(),
		allocMB, totalAllocMB, sysMB,
		heapInuseMB, m.HeapObjects,
		m.NumGC,
		stateStr,
		uptime,
		status.WorkerCount,
		status.MinDelay, status.MaxDelay,
		status.MinStagger, status.MaxStagger,
		modeStr, status.ScanPlan.Interval,
		lastScanStr,
		html.EscapeString(statePath),
		status.TotalAttended, status.TodayAttended,
		coursesCacheStr,
		acadCacheStr,
		userStr,
	)
}
