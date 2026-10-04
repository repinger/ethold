# Repository Guidelines

## Project Overview
Ethold is an automated attendance and academic notification daemon for Politeknik Elektronika Negeri Surabaya (PENS). The daemon authenticates to ETHOL via Central Authentication Service (CAS) SSO, monitors active lecture sessions, submits attendance tokens automatically, and serves interactive academic queries through a Telegram bot.

Operating modes:
- **Auto-Presence Mode** (`ETHOL_AUTO_PRESENCE=true`): Runs scheduled presence checks (every 60 seconds in class windows, every 15 minutes during background sweeps), submits student attendance keys, and records persistent state.
- **Academic-Only Mode** (default): Disables scanning and submission loops. Provides interactive Telegram bot commands and notification polling only.

## Architecture & Data Flow
The daemon uses a flat, single-package architecture (`internal/ethol`) centered on the `Scanner` orchestrator.

### Component Structure
- **Entry Point (`cmd/ethold/main.go`)**: Parses CLI flags and `.env` configuration, binds OS signal cancellation, initializes service singletons, starts background workers, and executes single-pass or continuous scanning.
- **HTTP Transport (`client.go`)**: Manages `http.Client` with `syncCookieJar` (thread-safe cookie storage) and `headerTransport` (browser profile rotation and header spoofing for `*.pens.ac.id`).
- **Authentication Engine (`auth.go`)**: Coordinates the four-step CAS login flow (redirect, HTML form parsing, credential POST, token validation) and session refreshes. Employs double-checked locking with `refreshMu` and `mu`.
- **Presence Engine (`presence.go`)**: Queries `/api/presensi/aktif-kuliah`, extracts session keys using `bytes.Buffer` pools, and sends attendance submissions to `/api/presensi/mahasiswa`.
- **Scheduler & Planner (`scheduler.go`)**: Hardcodes Western Indonesia Time (WIB, UTC+7). Calculates daily lecture scan windows with 15-minute lead and 20-minute trail buffers.
- **Scanner Daemon (`scanner.go`)**: Coordinates scan loops, worker pool concurrency, random submission jitter (2 to 10 seconds), and error recovery (session re-login on HTTP 401).
- **Academic Manager (`academic.go`, `academic_*.go`)**: Manages generic in-memory caches (`cacheEntry[T]`) with TTL expiration for schedules, assignments, materials, announcements, exams, and attendance statistics.
- **Telegram Interface (`telegram.go`, `commands.go`)**: Routes inbound bot updates, enforces token bucket rate limiting, separates notifications and commands by thread ID, and edits messages in place with `editMessageText`.
- **State Storage (`state.go`)**: Provides thread-safe, atomic JSON file persistence for attended lecture keys.

### Data Flow
```
Timer / Schedule / Telegram Command
       │
       ▼
   Scanner ──(evaluates window)──> Scheduler (WIB UTC+7)
       │
       ├─ Worker Pool (goroutines limited by -concurrency)
       │       │
       │       ▼
       │   PresenceEngine ──> HTTP Client (CAS cookies, User-Agent) ──> ETHOL API
       │       │
       │       ▼
       │   StateManager (writes atomically to attended_keys.json)
       │
       ▼
TelegramNotifier (delivers alert or updates message in topic thread)
```

## Key Directories
- `cmd/ethold/`: Application entry point and build-tagged pprof listeners (`main.go`, `pprof_dev.go`, `pprof_release.go`).
- `internal/ethol/`: Domain logic, API clients, HTML extractors, scheduler, scanner, and Telegram bot handlers.
- `docs/`: Technical documentation covering architecture, component details, API reference, authentication, and deployment.
- `.agents/skills/`: Installed agent skills for development workflows.

## Development Commands

### Build
```bash
# Release build (default: stripped binaries, disabled pprof and dev logs)
go build -trimpath -ldflags="-s -w" -o ethold ./cmd/ethold

# Development build (enables pprof HTTP server and verbose debug logs)
go build -tags dev -o ethold ./cmd/ethold
```

