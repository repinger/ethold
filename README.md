# Ethold (Go)

Lightweight, high-performance automated attendance daemon for E-THOL PENS.

## Features

- **Automated CAS SSO Auth**: Automatic login and session token refresh
- **Schedule-Driven Scanning**:
  - `Active Session` (class hours with lead/trail margins): scans every 60s, targeted to active course
  - `Background Sweep` (outside class windows): scans every 15m across all courses for ad-hoc sessions
- **Concurrent Scanning**: Worker pool probes courses in parallel (<1.5s/cycle)
- **Zero Loss Persistence**: Atomic state updates (`attended_keys.json`) prevent duplicate attendance
- **Telegram Bot & Alerts**: Notifications on attendance and interactive commands (`/status`, `/check`, `/ping`, `/help`)
- **Zero Heavy Dependencies**: Pure Go stdlib + streaming HTML tokenizer

---

## Quick Start

### 1. Prerequisites

- Go 1.27+

### 2. Configure

Copy `.env.example` to `.env`:

```bash
cp .env.example .env
```

Edit `.env`:

```bash
ETHOL_USERNAME=email@student.pens.ac.id
ETHOL_PASSWORD=sso_password

# Optional: Telegram alerts (leave empty to disable)
TELEGRAM_TOKEN=
TELEGRAM_CHAT_ID=

# Optional: Auto-presence (set to "true" or "1" to enable; disabled by default)
ETHOL_AUTO_PRESENCE=
```

Credentials can be set via `.env` file, system environment variables (`export ETHOL_USERNAME=...`), or command line flags (`--username`, `--password`).

Auto-presence is disabled by default. Set `ETHOL_AUTO_PRESENCE=true` to enable automatic attendance scanning and submission. Without this, the daemon functions purely as an academic info bot (schedule, assignments, materials, attendance stats).

### 3. Build & Run

Run continuously as a daemon:

```bash
go build -o ethold ./cmd/ethold
./ethold
```

Run a single scan pass (suitable for cron):

```bash
./ethold -once
```

Run test suite:

```bash
go test -v ./...
```

### CLI Flags

| Flag           | Default              | Description                               |
| -------------- | -------------------- | ----------------------------------------- |
| `-config`      | `.env`               | Path to .env config file                  |
| `-state`       | `attended_keys.json` | Path to state persistence file            |
| `-concurrency` | `4`                  | Number of concurrent course probe workers |
| `-once`        | `false`              | Run single scan pass and exit             |
| `-verbose`     | `false`              | Enable debug logging                      |

---

## Telegram Bot Commands

When `TELEGRAM_TOKEN` and `TELEGRAM_CHAT_ID` are configured, the daemon listens for commands from your chat ID:

| Command           | Description                                                                      |
| ----------------- | -------------------------------------------------------------------------------- |
| `/status`         | Shows daemon status (active/paused), uptime, mode/interval, last scan, and count |
| `/check`          | Triggers an immediate presence scan cycle                                        |
| `/courses`        | Lists enrolled courses and lecturers currently monitored                         |
| `/jadwal`         | Displays weekly class schedule with lecturer, time, and room                     |
| `/tugas`          | Lists pending assignments with deadlines and submission links                    |
| `/materi`         | Lists uploaded lecture materials (PDFs, docs, links) and videos                  |
| `/presensi_kelas` | Displays live attendance roster and count for active class sessions              |
| `/rekap`          | Official attendance rate and per-course session breakdown                        |
| `/whoami`         | Displays linked student profile (Name, NRP, ID)                                  |
| `/today`          | Lists presence keys successfully recorded today                                  |
| `/relogin`        | Forces re-authentication with CAS SSO and resets session cookies                 |
| `/pause`          | Temporarily pauses automatic scheduled scanning                                  |
| `/resume`         | Resumes automatic scheduled scanning                                             |
| `/ping`           | Connectivity check (replies with `Pong!`)                                        |
| `/help`           | Displays command list                                                            |

_Note: Commands from unauthorized Telegram accounts/chats are silently ignored._

---

## Running with Docker (Recommended)

Pre-built multi-architecture (`linux/amd64`, `linux/arm64`) container images are available from GitHub Container Registry (GHCR) and DockerHub.

### Container Images

Pull from GitHub Container Registry (GHCR):

```bash
docker pull ghcr.io/repinger/ethold:latest
# or dev build from main branch
docker pull ghcr.io/repinger/ethold:dev
```

Pull from DockerHub:

```bash
docker pull repinger/ethold:latest
# or dev build from main branch
docker pull repinger/ethold:dev
```

Run directly with Docker:

```bash
docker run -d \
  --name ethold \
  --restart unless-stopped \
  --env-file .env \
  -v ethold_state:/app/data \
  ghcr.io/repinger/ethold:latest
```

### Docker Compose

Start with Docker Compose (includes persistence volume):

```bash
docker compose up -d
```

View logs:

```bash
docker compose logs -f
```

Stop:

```bash
docker compose down
```
