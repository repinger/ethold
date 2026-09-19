# ETHOL API Reference

This document lists the ETHOL PENS REST API endpoints that the application calls.
All endpoints use the base URL `https://ethol.pens.ac.id`.

---

## Authentication Endpoints

### 1. CAS Redirect
- **Path:** `/api/auth/cas-redirect`
- **Method:** `GET`
- **Description:** Returns an HTTP redirect to the CAS SSO login service.

### 2. Validate Token
- **Path:** `/api/auth/validasi-token`
- **Method:** `GET`
- **Description:** Validates the active session and returns user profile data.
- **Response Format:**
  ```json
  {
    "nomor": 12345,
    "nama": "STUDENT NAME",
    "nipnrp": "3120500001"
  }
  ```

### 3. Refresh Session
- **Path:** `/api/auth/refresh`
- **Method:** `POST`
- **Description:** Extends the lifetime of current session cookies.
- **Success Code:** `200 OK`

### 4. Auth Config
- **Path:** `/api/auth/config`
- **Method:** `GET`
- **Description:** Returns system configuration including the active academic term.
- **Response Format:**
  ```json
  {
    "tahun_aktif": 2026,
    "semester_aktif": 2
  }
  ```

---

## Course Endpoints

### 5. List Courses
- **Path:** `/api/kuliah?tahun={year}&semester={sem}`
- **Method:** `GET`
- **Query Parameters:**
  - `tahun`: Academic year (integer).
  - `semester`: Academic semester (integer: 1 or 2).
- **Description:** Returns the enrolled courses for the authenticated student.

---

## Presence Endpoints

### 6. Active Presence Check
- **Path:** `/api/presensi/aktif-kuliah?kuliah={kuliahId}&jenis_schema={schema}`
- **Method:** `GET`
- **Query Parameters:**
  - `kuliah`: Course ID.
  - `jenis_schema`: Schema type code.
- **Description:** Checks if an attendance window is currently open.
- **Response Example:**
  ```json
  [
    {
      "key": "ABC123XYZ"
    }
  ]
  ```

### 7. Submit Presence
- **Path:** `/api/presensi/mahasiswa`
- **Method:** `POST`
- **Content-Type:** `application/json`
- **Request Body:**
  ```json
  {
    "kuliah": 101,
    "jenis_schema": 1,
    "mahasiswa": 12345,
    "key": "ABC123XYZ",
    "kuliah_asal": 101
  }
  ```
- **Description:** Submits student attendance for an open session.

### 8. Session Attendees Roster
- **Path:** `/api/presensi/daftar-mahasiswa-hadir-kuliah?key={presenceKey}`
- **Method:** `GET`
- **Description:** Returns the list of students who have checked in for the specified key.

### 9. Session Absentees Roster
- **Path:** `/api/presensi/daftar-mahasiswa-tidak-hadir-kuliah?key={presenceKey}&kuliah={kuliahId}&jenis_schema={schema}`
- **Method:** `GET`
- **Description:** Returns the list of enrolled students who have not yet checked in for the specified key.

### 10. Course Enrolled Count
- **Path:** `/api/presensi/jumlah-mahasiswa-per-kuliah?kuliah={kuliahId}&jenis_schema={schema}`
- **Method:** `GET`
- **Description:** Returns the total number of students enrolled in the class.

### 11. Student Presence History
- **Path:** `/api/presensi/riwayat?kuliah={kuliahId}&jenis_schema={schema}&nomor={studentId}`
- **Method:** `GET`
- **Description:** Returns the historical check-in records for a specific student in a course.

### 12. Lecturer Presence History
- **Path:** `/api/presensi/get-tanggal-presensi-dosen-per-semester?tahun={year}&semester={sem}&kuliah={kuliahId}&dosen={dosenId}`
- **Method:** `GET`
- **Description:** Returns all dates on which the lecturer conducted class attendance.

### 13. Student Home Attendance Stats
- **Path:** `/api/presensi/stat-beranda-mahasiswa?tahun={year}&semester={sem}`
- **Method:** `GET`
- **Description:** Returns official aggregated semester attendance summary and session counts.

---

## Academic Information Endpoints

### 14. Class Schedule
- **Path:** `/api/jadwal/jadwal-online?tahun={year}&semester={sem}`
- **Method:** `GET`
- **Description:** Returns weekly class schedule items with day and time boundaries.

### 15. Exam Schedule
- **Path:** `/api/ujian/daftar-ujian?tahun={year}&semester={sem}&jenis={jenis}`
- **Method:** `GET`
- **Query Parameters:**
  - `tahun`: Academic year (integer).
  - `semester`: Academic semester (integer: 1 or 2).
  - `jenis`: Exam type (1 = UTS / Midterms, 2 = UAS / Finals).
- **Description:** Returns online exam schedule entries including date, duration, room, and lecturer.

### 16. Course Tasks
- **Path:** `/api/tugas?kuliah={kuliahId}&jenisSchema={schema}`
- **Method:** `GET`
- **Description:** Returns course assignments and student submission status.

### 17. Course Materials
- **Path:** `/api/materi?matakuliah={kuliahId}&jenis_schema={schema}`
- **Method:** `GET`
- **Description:** Returns downloadable documents and presentation slides.

### 18. Course Videos
- **Path:** `/api/video?kuliah={kuliahId}&jenis_schema={schema}`
- **Method:** `GET`
- **Description:** Returns links to recorded video lectures.

### 19. Campus Announcements
- **Path:** `/api/pengumuman-admin`
- **Method:** `GET`
- **Description:** Returns campus-wide official announcements and important bulletin notifications.

---

## Notification Endpoints

### 20. Unread Notifications Count
- **Path:** `/api/notifikasi/mahasiswa-belum-baca`
- **Method:** `GET`
- **Description:** Returns the count of unread notifications for the user.

### 21. List Notifications
- **Path:** `/api/notifikasi/mahasiswa?filterNotif=SEMUA`
- **Method:** `GET`
- **Description:** Returns all notifications for the student account.

### 22. Mark Notifications Read
- **Path:** `/api/notifikasi/mahasiswa-baca-notif`
- **Method:** `PUT`
- **Description:** Marks pending notifications as read on the server.
