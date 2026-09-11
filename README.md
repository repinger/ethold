# ETHOL Auto-Presence (Go)

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
```

Credentials can be set via `.env` file, system environment variables (`export ETHOL_USERNAME=...`), or command line flags (`--username`, `--password`).

### 3. Build & Run

Run continuously as a daemon:

```bash
go build -o ethol-autopresence ./cmd/ethol-autopresence
./ethol-autopresence
```

Run a single scan pass (suitable for cron):

```bash
./ethol-autopresence -once
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

## Running with Docker

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

---

## Running as a Systemd Service

Create `/etc/systemd/system/ethol-autopresence.service`:

```ini
[Unit]
Description=ETHOL Auto-Presence Daemon
After=network.target

[Service]
Type=simple
User=YOUR_USER
WorkingDirectory=/home/YOUR_USER/ethol-autopresence
ExecStart=/home/YOUR_USER/ethol-autopresence/ethol-autopresence -config /home/YOUR_USER/ethol-autopresence/.env
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ethol-autopresence
```
