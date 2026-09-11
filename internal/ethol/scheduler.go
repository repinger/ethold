package ethol

import (
	"math/rand/v2"
	"time"
)

var WIBLocation = time.FixedZone("WIB", 7*3600)

type ScheduleMode struct {
	Name     string
	Interval time.Duration
}

const (
	ModeNightRest = "ISTIRAHAT_MALAM"
	ModeDawn      = "SIAGA_SUBUH"
	ModeNormal    = "SIAGA_NORMAL"
)

func NowWIB() time.Time {
	return time.Now().In(WIBLocation)
}

func TodayDate(t time.Time) string {
	return t.In(WIBLocation).Format("2006-01-02")
}

func GetScheduleMode(t time.Time) ScheduleMode {
	wib := t.In(WIBLocation)
	timeVal := float64(wib.Hour()) + float64(wib.Minute())/60.0

	if timeVal >= 21.5 || timeVal < 4.0 {
		return ScheduleMode{Name: ModeNightRest, Interval: 300 * time.Second}
	}
	if timeVal >= 4.0 && timeVal < 6.5 {
		return ScheduleMode{Name: ModeDawn, Interval: 180 * time.Second}
	}
	return ScheduleMode{Name: ModeNormal, Interval: 60 * time.Second}
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

