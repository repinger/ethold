package ethol

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ScheduleItem struct {
	Hari       string `json:"hari"`
	JamAwal    string `json:"jam_awal"`
	JamAkhir   string `json:"jam_akhir"`
	Matakuliah any    `json:"matakuliah"`
	Dosen      string `json:"dosen"`
	Ruang      string `json:"ruang"`
	Nomor      int    `json:"nomor"`
	Kuliah     int    `json:"kuliah"`
}

func (si ScheduleItem) CourseName() string {
	if si.Matakuliah == nil {
		if si.Kuliah > 0 {
			return fmt.Sprintf("Kuliah #%d", si.Kuliah)
		}
		return ""
	}
	if s, ok := si.Matakuliah.(string); ok && s != "" {
		return s
	}
	if m, ok := si.Matakuliah.(map[string]any); ok {
		if name, ok := m["nama"].(string); ok && name != "" {
			return name
		}
		if name, ok := m["matakuliah"].(string); ok && name != "" {
			return name
		}
	}
	if si.Kuliah > 0 {
		return fmt.Sprintf("Kuliah #%d", si.Kuliah)
	}
	return ""
}

func (si ScheduleItem) DayValue() int {
	h := strings.ToLower(strings.TrimSpace(si.Hari))
	switch {
	case strings.HasPrefix(h, "senin"):
		return 1
	case strings.HasPrefix(h, "selasa"):
		return 2
	case strings.HasPrefix(h, "rabu"):
		return 3
	case strings.HasPrefix(h, "kamis"):
		return 4
	case strings.HasPrefix(h, "jum"):
		return 5
	case strings.HasPrefix(h, "sabtu"):
		return 6
	case strings.HasPrefix(h, "minggu") || strings.HasPrefix(h, "ahad"):
		return 7
	default:
		return 8
	}
}

func (am *AcademicManager) GetSchedule(ctx context.Context, tahun, semester int) ([]ScheduleItem, error) {
	key := fmt.Sprintf("%d-%d", tahun, semester)

	am.mu.RLock()
	if entry, ok := am.scheduleCache[key]; ok && time.Since(entry.timestamp) < am.ttl {
		items := entry.items
		am.mu.RUnlock()
		return items, nil
	}
	am.mu.RUnlock()

	scheduleURL := fmt.Sprintf("%s/api/jadwal/jadwal-online?tahun=%d&semester=%d", am.baseURL, tahun, semester)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheduleURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create schedule req: %w", err)
	}

	resp, err := am.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch schedule: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch schedule failed: HTTP %d", resp.StatusCode)
	}

	var items []ScheduleItem
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512*1024)).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode schedule: %w", err)
	}

	slices.SortStableFunc(items, func(a, b ScheduleItem) int {
		da, db := a.DayValue(), b.DayValue()
		if da != db {
			return da - db
		}
		return strings.Compare(a.JamAwal, b.JamAwal)
	})

	am.mu.Lock()
	now := time.Now()
	am.sweepExpiredLocked(now)
	am.scheduleCache[key] = scheduleCacheEntry{
		items:     items,
		timestamp: now,
	}
	am.mu.Unlock()

	return items, nil
}

func matchCourse(item ScheduleItem, courses []Course) *Course {
	// 1. Primary: match item.Kuliah against course.Nomor or course.KuliahAsal
	if item.Kuliah > 0 {
		for i := range courses {
			if courses[i].Nomor == item.Kuliah || courses[i].KuliahAsal == item.Kuliah {
				c := courses[i]
				return &c
			}
		}
	}

	// 2. Fallback: match by normalized name
	itemName := strings.TrimSpace(item.CourseName())
	if itemName != "" {
		for i := range courses {
			if strings.EqualFold(strings.TrimSpace(courses[i].CourseName()), itemName) {
				c := courses[i]
				return &c
			}
		}
	}

	// 3. Fallback: only consider item.Nomor if item.Kuliah == 0 and name match didn't match
	if item.Kuliah == 0 && item.Nomor > 0 {
		for i := range courses {
			if courses[i].Nomor == item.Nomor || courses[i].KuliahAsal == item.Nomor {
				c := courses[i]
				return &c
			}
		}
	}

	return nil
}

func (am *AcademicManager) GetActiveCourse(ctx context.Context, now time.Time, tahun, semester int, courses []Course) *Course {
	if am == nil || len(courses) == 0 {
		return nil
	}

	items, err := am.GetSchedule(ctx, tahun, semester)
	if err != nil || len(items) == 0 {
		return nil
	}

	return findActiveCourse(items, courses, now)
}

