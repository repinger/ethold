package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type RosterItem struct {
	NRP  string `json:"nrp"`
	Nama string `json:"nama"`
}

type AttendanceStats struct {
	Percentage         float64
	TotalDosenSemester int
	TotalMhsSemester   int
	TotalDosenToday    int
	TotalMhsToday      int
	Breakdown          []CourseAttendance
}

type CourseAttendance struct {
	Nama   string
	Hadir  int
	Total  int
	DToday int
	MToday int
}

type studentHistoryItem struct {
	Tanggal string `json:"tanggal"`
	Waktu   string `json:"waktu"`
}

type lecturerHistoryItem struct {
	WaktuIndonesia string `json:"waktu_indonesia"`
	Tanggal        string `json:"tanggal"`
}

func (am *AcademicManager) GetAttendanceRoster(ctx context.Context, c Course, key string) ([]RosterItem, int, error) {
	var (
		attendees     []RosterItem
		rosterErr     error
		totalEnrolled int
		wg            sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		rosterURL := fmt.Sprintf("%s/api/presensi/daftar-mahasiswa-hadir-kuliah?key=%s", am.baseURL, url.QueryEscape(key))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rosterURL, nil)
		if err != nil {
			rosterErr = fmt.Errorf("create roster req: %w", err)
			return
		}

		resp, err := am.client.Do(req)
		if err != nil {
			rosterErr = fmt.Errorf("fetch roster: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized {
			rosterErr = ErrUnauthorized
			return
		}

		if resp.StatusCode == http.StatusOK {
			var items []RosterItem
			if err := json.NewDecoder(resp.Body).Decode(&items); err == nil {
				for _, item := range items {
					if item.NRP != "" || item.Nama != "" {
						attendees = append(attendees, item)
					}
				}
			}
		}
	}()

	go func() {
		defer wg.Done()
		countURL := fmt.Sprintf("%s/api/presensi/jumlah-mahasiswa-per-kuliah?kuliah=%d&jenis_schema=%d", am.baseURL, c.Nomor, c.JenisSchema)
		reqCount, err := http.NewRequestWithContext(ctx, http.MethodGet, countURL, nil)
		if err != nil {
			return
		}
		respCount, err := am.client.Do(reqCount)
		if err != nil {
			return
		}
		defer respCount.Body.Close()

		if respCount.StatusCode == http.StatusOK {
			var res struct {
				Jumlah any `json:"jumlah"`
				Data   struct {
					Jumlah any `json:"jumlah"`
				} `json:"data"`
			}
			if json.NewDecoder(respCount.Body).Decode(&res) == nil {
				if res.Data.Jumlah != nil {
					totalEnrolled = parseCount(res.Data.Jumlah)
				} else {
					totalEnrolled = parseCount(res.Jumlah)
				}
			}
		}
	}()

	wg.Wait()

	if rosterErr != nil {
		return nil, 0, rosterErr
	}

	return attendees, totalEnrolled, nil
}

func FormatRosterText(c Course, key string, attendees []RosterItem, totalEnrolled int) string {
	var sb strings.Builder
	sb.Grow(len(attendees)*64 + 128)
	totalStr := ""
	if totalEnrolled > 0 {
		totalStr = fmt.Sprintf(" / %d", totalEnrolled)
	}
	sb.WriteString("👥 <b>DAFTAR PRESENSI KELAS</b>\n\n")
	fmt.Fprintf(&sb, "📚 <b>Mata Kuliah:</b> %s\n", html.EscapeString(c.CourseName()))
	fmt.Fprintf(&sb, "🔑 <b>Key:</b> <code>%s</code>\n", html.EscapeString(key))
	fmt.Fprintf(&sb, "📊 <b>Kehadiran:</b> %d%s Mahasiswa Hadir\n\n", len(attendees), totalStr)

	if len(attendees) == 0 {
		sb.WriteString("Belum ada mahasiswa yang tercatat hadir.")
		return sb.String()
	}

	sb.WriteString("<b>Daftar Mahasiswa Hadir:</b>\n")
	limit := len(attendees)
	if limit > 100 {
		limit = 100
	}
	for i := 0; i < limit; i++ {
		a := attendees[i]
		nrp := html.EscapeString(a.NRP)
		nama := html.EscapeString(a.Nama)
		if nrp != "" && nama != "" {
			fmt.Fprintf(&sb, "%d. %s - <b>%s</b>\n", i+1, nrp, nama)
		} else if nrp != "" {
			fmt.Fprintf(&sb, "%d. <code>%s</code>\n", i+1, nrp)
		} else {
			fmt.Fprintf(&sb, "%d. <b>%s</b>\n", i+1, nama)
		}
	}
	if len(attendees) > 100 {
		fmt.Fprintf(&sb, "\n<i>... dan %d mahasiswa lainnya</i>", len(attendees)-100)
	}

	return strings.TrimSpace(sb.String())
}

