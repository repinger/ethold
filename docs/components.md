# Components

This document describes each module in the `internal/ethol` package.

## Module Summary

| File | Primary Role |
|---|---|
| `config.go` | Reads and parses `.env` files. |
| `client.go` | Creates HTTP clients with browser fingerprints and cookie jars. |
| `auth.go` | Executes CAS single sign-on and maintains active user sessions. |
| `courses.go` | Fetches and caches student course registrations. |
| `presence.go` | Checks course attendance status and submits attendance codes. |
| `scheduler.go` | Calculates scan intervals based on Western Indonesia Time (WIB). |
| `scanner.go` | Orchestrates background scanning, worker queues, and daemon lifecycle. |
| `commands.go` | Routes and formats Telegram bot commands. |
| `academic.go` | Academic manager struct, cache lifecycle, and common utilities. |
| `academic_schedule.go` | Retrieves timetable schedules, matches active courses, and formats schedule views. |
| `academic_tasks.go` | Queries course assignments, validates submission status, and formats task lists. |
| `academic_materials.go` | Fetches lecture materials, video recordings, and formats material lists. |
| `academic_attendance.go` | Computes attendance statistics, retrieves class rosters, and formats recap data. |
| `academic_notif.go` | Polls ETHOL notifications, marks items as read, and handles alert dispatch. |
| `debug.go` | Handles the `/debug` Telegram command, reporting runtime diagnostics. |
| `log.go` | Formats colored, neat CLI log output for `slog`. |
| `state.go` | Persists attendance keys and records to disk atomically. |
| `telegram.go` | Sends messages and polls updates using the Telegram Bot API. |

---

## 1. Config (`config.go`)

This file parses configuration key-value pairs from files.

### Key Types

- `Config`: Holds runtime configuration fields:
  - `Username string`
  - `Password string`
  - `TelegramToken string`
  - `TelegramChatID string`
  - `AutoPresence bool`

### Primary Functions

- `LoadConfig(path string, overrides ...Config) (*Config, error)`: Resolves configuration values with precedence (overrides > system environment variables > file at `path`), and validates required credentials.

---

## 2. HTTP Client (`client.go`)

This file configures the underlying HTTP transport for all network communication.

### Features

- **Cookie Jar:** Uses `net/http/cookiejar` so that all requests share session cookies.
- **Browser Profile:** Selects a realistic browser header profile on startup (Chrome, Firefox, Safari, or Edge).
- **Header Injection:** Wraps transport with `headerTransport` to add `User-Agent`, `Accept-Language`, and `Sec-CH-UA` headers to target hosts.
- **Connection Pooling:** Configures up to 64 idle connections (32 per host), 90-second idle timeout, 30-second total request timeout.

### Primary Functions

- `NewHTTPClient() (*http.Client, error)`: Returns a configured client instance with shared cookie jar and custom transport.

---

## 3. Authentication (`auth.go`)

This file manages the CAS login lifecycle.

### Key Types

- `UserInfo`: Represents the authenticated user:
  - `Nomor int`: Internal user number.
  - `Nama string`: Full name of the user.
  - `NipNrp string`: Academic identifier or NRP.
- `AuthManager`: Thread-safe manager that holds current credentials and active user data.

### Primary Functions

- `NewAuthManager(client, baseURL, username, password) *AuthManager`: Constructs a new manager.
- `(a *AuthManager) Login(ctx context.Context) (*UserInfo, error)`: Performs the 4-step CAS login flow.
- `(a *AuthManager) Relogin(ctx context.Context) (*UserInfo, error)`: Resets the cookie jar and performs a full login; used by `/relogin`.
- `(a *AuthManager) EnsureSession(ctx context.Context) error`: Refreshes session via `/api/auth/refresh` or triggers a new login.
- `(a *AuthManager) User() *UserInfo`: Returns the current user profile in a thread-safe manner.

---

## 4. Courses (`courses.go`)

This file fetches enrolled courses and caches the results in memory.

### Key Types

