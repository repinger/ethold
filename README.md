# Ethold (Go)

Lightweight, high-performance automated attendance daemon for E-THOL PENS.

## Features

- **Automated CAS SSO Auth**: Automatic login and session token refresh
- **Schedule-Driven Scanning**:
  - `Active Session` (class hours with lead/trail margins): scans every 60s, targeted to active course
  - `Background Sweep` (outside class windows): scans every 15m across all courses for ad-hoc sessions
- **Concurrent Scanning**: Worker pool probes courses in parallel (<1.5s/cycle)
- **Zero Loss Persistence**: Atomic state updates (`attended_keys.json`) prevent duplicate attendance
- **Telegram Bot & Alerts**: Notifications on attendance, interactive commands (`/status`, `/check`, `/ping`, `/help`), and optional forum supergroup topic routing
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
ETHOL_EMAIL=email@student.pens.ac.id
ETHOL_PASSWORD=sso_password

# Optional: Telegram alerts (leave empty to disable)
TELEGRAM_TOKEN=
TELEGRAM_CHAT_ID=

# Optional: Telegram forum supergroup topic routing (0 or unset for general chat)
TELEGRAM_COMMAND_THREAD_ID=
TELEGRAM_NOTIF_THREAD_ID=

# Optional: Auto-presence (set to "true" or "1" to enable; disabled by default)
ETHOL_AUTO_PRESENCE=
```

#### Environment Variables

| Variable                      | Required | Default | Description                                                                                 |
| ----------------------------- | -------- | ------- | ------------------------------------------------------------------------------------------- |
| `ETHOL_EMAIL`                 | **Yes**  | `""`    | PENS CAS student email or NRP username                                                      |
| `ETHOL_PASSWORD`              | **Yes**  | `""`    | PENS CAS SSO account password                                                               |
| `TELEGRAM_TOKEN`              | No       | `""`    | Bot token from [@BotFather](https://t.me/BotFather) (leaves bot disabled if empty)           |
| `TELEGRAM_CHAT_ID`            | No       | `""`    | Authorized user ID, group ID, or forum supergroup ID (`-100...`)                            |
| `TELEGRAM_COMMAND_THREAD_ID`  | No       | `0`     | Forum topic thread ID where bot commands are accepted (`0` = general chat)                 |
| `TELEGRAM_NOTIF_THREAD_ID`    | No       | `0`     | Forum topic thread ID where attendance alerts are sent (`0` = general chat)                 |
| `ETHOL_AUTO_PRESENCE`         | No       | `false` | Enable automated presence scanning and submission (`true`, `1`, `yes`)                      |

> **Note:** Configuration precedence: **CLI flags > System environment variables > `.env` file**.
> Credentials can also be supplied via `-username` and `-password` CLI flags.
> `ETHOL_AUTO_PRESENCE` is configured exclusively via environment variable or `.env` file (no CLI flag).
> Without `ETHOL_AUTO_PRESENCE=true`, the daemon runs in **academic-only mode** (Telegram queries active, no presence scanning or submission).

See [`docs/configuration.md`](docs/configuration.md) for full configuration reference and precedence rules.

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

| Flag                          | Default              | Description                                        |
| ----------------------------- | -------------------- | -------------------------------------------------- |
| `-config`                     | `.env`               | Path to .env config file                           |
| `-username`                   | `""`                 | ETHOL CAS username or NRP override                 |
| `-password`                   | `""`                 | ETHOL CAS password override                        |
| `-telegram-token`             | `""`                 | Telegram bot token override                        |
| `-telegram-chat-id`           | `""`                 | Telegram chat ID override                          |
| `-telegram-command-thread-id` | `0`                  | Telegram forum topic thread ID for commands        |
| `-telegram-notif-thread-id`   | `0`                  | Telegram forum topic thread ID for alerts          |
| `-state`                      | `attended_keys.json` | Path to state persistence file                     |
| `-concurrency`                | `4`                  | Number of concurrent course probe workers          |
| `-once`                       | `false`              | Run single scan pass and exit                      |
| `-verbose`                    | `false`              | Enable debug logging                               |
| `-version`                    | `false`              | Print program version and exit                     |

---

## Telegram Bot Commands

When `TELEGRAM_TOKEN` and `TELEGRAM_CHAT_ID` are configured, the daemon listens for commands from your chat ID. In forum supergroups with `TELEGRAM_COMMAND_THREAD_ID` set, commands are only accepted within that topic thread.

| Command           | Mode Requirement | Description                                                                      |
| ----------------- | ---------------- | -------------------------------------------------------------------------------- |
| `/status`         | All              | Shows daemon status (active/paused), uptime, mode/interval, last scan, and count |
| `/debug`          | All              | Shows system diagnostics, memory, goroutines, and cache statistics               |
| `/check`          | Auto-Presence    | Triggers an immediate presence scan cycle                                        |
| `/courses`        | All              | Lists enrolled courses and lecturers currently monitored                         |
| `/jadwal`         | All              | Displays weekly class schedule with lecturer, time, and room                     |
| `/ujian`          | All              | Displays scheduled midterm (UTS) and final (UAS) online exam timetables          |
| `/tugas`          | All              | Lists pending assignments with sorted deadlines, urgency badges, and links       |
| `/materi`         | All              | Lists uploaded lecture materials (PDFs, docs, links) and videos                  |
| `/pengumuman`     | All              | Displays official campus announcements and important bulletins                   |
| `/presensi_kelas` | Auto-Presence    | Displays live attendance roster and count for active class sessions              |
| `/rekap`          | All              | Official attendance rate and per-course session breakdown                        |
| `/whoami`         | All              | Displays linked student profile (Name, NRP, ID)                                  |
| `/today`          | Auto-Presence    | Lists presence keys successfully recorded today                                  |
| `/relogin`        | All              | Forces re-authentication with CAS SSO and resets session cookies                 |
| `/pause`          | Auto-Presence    | Temporarily pauses automatic scheduled scanning                                  |
| `/resume`         | Auto-Presence    | Resumes automatic scheduled scanning                                             |
| `/ping`           | All              | Connectivity check (replies with `Pong!`)                                        |
| `/help`           | All              | Displays command list                                                            |

_Note: Commands from unauthorized Telegram accounts/chats (or outside the configured command topic) are silently ignored or rejected._

See [`docs/telegram-commands.md`](docs/telegram-commands.md) for full documentation on inline keyboards, throttle limits, and text aliases.

---

## Running with Docker (Recommended)

Pre-built multi-architecture (`linux/amd64`, `linux/arm64`) container images are published on GitHub Container Registry (`ghcr.io/repinger/ethold:latest`) and DockerHub (`repinger/ethold:latest`).

### Running with Docker

Run with an existing `.env` file (recommended to prevent exposing credentials in shell history):

```bash
docker run -d \
  --name ethold \
  --restart unless-stopped \
  --env-file .env \
  -v ethold_state:/app/data \
  ghcr.io/repinger/ethold:latest
