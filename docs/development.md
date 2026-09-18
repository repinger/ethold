# Development Guide

This document describes how to develop, test, and contribute to the project.

## Requirements

You must install these tools on your development computer:
- Go version 1.27 or higher.
- Git.

## Project Structure

```
.
├── cmd/
│   └── ethold/
│       └── main.go           # CLI flags and service initialization
├── internal/
│   └── ethol/                # Single flat package containing all modules
│       ├── academic.go
│       ├── academic_attendance.go
│       ├── academic_materials.go
│       ├── academic_notif.go
│       ├── academic_schedule.go
│       ├── academic_tasks.go
│       ├── auth.go
│       ├── client.go
│       ├── commands.go
│       ├── config.go
│       ├── courses.go
│       ├── debug.go
│       ├── dev_log.go        # Debug tracing (build tag: dev)
│       ├── dev_log_release.go# No-op tracing (build tag: !dev)
│       ├── log.go            # Pretty CLI log handler
│       ├── presence.go
│       ├── scanner.go
│       ├── scheduler.go
│       ├── state.go
│       └── telegram.go
├── docs/                     # Technical documentation
├── go.mod
├── Dockerfile
└── docker-compose.yml
```

## Build Commands

Compile the executable into the current directory:
```bash
go build -o ethold ./cmd/ethold
```

Compile with development tracing enabled (forces debug log level, traces HTTP requests and Telegram commands):
```bash
go build -tags dev -o ethold ./cmd/ethold
```

Run static analysis checks:
```bash
go vet ./...
golangci-lint run ./...
```

Format all Go source files:
```bash
go fmt ./...
```

## Running Tests

The test suite uses only standard library Go testing packages.
Tests do not require external databases or running ETHOL instances.

Run all tests with verbose output:
```bash
go test -v ./...
```

Run a specific test in the `internal/ethol` package:
```bash
go test -v -run TestCourseManager_GetCourses ./internal/ethol
```

Check code test coverage:
```bash
go test -cover ./...
```

## Running Benchmarks

Measure execution duration and heap allocations across critical paths:

Run all benchmarks:
```bash
go test -run='^$' -bench=. -benchmem ./internal/ethol
```

Run a specific benchmark:
```bash
go test -run='^$' -bench=BenchmarkParseEnv -benchmem ./internal/ethol
```

## Coding Conventions

Adhere to these rules when making changes:
- Keep all domain logic in the single `internal/ethol` package.
- Do not create sub-packages under `internal/`.
- Minimize external dependencies. Use the Go standard library first.
- Write tests beside each source file using the `_test.go` naming convention.
- Use `t.TempDir()` to create isolated directories for state tests.
- Format commit messages as `subsystem: imperative summary` (max 75 chars). Examples: `scanner: fix race on pause flag`, `docs: update api-reference`. This is **not** Conventional Commits format — no parenthesized scopes, no `feat:`/`fix:` prefixes.
- Keep commits small and bisectable. Each commit must be a single logical unit that compiles and passes tests independently; split refactors, features, and fixes across separate commits.
