# Telegram Bot Commands

This document describes all interactive commands available through the Telegram bot.

## Authorization

The bot only processes commands sent from the configured `TELEGRAM_CHAT_ID`.
The bot ignores messages from unauthorized users.
Commands can include bot mention suffixes (for example, `/status@my_bot`).
The command parser removes these suffixes before execution.
Incoming commands are throttled via an idle-aware token bucket (burst: 3, refill: 1 token/second during idle wait time, with a 5-second warning cooldown). Elapsed command execution time is excluded from token replenishment to prevent burst leakage during slow network requests. Outgoing messages are automatically split across multiple messages if exceeding 4,000 characters.
Interactive command interactions are limited to a 3-command batch window. Upon receiving a 4th command, all previous user commands and bot responses in the cycle are purged asynchronously in the background using Telegram's `deleteMessages` API.
Command replies automatically attach an interactive inline keyboard (`InlineKeyboardMarkup`) containing clean action buttons (e.g. `📅 Jadwal`, `📝 Tugas`, `👥 Presensi Kelas`, `📊 Rekap`, `⚡ Scan Presensi`, `📌 Hari Ini`, `ℹ️ Status`, `❓ Bantuan`) that transmit slash commands via background `callback_query` without posting user message bubbles into the chat.
In addition, persistent custom reply keyboards (`ReplyKeyboardMarkup`) remain supported via `SetReplyKeyboard`.
Additionally, all bot commands support human-friendly text aliases (with or without emoji, plain lowercase words, case-insensitive, e.g. `jadwal`, `tugas`, `ujian`, `matkul`, `whoami`, `relogin`).

---

## Command Reference

### Service Control Commands

#### `/ping`
- **Description:** Checks if the service is online.
- **Response:** `🏓 Pong!`

#### `/help` or `/start`
- **Description:** Returns the complete menu of available commands.

#### `/status`
- **Description:** Returns a detailed report of system status.
- **Included Details:**
  - Service uptime.
  - Active schedule mode and interval.
  - Scan counts and success rates.
  - Last scan time and duration.
  - Paused state.
  - Go runtime memory usage and goroutine count.

#### `/debug`
- **Description:** Returns full runtime diagnostics for troubleshooting.
- **Included Details:**
  - Go version, OS, architecture, PID, CPU count, goroutine count.
  - Memory statistics: heap allocation, GC cycle count.
  - Daemon configuration: worker count, min/max delay and stagger ranges, current scan mode, last scan timestamp.
  - Storage: state file path, recorded key count, course and academic cache entry counts.
  - CAS session: authenticated user name and NRP.

#### `/pause`
- **Description:** Pauses automatic background attendance scanning (requires `ETHOL_AUTO_PRESENCE=true`).
- **Response:** Confirms that automatic scanning is suspended.

#### `/resume`
- **Description:** Resumes automatic background scanning (requires `ETHOL_AUTO_PRESENCE=true`).
- **Response:** Confirms that automatic scanning is active.

---

## Information Commands

#### `/whoami`
- **Description:** Displays the identity of the authenticated student.
- **Included Details:** Student name, NRP, and internal student ID number.

#### `/courses`
- **Description:** Lists all enrolled courses for the active academic semester.

#### `/today`
- **Description:** Displays recorded attendance for the current day (requires `ETHOL_AUTO_PRESENCE=true`).

#### `/jadwal`
- **Description:** Shows the complete weekly class timetable grouped by day.

#### `/ujian`
- **Description:** Displays scheduled midterm (UTS) and final (UAS) online exam timetables with time windows, room, and lecturer.

#### `/tugas`
- **Description:** Lists open and pending assignments across all enrolled courses with sorted deadlines and urgency indicators.

#### `/materi`
- **Description:** Lists uploaded lecture materials and recorded video links.

#### `/pengumuman`
- **Description:** Displays active campus announcements and important administrative bulletins.

#### `/presensi_kelas`
- **Description:** Shows the student attendee list and attendance count for the currently active class session (requires `ETHOL_AUTO_PRESENCE=true`).
- **Note:** Responses are cached for 20 seconds to prevent hammering the ETHOL roster API.

#### `/rekap`
- **Description:** Displays overall attendance percentages and a course-by-course breakdown for the semester.

---

## Action Commands

#### `/check`
- **Description:** Manually triggers an immediate scan of all courses for active presence sessions (requires `ETHOL_AUTO_PRESENCE=true`).
- **Response:** Reports whether any open attendance sessions were found or submitted.
- **Note:** Enforces a 15-second cooldown between scan invocations.

#### `/relogin`
- **Description:** Forces a session re-authentication against the PENS CAS server.
- **Response:** Confirms successful authentication or reports an error message.
- **Note:** Enforces a 30-second cooldown between invocations to prevent rapid re-authentication loops.