```

Alternatively, configure inline for specific setups:

#### 1. No Telegram (Standalone Auto-Presence)

Runs the daemon in the background without bot polling or alerts:

```bash
docker run -d \
  --name ethold \
  --restart unless-stopped \
  -e ETHOL_EMAIL=your_mail \
  -e ETHOL_PASSWORD=your_password \
  -e ETHOL_AUTO_PRESENCE=true \
  -v ethold_state:/app/data \
  ghcr.io/repinger/ethold:latest
```

#### 2. With Telegram (Direct Chat / Group — No Topics)

Receives presence notifications and accepts bot commands in a standard chat or group:

```bash
docker run -d \
  --name ethold \
  --restart unless-stopped \
  -e ETHOL_EMAIL=your_nrp \
  -e ETHOL_PASSWORD=your_password \
  -e ETHOL_AUTO_PRESENCE=true \
  -e TELEGRAM_TOKEN=your_bot_token \
  -e TELEGRAM_CHAT_ID=your_chat_id \
  -v ethold_state:/app/data \
  ghcr.io/repinger/ethold:latest
```

#### 3. With Telegram Forum Supergroup (Topic Threads)

Routes commands to one topic thread and posts attendance alerts to another:

```bash
docker run -d \
  --name ethold \
  --restart unless-stopped \
  -e ETHOL_EMAIL=your_nrp \
  -e ETHOL_PASSWORD=your_password \
  -e ETHOL_AUTO_PRESENCE=true \
  -e TELEGRAM_TOKEN=your_bot_token \
  -e TELEGRAM_CHAT_ID=-1001234567890 \
  -e TELEGRAM_COMMAND_THREAD_ID=2 \
  -e TELEGRAM_NOTIF_THREAD_ID=4 \
  -v ethold_state:/app/data \
  ghcr.io/repinger/ethold:latest
```

#### 4. Academic-Only Mode (No Auto-Presence)

Runs as an interactive info bot (schedule, assignments, roster) without automated presence:

```bash
docker run -d \
  --name ethold \
  --restart unless-stopped \
  -e ETHOL_EMAIL=your_nrp \
  -e ETHOL_PASSWORD=your_password \
  -e TELEGRAM_TOKEN=your_bot_token \
  -e TELEGRAM_CHAT_ID=your_chat_id \
  -v ethold_state:/app/data \
  ghcr.io/repinger/ethold:latest
```

### Docker Compose

Example `docker-compose.yml`:

```yaml
services:
  ethold:
    image: ghcr.io/repinger/ethold:latest # or build: . to compile from source
    container_name: ethold
    restart: unless-stopped
    env_file:
      - path: .env
        required: false
    # Alternatively, set environment variables directly:
    # environment:
    #   - ETHOL_EMAIL=1234567890
    #   - ETHOL_PASSWORD=secret_password_here
    #   - TELEGRAM_TOKEN=123456:ABC-DEF
    #   - TELEGRAM_CHAT_ID=987654321
    #   - TELEGRAM_COMMAND_THREAD_ID=0
    #   - TELEGRAM_NOTIF_THREAD_ID=0
    #   - ETHOL_AUTO_PRESENCE=true
    #   - GOMEMLIMIT=48MiB # recommended for 64MB container limits
    # tty: true # enable ANSI colors in docker logs
    volumes:
      - state:/app/data
    command: ["--state", "/app/data/attended_keys.json"]

volumes:
  state:
```
