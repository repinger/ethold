package ethol

import (
	"math/rand/v2"
	"sort"
	"time"
)

var WIBLocation = time.FixedZone("WIB", 7*3600)

const (
	ActiveInterval     = 60 * time.Second
	BackgroundInterval = 15 * time.Minute
	WindowLeadTime     = 15 * time.Minute
	WindowTrailTime    = 20 * time.Minute
)

type ScanWindow struct {
	Start   time.Time
	End     time.Time
	Courses []Course
}

type ScanPlan struct {
	Interval time.Duration
	Courses  []Course
	InWindow bool
}

func ComputeScanWindows(items []ScheduleItem, courses []Course, day time.Time) []ScanWindow {
	dayWIB := day.In(WIBLocation)
	targetDayVal := int(dayWIB.Weekday())
	if targetDayVal == 0 {
		targetDayVal = 7
	}

	var raw []ScanWindow
	for _, item := range items {
		if item.DayValue() != targetDayVal {
			continue
		}

		start, err := parseClockToTime(item.JamAwal, dayWIB)
		if err != nil {
			continue
		}
		end, err := parseClockToTime(item.JamAkhir, dayWIB)
		if err != nil {
			continue
		}

		wStart := start.Add(-WindowLeadTime)
		wEnd := end.Add(WindowTrailTime)

		var matchedCourses []Course
		if c := matchCourse(item, courses); c != nil {
			matchedCourses = append(matchedCourses, *c)
		} else if item.Kuliah > 0 {
			matchedCourses = append(matchedCourses, Course{
				Nomor:          item.Kuliah,
				KuliahAsal:     item.Kuliah,
				NamaMatakuliah: item.CourseName(),
				Dosen:          item.Dosen,
			})
		}

		raw = append(raw, ScanWindow{
			Start:   wStart,
			End:     wEnd,
			Courses: matchedCourses,
		})
	}

	if len(raw) == 0 {
		return nil
	}

	sort.SliceStable(raw, func(i, j int) bool {
		return raw[i].Start.Before(raw[j].Start)
	})

	merged := []ScanWindow{raw[0]}
	for i := 1; i < len(raw); i++ {
		prev := &merged[len(merged)-1]
		cur := raw[i]

		if !cur.Start.After(prev.End) {
			if cur.End.After(prev.End) {
				prev.End = cur.End
			}
			for _, cc := range cur.Courses {
				exists := false
				for _, ec := range prev.Courses {
					if ec.Nomor == cc.Nomor {
						exists = true
						break
					}
				}
				if !exists {
					prev.Courses = append(prev.Courses, cc)
				}
			}
		} else {
			merged = append(merged, cur)
		}
	}

	return merged
}

func NextScanPlan(now time.Time, windows []ScanWindow) ScanPlan {
	nowWIB := now.In(WIBLocation)
	for _, w := range windows {
		if (nowWIB.Equal(w.Start) || nowWIB.After(w.Start)) && (nowWIB.Equal(w.End) || nowWIB.Before(w.End)) {
			return ScanPlan{
				Interval: ActiveInterval,
				Courses:  w.Courses,
				InWindow: true,
			}
		}
	}

	var nextWindow *ScanWindow
	for i := range windows {
		if windows[i].Start.After(nowWIB) {
			nextWindow = &windows[i]
			break
		}
	}

	interval := BackgroundInterval
	if nextWindow != nil {
		gap := nextWindow.Start.Sub(nowWIB)
		if gap > 0 && gap < BackgroundInterval {
			interval = gap
		}
	}

	return ScanPlan{
		Interval: interval,
		Courses:  nil,
		InWindow: false,
	}
}

func NowWIB() time.Time {
	return time.Now().In(WIBLocation)
}

func TodayDate(t time.Time) string {
	return t.In(WIBLocation).Format("2006-01-02")
}

func calculateJitter(base time.Duration, fraction float64) time.Duration {
	if base <= 0 || fraction <= 0 {
		return base
	}
	// ponytail: uniform random jitter; upgrade to configurable distribution if required.
	deltaRange := int64(float64(base) * fraction * 2)
	if deltaRange <= 0 {
		return base
	}
	offset := time.Duration(rand.Int64N(deltaRange+1)) - time.Duration(int64(float64(base)*fraction))
	interval := base + offset
	if interval < time.Millisecond {
		return time.Millisecond
	}
	return interval
}