- `Course`: Represents an academic course:
  - `Nomor int`: Course registration identifier.
  - `JenisSchema int`: Scheme category.
  - `KuliahAsal int`: Source course reference ID.
  - `Dosen string`: Name of the primary lecturer.
  - `CourseName() string`: Method that parses polymorphic name fields into a string.
- `CourseManager`: Caches course lists using a configured time-to-live (TTL) duration.

### Primary Functions

- `NewCourseManager(client, baseURL, ttl) *CourseManager`: Creates the manager with the specified cache TTL.
- `(cm *CourseManager) GetCourses(ctx) ([]Course, error)`: Returns cached courses if fresh, or refreshes from API.
- `(cm *CourseManager) Refresh(ctx) ([]Course, error)`: Forces an update from `/api/kuliah`.
- `(cm *CourseManager) ActivePeriod(ctx) (year, semester int, error)`: Queries `/api/auth/config` for active term values.
- `(cm *CourseManager) CachedCourses() []Course`: Returns the current in-memory course list without a network call.
- `(cm *CourseManager) CachedCount() int`: Returns the number of cached courses.
- `(cm *CourseManager) CachedActivePeriod() (year, semester int, ok bool)`: Returns cached year/semester without a network call.
- `(cm *CourseManager) CacheAge() (time.Duration, bool)`: Returns how long ago the cache was last populated.

---

## 5. Presence Engine (`presence.go`)

This file inspects and submits course attendance.

### Key Types

- `PresenceEngine`: Handles attendance API communication.

### Primary Functions

- `NewPresenceEngine(client, baseURL) *PresenceEngine`: Constructs the engine.
- `(pe *PresenceEngine) CheckCourse(ctx, course) (key string, open bool, error)`: Calls `/api/presensi/aktif-kuliah`. Returns true when attendance is open.
- `(pe *PresenceEngine) Submit(ctx, course, key, studentID) (msg string, ok bool, error)`: Sends attendance submission to `/api/presensi/mahasiswa`.

---

## 6. Scheduler (`scheduler.go`)

This file manages schedule-driven scan timing based on class schedules and Western Indonesia Time (WIB, UTC+7).

### Scan Tiers

| Tier | Condition | Base Interval | Scope |
|---|---|---|---|
| Active Window | Inside class time window (`start - 15m` to `end + 20m`) | 60 seconds | Targeted course(s) only |
| Background Sweep | Outside active class windows | 15 minutes | All enrolled courses (ad-hoc catch) |

### Primary Functions

- `NowWIB() time.Time`: Returns the current time in the WIB timezone.
- `TodayDate(t time.Time) string`: Returns date formatted as `YYYY-MM-DD`.
- `ComputeScanWindows(items []ScheduleItem, courses []Course, day time.Time) []ScanWindow`: Calculates and merges active scan windows for a given day.
- `NextScanPlan(now time.Time, windows []ScanWindow) ScanPlan`: Computes scan target courses and wait duration for the next cycle.

---

## 7. Scanner (`scanner.go`)

This file orchestrates scanning operations, concurrency, and daemon lifecycle.

### Key Types

- `Scanner`: The central daemon coordinator.
- `ScannerStatus`: Snapshot struct containing operational metrics, memory stats, and scan counters.

### Primary Functions

- `NewScanner(auth, courses, presence, academic, state, notifier, concurrency) *Scanner`: Constructs a new scanner.
- `(s *Scanner) Run(ctx context.Context) error`: Executes the continuous loop until the context cancels.
- `(s *Scanner) ScanOnce(ctx context.Context) (int, error)`: Runs a single scan pass across all courses.
- `(s *Scanner) TryScanOnce(ctx context.Context) (int, bool, error)`: Non-blocking variant; returns `false` if a scan is already in progress.
- `(s *Scanner) ScanCourses(ctx context.Context, courses []Course) (int, error)`: Scans a specific subset of courses.
- `(s *Scanner) TryScanCourses(ctx context.Context, courses []Course) (int, bool, error)`: Non-blocking variant of `ScanCourses`.
- `(s *Scanner) Status() ScannerStatus`: Returns a snapshot of current operational metrics.