func (am *AcademicManager) FormatAttendanceStatsText(ctx context.Context, now time.Time, tahun, semester, studentID int, courses []Course) (string, error) {
	stats, err := am.getAttendanceStatsAt(ctx, now, tahun, semester, studentID, courses)
	if err != nil {
		return "", err
	}

	if len(courses) == 0 {
		return "📊 <b>REKAP PRESENSI KULIAH</b>\n\nTidak ada mata kuliah yang terdaftar.", nil
	}

	var sb strings.Builder
	sb.WriteString("📊 <b>REKAP PRESENSI KULIAH</b>\n\n")
	sb.WriteString(fmt.Sprintf("📈 <b>Kehadiran Semester:</b> %.1f%%\n", stats.Percentage))
	sb.WriteString(fmt.Sprintf("📚 <b>Total Kehadiran:</b> %d / %d Sesi\n", stats.TotalMhsSemester, stats.TotalDosenSemester))
	sb.WriteString(fmt.Sprintf("📅 <b>Status Hari Ini:</b> %d / %d Kelas Hadir\n\n", stats.TotalMhsToday, stats.TotalDosenToday))
	sb.WriteString("<b>Rincian Mata Kuliah:</b>\n")

	for _, c := range stats.Breakdown {
		statusTag := "⚪ Belum Ada Sesi"
		if c.DToday > 0 {
			if c.MToday > 0 {
				statusTag = "🟢 Tervalidasi Hadir"
			} else {
				statusTag = "⚠️ Belum Hadir"
			}
		} else if c.MToday > 0 {
			statusTag = "🟢 Tervalidasi Hadir"
		}

		coursePct := 100.0
		if c.Total > 0 {
			coursePct = (float64(c.Hadir) / float64(c.Total)) * 100.0
		}

		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n", html.EscapeString(c.Nama)))
		sb.WriteString(fmt.Sprintf("  Kehadiran: %d/%d (%.1f%%)\n", c.Hadir, c.Total, coursePct))
		sb.WriteString(fmt.Sprintf("  Status Hari Ini: %s\n", statusTag))
	}

	return strings.TrimSpace(sb.String()), nil
}

func (am *AcademicManager) fetchStudentHistory(ctx context.Context, c Course, studentID int) ([]studentHistoryItem, error) {
	histURL := fmt.Sprintf("%s/api/presensi/riwayat?kuliah=%d&jenis_schema=%d&nomor=%d", am.baseURL, c.Nomor, c.JenisSchema, studentID)
	reqHist, err := http.NewRequestWithContext(ctx, http.MethodGet, histURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create riwayat req: %w", err)
	}
	respHist, err := am.client.Do(reqHist)
	if err != nil {
		return nil, fmt.Errorf("fetch riwayat: %w", err)
	}
	defer respHist.Body.Close()

	if respHist.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	var studentItems []studentHistoryItem
	if respHist.StatusCode == http.StatusOK {
		_ = json.NewDecoder(respHist.Body).Decode(&studentItems)
	}
	return studentItems, nil
}

func (am *AcademicManager) fetchLecturerHistory(ctx context.Context, c Course, tahun, semester int) ([]lecturerHistoryItem, error) {
	dosenURL := fmt.Sprintf("%s/api/presensi/get-tanggal-presensi-dosen-per-semester?tahun=%d&semester=%d&kuliah=%d&dosen=%s",
		am.baseURL, tahun, semester, c.Nomor, url.QueryEscape(c.Dosen))
	reqDosen, err := http.NewRequestWithContext(ctx, http.MethodGet, dosenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create presensi dosen req: %w", err)
	}
	respDosen, err := am.client.Do(reqDosen)
	if err != nil {
		return nil, fmt.Errorf("fetch presensi dosen: %w", err)
	}
	defer respDosen.Body.Close()

	if respDosen.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	var lecturerItems []lecturerHistoryItem
	if respDosen.StatusCode == http.StatusOK {
		_ = json.NewDecoder(respDosen.Body).Decode(&lecturerItems)
	}
	return lecturerItems, nil
}

