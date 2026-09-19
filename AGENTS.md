# AGENTS.md

## Project

Go 1.27.1 daemon that auto-submits attendance on ETHOL PENS via CAS SSO. Single binary, single `internal/ethol` package, one external dep (`golang.org/x/net` for HTML tokenizer). Auto-presence is opt-in (`ETHOL_AUTO_PRESENCE=true`); without it the daemon runs in academic-only mode (Telegram commands, notifications, no scanning).

## Commands

```bash
go build -o ethold ./cmd/ethold   # build
go test -v ./...                                           # all tests (stdlib only, no framework)
go test -v -run TestFoo ./internal/ethol                   # single test
go test -run='^$' -bench=. -benchmem ./internal/ethol      # all benchmarks
go test -run='^$' -bench=BenchmarkFoo -benchmem ./internal/ethol # single benchmark
go vet ./...                                               # vet
golangci-lint run ./...                                    # lint
golangci-lint run --build-tags dev ./...                   # lint dev build
go run golang.org/x/tools/cmd/deadcode@latest ./...        # dead code analysis
go run golang.org/x/tools/cmd/deadcode@latest -tags dev ./... # dev build dead code analysis
```

Linter config at `.golangci.yml`. Tests use `t.TempDir()` for isolation; no external services or fixtures required.

## Structure

```
cmd/ethold/main.go   # entrypoint, flag parsing, wiring
internal/ethol/                  # all domain code (flat, single package)
  config.go      # custom .env parser
  client.go      # http.Client with cookie jar
  auth.go        # CAS SSO login, session refresh, relogin
  courses.go     # course list with TTL cache
  presence.go    # presence check/submit
  scheduler.go   # WIB timezone, schedule-driven windows, and scan plans
  scanner.go     # main daemon loop, worker pool, scan planning
  commands.go    # Telegram command routing and response formatting
  academic.go               # academic manager struct, cache lifecycle
  academic_schedule.go      # schedule, active course matching, formatting
  academic_exams.go         # exam schedules (UTS/UAS), formatting
  academic_tasks.go         # task list, submission status, sorting, formatting
  academic_materials.go     # materials and videos fetch, formatting
  academic_announcements.go # campus announcements fetch, formatting
  academic_attendance.go    # class roster, attendance stats, riwayat
  academic_notif.go         # notification polling and mark-read
  debug.go                  # /debug Telegram command, runtime diagnostics
  log.go                    # pretty CLI log handler with color support

  state.go       # atomic JSON persistence (attended_keys.json)
  telegram.go    # Telegram Bot API (sendMessage, long-poll getUpdates)
```

Tests are `*_test.go` beside each source file, same `package ethol` (white-box).

## Conventions

- Commit style: `subsystem: imperative summary` (max 75 chars)
  - Body: describe problem and technical solution in detail, wrapped at 75 columns
  - Commits MUST be small and bisectable: each commit is a single logical unit that compiles and passes tests independently; split refactors, features, and fixes across separate commits
  - AI agents MUST NOT add `Signed-off-by` tags (only human contributors certify DCO)
  - AI-assisted commits MUST include: `Assisted-by: <model-name> <tool>` (e.g. `Assisted-by: gemini-3.8-flash-medium Antigravity`)
- Benchmark baseline & regression gating:
  - AI agents MUST run relevant benchmarks before modifying code to establish a baseline (`go test -run='^$' -bench=. -benchmem ./internal/ethol`).
  - After making modifications, re-run benchmarks and compare `ns/op`, `B/op`, and `allocs/op`.
  - If results show performance regression compared to baseline, the agent MUST revise and optimize the implementation until performance matches or improves upon baseline before completing work.
- Dead & unused code elimination:
  - AI agents MUST scan for and remove dead, unreachable, or obsolete code (functions, methods, types, fields, parameters, constants) before finalizing changes.
  - Verify both default and `-tags dev` build configurations using `deadcode` (`go run golang.org/x/tools/cmd/deadcode@latest ./...`) and `golangci-lint run --build-tags dev ./...`.
  - Do not leave unused production symbols behind; code solely used by tests must either move to `*_test.go` or be eliminated.
- All code in one flat package under `internal/ethol` — no sub-packages
- Config via `.env` file (custom parser, not third-party); see `.env.example`
- State persisted as atomic JSON writes to `attended_keys.json`
- Docker: `docker compose up -d` (volume for state persistence at `/app/data/`)
- Documentation maintenance: when modifying code that affects behavior, APIs, CLI flags, Telegram commands, configuration, or architecture described in `docs/`, update the affected documentation files in the same PR/commit series.

## Gotchas

- `.env` holds real credentials — never commit it (gitignored). Use `.env.example` as template.
- Academic logic is split across domain files (`academic_*.go`); test files match 1:1 with source files (`academic_*_test.go`). Run the full test suite when modifying shared cache structures.
- The HTTP client uses a custom `User-Agent` transport and shared cookie jar — auth state is implicit in the client, not passed explicitly.
- `scheduler.go` hardcodes WIB (UTC+7) timezone; time-dependent tests should account for this.