### Run
```bash
# Run daemon with local .env file
./ethold -config .env

# Execute a single scan pass and exit
./ethold -config .env -once

# Start development build with pprof profiling enabled
PPROF_ADDR="localhost:6060" ./ethold -config .env
```

### Test & Benchmark
```bash
# Run all unit tests (release configuration)
go test -v ./...

# Run all unit tests with dev build tag
go test -tags dev -v ./...

# Run single test
go test -v -run TestCheckCourse ./internal/ethol

# Run soak and memory leak gating check
go test -v -count=1 -run TestSoakMemory ./internal/ethol

# Run all benchmarks
go test -run='^$' -bench=. -benchmem ./internal/ethol

# Run specific benchmark
go test -run='^$' -bench=BenchmarkScanner_ScanOnce_10Courses -benchmem ./internal/ethol
```

### Lint & Code Quality
```bash
# Run golangci-lint on release build
golangci-lint run ./...

# Run golangci-lint on dev build
golangci-lint run --build-tags dev ./...

# Analyze dead code on release build
go run golang.org/x/tools/cmd/deadcode@latest ./...

# Analyze dead code on dev build
go run golang.org/x/tools/cmd/deadcode@latest -tags dev ./...
```

## Code Conventions & Common Patterns

### Formatting & Style
- Standard Go formatting using `gofmt` and `goimports`.
- Struct tags follow standard JSON camelCase or snake_case matching ETHOL API responses.
- Standard library only for logging and assertions (`log/slog` and `testing`).

### Logging Conventions (`log/slog`)
- Follow `sloglint` rules strictly:
  - Provide key-value arguments only.
  - Keep log messages static string literals. Never use dynamic string formatting inside log messages.
  - Valid: `slog.Info("attendance submitted", "course", name, "key", key)`
  - Invalid: `slog.Info(fmt.Sprintf("submitted %s", name))`
- Dev-only verbose traces must use `devLog`, `devLogHTTP`, and `devLogTelegramCommand` helpers (stubbed out in release builds).

### Error Handling
- Wrap underlying errors with `%w`: `fmt.Errorf("parse CAS form: %w", err)`.
- Inspect typed errors using `errors.Is` and `errors.As`.
- Expose sentinel errors when callers must branch on the failure mode (`ErrUnauthorized`, `ErrNoActivePresence`, `ErrAlreadyAttended`).
- Linter rules require specific explanations for any `//nolint:errcheck // reason` directives.

### Concurrency & Asynchronous Patterns
- Propagate `context.Context` to all network and blocking calls.
- Bounded concurrency: manage workers through buffered job channels and `sync.WaitGroup`.
- Double-checked locking: coordinate multi-step authentication through dedicated mutexes (`refreshMu` serializes network login, `mu` protects user session fields).
- Jitter and backoff: use `math/rand/v2` for random jitter in scan intervals and exponential backoff for network retries.
- Timers: initialize with `time.NewTicker` and release via `defer ticker.Stop()`.

### Dependency Injection
- Inject dependencies explicitly via constructors in `cmd/ethold/main.go`.
- Structs expose constructor functions (for example, `NewAuthManager`, `NewScanner`).
- Avoid mutable package-level globals. Shared state resides in the cookie jar managed by `client.go`.

### State Management
- `StateManager` manages `attended_keys.json` using `sync.RWMutex`.
- Atomic writes: writes JSON to a temporary file (`<path>.tmp`), flushes file descriptors, and renames atomically to target path (`os.Rename`).
- State pruning: purges entries older than `StateRetentionDays` (default 90 days) during startup loads.

## Important Files
- `cmd/ethold/main.go`: Application entrypoint, CLI flag definitions, and service wiring.
- `cmd/ethold/pprof_dev.go` & `cmd/ethold/pprof_release.go`: Build-tagged pprof HTTP server pair.
- `internal/ethol/client.go`: HTTP client with user-agent rotation and synchronized cookie jar.
- `internal/ethol/auth.go`: CAS SSO login lifecycle and session refresh handlers.
- `internal/ethol/presence.go`: Presence session detection and attendance submission engine.
- `internal/ethol/scheduler.go`: WIB timetable window computations and scan interval planning.
- `internal/ethol/scanner.go`: Main daemon loop, worker pool management, and retry handling.
- `internal/ethol/commands.go`: Telegram command routing, rate limiting, and response templates.
- `internal/ethol/state.go`: Atomic JSON file persistence for attended keys.
- `internal/ethol/config.go`: Custom `.env` parser implementation without third-party dependencies.
- `.golangci.yml`: Strict linter configuration for CI and local verification.
- `Dockerfile`: Multi-stage distroless build specifying `GOMEMLIMIT=48MiB`.