---

## 7.1 Telegram Commands (`commands.go`)

This file handles routing and response formatting for interactive Telegram bot commands.

### Primary Functions

- `(s *Scanner) HandleTelegramCommand(ctx context.Context, cmd string) string`: Routes and executes Telegram bot commands.

---

## 8. Academic Manager (`academic.go`, `academic_*.go`)

The academic module queries academic information including timetables, assignments, materials, videos, student rosters, and notifications. Logic is organized across specialized files within the `ethol` package:

- `academic.go`: Defines `AcademicManager`, cache containers, TTL cache invalidation, and common parsing utilities.
- `academic_schedule.go`: Queries class timetables, formats schedule views, and determines currently active courses by comparing clock ranges against current WIB time.
- `academic_tasks.go`: Retrieves assignment lists across courses, checks individual student submission status, filters open deadlines, and formats task messages.
- `academic_materials.go`: Fetches downloadable lecture files and external video links for monitored courses, formatting them with HTML links.
- `academic_attendance.go`: Queries student attendance history (`riwayat`), calculates semester attendance percentages, and inspects live class attendance rosters.
- `academic_notif.go`: Manages background polling of ETHOL notifications, tracking seen IDs to prevent duplicate alerts, and triggering callbacks on new presence, task, or other system notifications.

### Key Types

- `AcademicManager`: Central coordinator struct managing HTTP client, caches, and sync locks.
- `ScheduleItem`: Represents a scheduled class period.
- `TaskItem`: Represents an assignment with due dates and submission state.
- `MaterialItem`: Represents course documents and lecture slides.
- `VideoItem`: Represents recorded lectures.
- `RosterItem`: Contains attendee student ID and name.
- `AttendanceStats`: Contains semester and daily presence statistics.

### Primary Functions

- `NewAcademicManager(client, baseURL, cacheTTL) *AcademicManager`: Creates a new academic manager with shared caching.
- `(am *AcademicManager) GetSchedule(ctx, year, semester)`: Queries class schedules (`academic_schedule.go`).
- `(am *AcademicManager) GetActiveCourse(ctx, now, year, semester, courses)`: Determines the current course based on timetable (`academic_schedule.go`).
- `(am *AcademicManager) CachedActiveCourse(now, year, semester, courses)`: Non-network variant using the cached schedule only.
- `(am *AcademicManager) GetPendingTasks(ctx, courses)`: Fetches open assignments (`academic_tasks.go`).
- `(am *AcademicManager) GetCourseMaterials(ctx, courses)`: Fetches uploaded materials (`academic_materials.go`).
- `(am *AcademicManager) GetCourseVideos(ctx, courses)`: Fetches recorded videos (`academic_materials.go`).
- `(am *AcademicManager) FormatAttendanceStatsText(ctx, now, year, semester, studentID, courses)`: Computes and formats attendance statistics (`academic_attendance.go`).
- `(am *AcademicManager) GetAttendanceRoster(ctx, course, key)`: Fetches attendees for a session (`academic_attendance.go`).
- `FormatRosterText(course, key, attendees, totalEnrolled)`: Standalone formatter for session attendee roster (`academic_attendance.go`).
- `(am *AcademicManager) FormatScheduleText(ctx, now, year, semester)`: Formats full schedule message (`academic_schedule.go`).
- `(am *AcademicManager) FormatTasksText(ctx, courses)`: Formats pending assignments message (`academic_tasks.go`).
- `(am *AcademicManager) FormatMaterialsText(ctx, courses)`: Formats materials and recorded video links (`academic_materials.go`).
- `(am *AcademicManager) StartNotificationPoller(ctx, interval, authFn, onPres, onTask, onOther)`: Periodically polls ETHOL notifications (`academic_notif.go`).
- `(am *AcademicManager) InvalidateAttendanceCache()`: Clears the attendance stats cache; called after a successful presence submission (`academic.go`).
- `(am *AcademicManager) CacheStats() AcademicCacheStats`: Returns entry counts for all in-memory caches (`academic.go`).