func (am *AcademicManager) fetchCourseAttendance(ctx context.Context, c Course, tahun, semester, studentID int, todayStr, todayStrAlt string) (CourseAttendance, error) {
	var (
		studentItems  []studentHistoryItem
		lecturerItems []lecturerHistoryItem
		errStudent    error
		errLecturer   error
		wg            sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		studentItems, errStudent = am.fetchStudentHistory(ctx, c, studentID)
	}()
	go func() {
		defer wg.Done()
		lecturerItems, errLecturer = am.fetchLecturerHistory(ctx, c, tahun, semester)
	}()
	wg.Wait()

	if errStudent != nil {
		return CourseAttendance{}, errStudent
	}
	if errLecturer != nil {
		return CourseAttendance{}, errLecturer
	}

	hadir := len(studentItems)
	mToday := 0
	for _, item := range studentItems {
		t := item.Tanggal
		if t == "" {
			t = item.Waktu
		}
		if isDateToday(t, todayStr, todayStrAlt) {
			mToday++
		}
	}

	total := len(lecturerItems)
	dToday := 0
	for _, item := range lecturerItems {
		t := item.WaktuIndonesia
		if t == "" {
			t = item.Tanggal
		}
		if isDateToday(t, todayStr, todayStrAlt) {
			dToday++
		}
	}

	return CourseAttendance{
		Nama:   c.CourseName(),
		Hadir:  hadir,
		Total:  total,
		DToday: dToday,
		MToday: mToday,
	}, nil
}

type berandaStatsResponse struct {
	Sukses bool `json:"sukses"`
	Data   struct {
		TotalSesi any `json:"totalSesi"`
		RataHadir any `json:"rataHadir"`
	} `json:"data"`
}

func (am *AcademicManager) fetchBerandaStats(ctx context.Context, tahun, semester int) (float64, int, bool) {
	endpoint := fmt.Sprintf("%s/api/presensi/stat-beranda-mahasiswa?tahun=%d&semester=%d", am.baseURL, tahun, semester)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, false
	}
	resp, err := am.client.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, false
	}
	var res berandaStatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil || !res.Sukses {
		return 0, 0, false
	}
	rata := float64(parseCount(res.Data.RataHadir))
	total := parseCount(res.Data.TotalSesi)
	return rata, total, true
}

func (am *AcademicManager) getAttendanceStatsAt(ctx context.Context, now time.Time, tahun, semester, studentID int, courses []Course) (*AttendanceStats, error) {
	nowWIB := now.In(WIBLocation)
	todayStr := nowWIB.Format("02-01-2006")
	todayStrAlt := nowWIB.Format("2006-01-02")

	key := fmt.Sprintf("%d-%d-%d-%s", tahun, semester, studentID, todayStr)
	am.mu.RLock()
	if entry, ok := am.attendanceCache[key]; ok && time.Since(entry.timestamp) < am.ttl {
		cp := *entry.stats
		cp.Breakdown = make([]CourseAttendance, len(entry.stats.Breakdown))
		copy(cp.Breakdown, entry.stats.Breakdown)
		am.mu.RUnlock()
		return &cp, nil
	}
	am.mu.RUnlock()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stats := &AttendanceStats{
		Breakdown: make([]CourseAttendance, 0, len(courses)),
	}

	var (
		berandaRata  float64
		berandaTotal int
		hasBeranda   bool
		berandaWg    sync.WaitGroup
	)
	berandaWg.Add(1)
	go func() {
		defer berandaWg.Done()
		berandaRata, berandaTotal, hasBeranda = am.fetchBerandaStats(cancelCtx, tahun, semester)
	}()

	results := make([]CourseAttendance, len(courses))
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	sem := make(chan struct{}, maxAcademicConcurrency)
courseLoop:
	for i, c := range courses {
		select {
		case <-cancelCtx.Done():
			break courseLoop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int, crs Course) {
			defer func() {
				<-sem
				wg.Done()
			}()
			ca, err := am.fetchCourseAttendance(cancelCtx, crs, tahun, semester, studentID, todayStr, todayStrAlt)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			results[idx] = ca
		}(i, c)
	}
	wg.Wait()
	berandaWg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	for _, ca := range results {
		stats.TotalMhsSemester += ca.Hadir
		stats.TotalDosenSemester += ca.Total
		stats.TotalMhsToday += ca.MToday
		stats.TotalDosenToday += ca.DToday
		stats.Breakdown = append(stats.Breakdown, ca)
	}

	if hasBeranda {
		stats.Percentage = berandaRata
		if stats.TotalDosenSemester == 0 && berandaTotal > 0 {
			stats.TotalDosenSemester = berandaTotal
		}
	} else if stats.TotalDosenSemester > 0 {
		stats.Percentage = (float64(stats.TotalMhsSemester) / float64(stats.TotalDosenSemester)) * 100.0
		if stats.Percentage > 100.0 {
			stats.Percentage = 100.0
		}
	} else {
		stats.Percentage = 100.0
	}

	am.mu.Lock()
	am.attendanceCache[key] = attendanceCacheEntry{
		stats:     stats,
		timestamp: time.Now(),
	}
	am.mu.Unlock()

	return stats, nil
}

func isDateToday(raw, todayStr, todayStrAlt string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	return strings.Contains(raw, todayStr) || strings.Contains(raw, todayStrAlt)
}