## Runtime/Tooling Preferences
- **Runtime**: Go `1.27.1` (declared in `go.mod`).
- **Dependencies**: Minimal dependency policy. Only one direct external dependency allowed: `golang.org/x/net`. Do not add third-party frameworks or libraries.
- **Package Management**: Standard Go modules (`go mod tidy`, `go mod verify`).
- **Container Environment**: Distroless non-root image (`gcr.io/distroless/static-debian13:nonroot`) with `CGO_ENABLED=0`. Memory ceiling configured at `GOMEMLIMIT=48MiB`.
- **Timezone**: Western Indonesia Time (`WIB`, UTC+7) required for all schedule and window calculations.

## Testing & QA

### Testing Framework
- Standard Go `testing` package exclusively. No external assertion or mocking packages.
- White-box tests reside in `internal/ethol/*_test.go` under `package ethol`.
- Isolate file system tests using `t.TempDir()`.

### Test Conventions
- Structure tests as table-driven cases using `t.Run`.
- Use `httptest.NewServer` to mock upstream ETHOL endpoints and CAS redirects.
- In test helpers, set sleep delays and worker jitter to zero (`minDelay = 0`, `maxDelay = 0`) to eliminate flaky timing.
- Write test setup functions with `tb testing.TB` parameters to share setup across unit tests and benchmarks.

### Soak & Memory Leak Gating (`TestSoakMemory`)
Run the soak test before completing changes:
```bash
go test -v -count=1 -run TestSoakMemory ./internal/ethol
```
Mandatory gating criteria:
1. **Zero Net Goroutine Growth**: `goroutinesEnd <= goroutinesStart`.
2. **Heap Growth Ceiling**: HeapAlloc delta must not exceed 400 KB (`HeapAlloc delta <= 400 * 1024`).

### Benchmark Regression Gating
Run benchmarks before and after modifying performance-sensitive code:
```bash
go test -run='^$' -bench=. -benchmem ./internal/ethol
```
Do not introduce regressions in execution time (`ns/op`), memory allocation (`B/op`), or allocation counts (`allocs/op`).

### Dead Code Elimination
Run deadcode checks before finalizing commits:
```bash
go run golang.org/x/tools/cmd/deadcode@latest ./...
go run golang.org/x/tools/cmd/deadcode@latest -tags dev ./...
```
Remove all dead or unreachable production code.

## Documentation Maintenance
When changing code that modifies behavior, APIs, CLI flags, Telegram commands, configuration, or system architecture, update the corresponding documents in `docs/` in the same commit series.

## AI Agent Rules & Skill Loading

AI agents MUST load applicable skills before inspecting, editing, or testing code:

| Task | Required Skill(s) |
|---|---|
| Write / refactor / review Go code | `golang-patterns` |
| Concurrency, goroutines, channels | `golang-patterns`, `golang-concurrency` |
| Tests, benchmarks | `golang-patterns`, `golang-testing` |
| Performance, profiling, hot paths | `golang-patterns`, `golang-performance` |
| Error types, wrapping, error paths | `golang-patterns`, `golang-error-handling` |
| Dockerfile, docker-compose | `docker-patterns` |
| Bug diagnosis, test failure, panic, race | `systematic-debugging`, `golang-troubleshooting` |
| Technical documentation | `asd-ste100` |

### Commit Standards
- Style: `subsystem: imperative summary` (maximum 50 characters).
- Body: Detail technical solution and rationale, wrapped at 72 columns.
- AI agents MUST NOT append `Signed-off-by` tags.
- AI-assisted commits MUST include:
  `Assisted-by: <model-name> <tool>`