func (am *AcademicManager) CachedActiveCourse(now time.Time, tahun, semester int, courses []Course) *Course {
	if am == nil || len(courses) == 0 {
		return nil
	}

	key := fmt.Sprintf("%d-%d", tahun, semester)
	am.mu.RLock()
	entry, ok := am.scheduleCache[key]
	if !ok || time.Since(entry.timestamp) >= am.ttl || len(entry.items) == 0 {
		am.mu.RUnlock()
		return nil
	}
	items := make([]ScheduleItem, len(entry.items))
	copy(items, entry.items)
	am.mu.RUnlock()

	return findActiveCourse(items, courses, now)
}

func findActiveCourse(items []ScheduleItem, courses []Course, now time.Time) *Course {
	nowWIB := now.In(WIBLocation)
	nowWeekday := nowWIB.Weekday()
	nowDayVal := int(nowWeekday)
	if nowDayVal == 0 {
		nowDayVal = 7
	}

	for _, item := range items {
		if item.DayValue() != nowDayVal {
			continue
		}

		start, err := parseClockToTime(item.JamAwal, nowWIB)
		if err != nil {
			continue
		}
		end, err := parseClockToTime(item.JamAkhir, nowWIB)
		if err != nil {
			continue
		}

		windowStart := start.Add(-15 * time.Minute)
		windowEnd := end.Add(20 * time.Minute)

		if (nowWIB.Equal(windowStart) || nowWIB.After(windowStart)) &&
			(nowWIB.Equal(windowEnd) || nowWIB.Before(windowEnd)) {
			if matched := matchCourse(item, courses); matched != nil {
				return matched
			}
		}
	}

	return nil
}

func (am *AcademicManager) FormatScheduleText(ctx context.Context, now time.Time, tahun, semester int) (string, error) {
	items, err := am.GetSchedule(ctx, tahun, semester)
	if err != nil {
		return "", err
	}

	if len(items) == 0 {
		return "📅 <b>JADWAL PERKULIAHAN</b>\n\nTidak ada jadwal perkuliahan yang ditemukan.", nil
	}

	nowWIB := now.In(WIBLocation)
	todayVal := int(nowWIB.Weekday())
	if todayVal == 0 {
		todayVal = 7
	}

	var sb strings.Builder
	sb.Grow(len(items) * 128)
	sb.WriteString("📅 <b>JADWAL PERKULIAHAN</b>\n")

	currentDayVal := -1
	for _, item := range items {
		dayVal := item.DayValue()
		if dayVal != currentDayVal {
			currentDayVal = dayVal
			dayName := strings.ToUpper(strings.TrimSpace(item.Hari))
			if dayName == "" {
				dayName = "LAINNYA"
			}
			dayName = html.EscapeString(dayName)
			if dayVal == todayVal {
				sb.WriteString(fmt.Sprintf("\n📌 <b>%s (HARI INI)</b>\n", dayName))
			} else {
				sb.WriteString(fmt.Sprintf("\n📌 <b>%s</b>\n", dayName))
			}
		}

		mk := html.EscapeString(item.CourseName())
		jamAwal := html.EscapeString(item.JamAwal)
		jamAkhir := html.EscapeString(item.JamAkhir)
		ruang := html.EscapeString(item.Ruang)
		dosen := html.EscapeString(item.Dosen)

		sb.WriteString(fmt.Sprintf("• <b>%s</b>\n  🕒 %s - %s", mk, jamAwal, jamAkhir))
		if ruang != "" {
			sb.WriteString(fmt.Sprintf(" | 📍 %s", ruang))
		}
		if dosen != "" {
			sb.WriteString(fmt.Sprintf(" | 👨‍🏫 %s", dosen))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String()), nil
}

func parseClockToTime(clockStr string, base time.Time) (time.Time, error) {
	s := strings.TrimSpace(clockStr)
	sep := strings.IndexAny(s, ":.")
	if sep <= 0 || sep+1 >= len(s) {
		return time.Time{}, fmt.Errorf("invalid clock time: %q", clockStr)
	}
	hour, err := strconv.Atoi(s[:sep])
	if err != nil {
		return time.Time{}, err
	}
	minute, err := strconv.Atoi(s[sep+1:])
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(base.Year(), base.Month(), base.Day(), hour, minute, 0, 0, WIBLocation), nil
}
