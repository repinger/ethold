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

CI runs both default and `-tags dev` builds for tests, lint, and `go build`. Always verify both.

## Build tags

The `dev` build tag enables verbose dev-only logging (`dev_log.go` / `dev_log_release.go` pair) and pprof (`cmd/ethold/pprof_dev.go` / `pprof_release.go`, activated via `PPROF_ADDR` env var). The release build stubs these out. CI tests both configurations. When adding build-tagged code, provide both `dev` and `!dev` files.

## Structure

```
cmd/ethold/main.go               # entrypoint, flag parsing, wiring
cmd/ethold/pprof_{dev,release}.go # dev-only pprof server (build-tagged pair)
internal/ethol/                  # all domain code (flat, single package)
  config.go                      # custom .env parser (not third-party)
  client.go                      # http.Client with cookie jar and custom User-Agent transport
  auth.go                        # CAS SSO login, session refresh, relogin
  courses.go                     # course list with TTL cache
  presence.go                    # presence check and submit engine
  scheduler.go                   # WIB (UTC+7) timezone windows and scan planning
  scanner.go                     # main daemon loop, worker pool, scan planning
  commands.go                    # Telegram command routing and response formatting
  academic.go                    # academic manager struct, shared cache lifecycle
  academic_*.go                  # academic domains (schedule, exams, tasks, materials, etc.)
  debug.go                       # /debug Telegram command, runtime diagnostics
  log.go                         # pretty CLI log handler with color support
  dev_log{,_release}.go          # build-tagged dev-only verbose logger pair
  state.go                       # atomic JSON persistence (attended_keys.json)
  telegram.go                    # Telegram Bot API (sendMessage, long-poll getUpdates)
```

Tests are `*_test.go` beside each source file, same `package ethol` (white-box).

## Conventions

- Commit style: `subsystem: imperative summary` (max 50 chars)
  - Body: describe problem and technical solution in detail, wrapped at 72 columns
  - Commits MUST be small and bisectable: each commit is a single logical unit that compiles and passes tests independently; split refactors, features, and fixes across separate commits
  - AI agents MUST NOT add `Signed-off-by` tags (only human contributors certify DCO)
  - AI-assisted commits MUST include: `Assisted-by: <model-name> <tool>` (e.g. `Assisted-by: gemini-3.8-flash-medium Antigravity`)
- Benchmark baseline & regression gating:
  - AI agents MUST run relevant benchmarks before modifying code to establish a baseline (`go test -run='^$' -bench=. -benchmem ./internal/ethol`).
  - After making modifications, re-run benchmarks and compare `ns/op`, `B/op`, and `allocs/op`.
  - If results show performance regression compared to baseline, the agent MUST revise and optimize the implementation until performance matches or improves upon baseline before completing work.
- Memory leak & soak testing gating:
  - AI agents MUST run the soak test before and after modifying code: `go test -v -count=1 -run TestSoakMemory ./internal/ethol`.
  - Commits MUST NOT introduce positive net goroutine growth (`goroutinesEnd > goroutinesStart`) or excessive heap retention (`HeapAlloc delta > 400KB` in soak test).
  - Verify that idle goroutines shut down cleanly and in-memory caches or state maps are pruned or bounded.
- Dead & unused code elimination:
  - AI agents MUST scan for and remove dead, unreachable, or obsolete code (functions, methods, types, fields, parameters, constants) before finalizing changes.
  - Verify both default and `-tags dev` build configurations using `deadcode` (`go run golang.org/x/tools/cmd/deadcode@latest ./...`) and `golangci-lint run --build-tags dev ./...`.
  - Do not leave unused production symbols behind; code solely used by tests must either move to `*_test.go` or be eliminated.
- Config via `.env` file (custom parser, not third-party); see `.env.example`
  - Env vars: `ETHOL_EMAIL`, `ETHOL_PASSWORD`, `TELEGRAM_TOKEN`, `TELEGRAM_CHAT_ID`, `TELEGRAM_COMMAND_THREAD_ID`, `TELEGRAM_NOTIF_THREAD_ID`, `ETHOL_AUTO_PRESENCE`, `ETHOL_STATE_RETENTION_DAYS`
  - CLI flags (`-username`, `-password`, `-telegram-token`, `-telegram-chat-id`, `-telegram-command-thread-id`, `-telegram-notif-thread-id`, `-state-retention-days`) override `.env` values
  - Additional CLI flags: `-config` (`.env` path), `-state` (state file path), `-once` (single scan pass), `-concurrency` (worker count, default 4), `-verbose`, `-version`
- State persisted as atomic JSON writes to `attended_keys.json`
- Docker: `docker compose up -d` (volume for state persistence at `/app/data/`)
- Documentation maintenance: when modifying code that affects behavior, APIs, CLI flags, Telegram commands, configuration, or architecture described in `docs/`, update the affected documentation files in the same PR/commit series.

## Gotchas

- `.env` holds real credentials — never commit it (gitignored). Use `.env.example` as template.
- The HTTP client uses a custom `User-Agent` transport and shared cookie jar — auth state is implicit in the client, not passed explicitly.
- `scheduler.go` hardcodes WIB (UTC+7) timezone; time-dependent tests should account for this.
- `sloglint` enforces key-value only args, static messages, no mixed args — `slog.Info("msg", "key", val)` not `slog.Info(fmt.Sprintf(...))`.
- Every `//nolint` directive requires an explanation and specific linter name.
- CI skips on `**.md` path changes; markdown-only PRs won't trigger test/lint jobs.
- Run the full test suite when modifying shared cache structures in `academic.go` (used across all `academic_*.go` files).
