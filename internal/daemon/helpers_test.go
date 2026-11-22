package daemon

import (
	"github.com/repinger/ethold/internal/ethol"
	"github.com/repinger/ethold/internal/telegram"
)

var (
	NewHTTPClient       = ethol.NewHTTPClient
	NewAuthManager      = ethol.NewAuthManager
	NewCourseManager    = ethol.NewCourseManager
	NewPresenceEngine   = ethol.NewPresenceEngine
	NewAcademicManager  = ethol.NewAcademicManager
	NewTelegramNotifier = telegram.NewNotifier
	NowWIB              = ethol.NowWIB
	TodayDate           = ethol.TodayDate
	ComputeScanWindows  = ethol.ComputeScanWindows
	ErrUnauthorized     = ethol.ErrUnauthorized
)

const (
	ActiveInterval     = ethol.ActiveInterval
	BackgroundInterval = ethol.BackgroundInterval
)

type (
	Course          = ethol.Course
	ScanPlan        = ethol.ScanPlan
	ScanWindow      = ethol.ScanWindow
	ScheduleItem    = ethol.ScheduleItem
	UserInfo        = ethol.UserInfo
	PresenceEngine  = ethol.PresenceEngine
	AuthManager     = ethol.AuthManager
	CourseManager   = ethol.CourseManager
	AcademicManager = ethol.AcademicManager
)
