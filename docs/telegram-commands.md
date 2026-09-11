# Telegram Bot Commands

This document describes all interactive commands available through the Telegram bot.

## Authorization

The bot only processes commands sent from the configured `TELEGRAM_CHAT_ID`.
The bot ignores messages from unauthorized users.
Commands can include bot mention suffixes (for example, `/status@my_bot`).
The command parser removes these suffixes before execution.

---

## Command Reference

### Service Control Commands

#### `/ping`
- **Description:** Checks if the service is online.
- **Response:** `pong 🏓`

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

#### `/pause`
- **Description:** Pauses automatic background attendance scanning.
- **Response:** Confirms that automatic scanning is suspended.

#### `/resume`
- **Description:** Resumes automatic background scanning.
- **Response:** Confirms that automatic scanning is active.

---

## Information Commands

#### `/whoami`
- **Description:** Displays the identity of the authenticated student.
- **Included Details:** Student name, NRP, and internal student ID number.

#### `/courses`
- **Description:** Lists all enrolled courses for the active academic semester.

#### `/today`
- **Description:** Displays classes scheduled for the current day, including lecture times and room numbers.

#### `/jadwal`
- **Description:** Shows the complete weekly class timetable grouped by day.

#### `/tugas`
- **Description:** Lists open and pending assignments across all enrolled courses with due dates.

#### `/materi`
- **Description:** Lists uploaded lecture materials and recorded video links.

#### `/presensi_kelas`
- **Description:** Shows the student attendee list and attendance count for the currently active class session.

#### `/rekap`
- **Description:** Displays overall attendance percentages and a course-by-course breakdown for the semester.

---

## Action Commands

#### `/check`
- **Description:** Manually triggers an immediate scan of all courses for active presence sessions.
- **Response:** Reports whether any open attendance sessions were found or submitted.

#### `/relogin`
- **Description:** Forces a session re-authentication against the PENS CAS server.
- **Response:** Confirms successful authentication or reports an error message.
