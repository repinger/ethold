# Architecture

This document describes the software design and runtime behavior of the system.

## System Overview

The system is a single binary written in Go.
All domain logic lives in one package: `internal/ethol`.
The program runs as a long-lived service or as a single-scan command.

## Component Diagram

```
+-------------------------------------------------------------------------+
|                              cmd/ethold                                 |
|                               (main.go)                                 |
+------------------------------------+------------------------------------+
                                     |
                                     v
+-------------------------------------------------------------------------+
|                         Scanner (Daemon Loop)                           |
|                                                                         |
|  +------------------+  +------------------+  +-----------------------+  |
|  |  Scan Scheduler  |  |   Worker Pool    |  | Telegram Command Bus  |  |
|  +------------------+  +------------------+  +-----------------------+  |
+----+-------------+--------------+-----------------+----------------+----+
     |             |              |                 |                |
     v             v              v                 v                v
+----------+ +-----------+ +--------------+ +---------------+ +-----------+
|   Auth   | |  Courses  | |   Presence   | |   Academic    | | Telegram  |
| Manager  | |  Manager  | |    Engine    | |    Manager    | | Notifier  |
+----+-----+ +-----+-----+ +------+-------+ +-------+-------+ +-----+-----+
     |             |              |                 |               |
     +-------------+--------------+-----------------+               |
                                  |                                 |
                                  v                                 v
                       +--------------------+             +------------------+
                       |    HTTP Client     |             | Telegram Bot API |
                       | (Cookie Jar + UA)  |             +------------------+
                       +----------+---------+
                                  |
                                  v
                       +--------------------+
                       |     ETHOL PENS     |
                       |    (Remote API)    |
                       +--------------------+
```

## Shared State and Persistence

The system uses a JSON file to store records of submitted attendance.
The `StateManager` component manages this file.
The default path is `attended_keys.json`.

```
+--------------------+      writes to temp file      +--------------------+
|    StateManager    | ----------------------------> |  attended_keys.tmp |
+--------------------+                               +---------+----------+
                                                               |
                                                          atomic rename
                                                               |
                                                               v
                                                     +--------------------+
                                                     | attended_keys.json |
                                                     +--------------------+
```

The file saves two types of data:
- A list of keys for completed attendance checks (`attended_keys`).
- Detailed metadata records (`records`) with time, lecturer, and course name.

The write operation uses a temporary file and an atomic rename.
This prevents data damage if the service stops during a write.

## Data Flow

A normal scan cycle follows these steps:

1. **Check Session:** The `Scanner` calls `AuthManager.EnsureSession()`.
   If the session expired, the manager refreshes or logs in again.
2. **Fetch Courses:** The `Scanner` calls `CourseManager.GetCourses()`.
   The manager returns cached courses or requests the list from ETHOL.
3. **Sort Queue:** The scanner checks the current timetable with `AcademicManager`.
   The scanner puts the active course at the front of the queue.
   The scanner randomizes the order of the other courses.
4. **Inspect Courses:** Workers check each course for an open attendance session.
   Workers add a small pause between checks to avoid rate limits.
5. **Submit Presence:** When a worker finds an open session with a new key:
   - The worker waits for a random delay between 2 and 10 seconds.
   - The worker sends the attendance request to the ETHOL API.
   - The worker saves the key in `StateManager`.
   - The worker sends a notification message through Telegram.

## Concurrency Model

The software uses standard Go concurrency primitives:

- **Worker Pool:** The scanner uses a channel of course items.
  A fixed number of worker goroutines read from this channel.
  The default worker count is 4.
- **Background Tasks:** Two goroutines run in the background when Telegram is active:
  - The Telegram command poller processes incoming user messages.
  - The notification poller checks ETHOL system alerts. When a `PRESENSI` notification arrives and `ETHOL_AUTO_PRESENCE` is enabled, the poller immediately triggers a non-blocking scan attempt via `TryScanOnce` in addition to sending the Telegram alert. Task (`TUGAS`) notifications send an alert only.
- **Locks:**
  - `sync.RWMutex` guards cached data in `CourseManager` and `AcademicManager`.
  - `sync.Mutex` guards login calls and file write operations.
  - `atomic.Bool` stores the pause state of the scanner.