---

## 9. State Manager (`state.go`)

This file manages disk persistence for recorded attendance keys.

### Key Types

- `PresenceRecord`: Contains metadata for an attended session:
  - `Key string`
  - `CourseName string`
  - `Dosen string`
  - `Time string`
- `StateManager`: Manages in-memory maps and disk synchronization.

### Primary Functions

- `NewStateManager(path string) (*StateManager, error)`: Loads existing state and verifies file permissions.
- `(sm *StateManager) Path() string`: Returns the configured state file path.
- `(sm *StateManager) Has(key string) bool`: Checks if a key was already recorded.
- `(sm *StateManager) Count() int`: Returns total count of recorded presence keys.
- `(sm *StateManager) CountWithPrefix(prefix string) int`: Returns count of keys matching date prefix.
- `(sm *StateManager) AddRecord(records ...PresenceRecord) error`: Adds records and writes atomically to disk.
- `(sm *StateManager) RecordsWithPrefix(prefix string) []PresenceRecord`: Returns records matching a date prefix.

---

## 10. Debug (`debug.go`)

This file handles the `/debug` Telegram command.

### Primary Functions

- `(s *Scanner) handleDebug(ctx context.Context) string`: Formats a full diagnostic report including:
  - Go runtime version, OS, architecture, PID, CPU count, goroutine count.
  - Memory statistics: `Alloc`, `TotalAlloc`, `Sys`, `HeapInuse`, `HeapObjects`, GC cycles.
  - Daemon state: uptime, concurrency, min/max delay and stagger ranges, scan mode, last scan time.
  - Storage: state file path, recorded key count, course cache stats, academic cache stats.
  - CAS session: currently authenticated user info.

---

## 11. Telegram Notifier (`telegram.go`)

This file interfaces with the Telegram Bot API.

### Key Types

- `TelegramNotifier`: Manages outgoing alerts and incoming command updates.

### Primary Functions

- `NewTelegramNotifier(client, baseURL, token, chatID) *TelegramNotifier`: Creates the notifier.
- `(tn *TelegramNotifier) SendMessage(ctx, text string) error`: Sends an HTML formatted message.
- `(tn *TelegramNotifier) SendMessageIDs(ctx context.Context, text string) ([]int64, error)`: Sends formatted message chunks and returns their message IDs.
- `(tn *TelegramNotifier) DeleteMessages(ctx context.Context, messageIDs []int64) error`: Deletes messages in batches of up to 100 using Telegram's `deleteMessages` endpoint.
- `(tn *TelegramNotifier) NotifyPresenceSuccess(ctx, course, lecturer, key, msg) error`: Sends formatted attendance alerts.
- `(tn *TelegramNotifier) NotifyServerError(ctx context.Context, err error) error`: Sends server outage notification.
- `(tn *TelegramNotifier) NotifyServerRecovery(ctx context.Context) error`: Sends server recovery notification.
- `(tn *TelegramNotifier) NotifyAuthFailure(ctx context.Context, err error) error`: Sends authentication failure alert.
- `(tn *TelegramNotifier) PollOnce(ctx context.Context, offset int64, handler func(ctx context.Context, cmd string) string) (int64, error)`: Fetches single update batch from Telegram API.
- `(tn *TelegramNotifier) StartCommandPoller(ctx, handler)`: Runs an update polling loop.

---

## 12. Log Handler (`log.go`)

This file implements a custom `slog.Handler` formatting records for terminal readability.

### Key Types

- `PrettyHandlerOptions`: Configures minimum level and color preferences (`NoColor`, `ForceColor`).
- `PrettyHandler`: Custom `slog.Handler` rendering compact timestamped, color-coded level badges and attributes.

### Primary Functions

- `NewPrettyHandler(w io.Writer, opts *PrettyHandlerOptions) *PrettyHandler`: Instantiates the CLI handler with auto-detected TTY and `NO_COLOR` support. ANSI colors are automatically suppressed when standard output is not a terminal (e.g. piped or Docker without `tty: true`).

