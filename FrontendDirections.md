# Frontend Directions — QR Class Attendance System

> This document describes the **live Go/PostgreSQL backend** in this repo, exactly as implemented (not just the original spec), so a React frontend can be built against it directly. All routes, request bodies, response bodies, status codes, and error codes below were read from the actual handler/service code.

---

## 1. What this project is

A backend for tracking **student class attendance via rotating QR codes**.

- An **admin** creates all accounts (professors and students) — there is no public sign-up.
- A **professor** creates subjects, enrolls students, schedules weekly class slots, and starts a **live session** for a subject.
- While a session is live, the backend generates a **brand-new random QR token every ~1 second** and streams it to the professor's board (via WebSocket, or a polling REST endpoint as fallback). The professor's screen renders that token as a QR code.
- A **student** scans the currently-displayed QR (extracting `session_id` + `token` from it) and calls the scan endpoint. If the token is still valid and the student is enrolled, they're marked present for that session.
- Each token is valid for about 1.5 seconds, so a photo of an old frame can't be reused later — this is the anti-cheating mechanism (no walking around scanning for absent friends).

Three roles: `admin`, `professor`, `student`. The frontend will need three different views/dashboards, gated by role.

---

## 2. Global conventions

**Base URL:** `http://localhost:8080` (configurable; see `APP_PORT` env var).

**Response envelope** — every JSON response (success or error) is wrapped:

```json
// success
{ "data": { ...payload } }

// error
{ "error": { "code": "SOME_CODE", "message": "human readable", "details": { } } }
```

Always unwrap `.data` on success, and check for `.error` on failure. `details` is optional and may be a string, or (for validation errors) an object of `{ "field_name": "validation_tag" }` where `field_name` is exactly the JSON key you sent (e.g. `"session_id": "uuid"`).

**Auth header:** `Authorization: Bearer <access_token>` on every protected request. Login/register/refresh are exceptions.

**Content type:** `application/json` for all request bodies. The server **rejects unknown JSON fields** (400 Bad Request) — don't send extra keys.

**HTTP status codes used:**
| Status | Meaning |
|---|---|
| 200 | OK |
| 201 | Created |
| 204 | No Content (delete) |
| 400 | Bad request / malformed JSON / bad UUID |
| 401 | Unauthorized (missing/invalid/expired token, deactivated or deleted account, bad login) |
| 403 | Forbidden (wrong role, not the resource owner, inactive account at login, `PASSWORD_CHANGE_REQUIRED`) |
| 404 | Not found |
| 409 | Conflict (duplicate, already-live session, already enrolled, etc.) |
| 410 | Gone (QR token expired) |
| 422 | Validation error |
| 429 | Rate limited |
| 500 | Internal error |

**Error codes** you should branch UI behavior on (`error.code`):
`UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `CONFLICT`, `VALIDATION_ERROR`, `BAD_REQUEST`, `RATE_LIMITED`, `INVALID_CREDENTIALS`, `ACCOUNT_INACTIVE`, `PASSWORD_CHANGE_REQUIRED`, `TOKEN_EXPIRED`, `TOKEN_INVALID`, `SESSION_NOT_LIVE`, `NOT_ENROLLED`, `INTERNAL`.

`ALREADY_MARKED` is **not** an error code: a repeated scan is a successful `200` with `data.status = "already_marked"` (§5.5).

**Lists** are always JSON arrays: an empty result is `{ "data": [] }`, never `null` (also for nested lists such as a subject's `schedules` and `roster`).

**Dates:** all timestamps are ISO-8601 / RFC3339 strings in UTC (e.g. `"2026-09-13T10:15:30Z"`).

**IDs:** all resource IDs are UUID strings (v4-shaped).

---

## 3. Auth model

- JWT **access token** (short-lived, default 30 min) — sent as `Authorization: Bearer`.
- Opaque **refresh token** (default 168h / 7 days) — stored server-side (DB-backed, revocable), sent only to `/auth/refresh` and `/auth/logout`.
- Login response includes both tokens plus the user's `role`. Route the user to the admin/professor/student UI based on `user.role`.
- `must_change_password: true` on the user object means the admin created this account with a temp password. Show a "change your password" screen and call `POST /auth/change-password`. **The server enforces this:** until the password is changed, every endpoint except `GET /auth/me` and `POST /auth/change-password` returns `403 PASSWORD_CHANGE_REQUIRED`. The same access token works right after the change (but refresh tokens are revoked, so log in again before it expires).
- The professor board WebSocket cannot send headers, so it authenticates via `?access_token=<jwt>` query param instead of the header — see §6.2.

---

## 4. Data model (for shaping your TS types)

```ts
type Role = "admin" | "professor" | "student";
type SessionStatus = "pending" | "live" | "ended";

interface User {
  id: string;
  full_name: string;
  email: string;
  role: Role;
  student_code?: string;   // students only
  is_active: boolean;
  must_change_password: boolean;
  created_at: string;
  updated_at: string;
}

interface Subject {
  id: string;
  name: string;
  code: string;            // unique, e.g. "CS101"
  professor_id: string;
  created_at: string;
  updated_at: string;
}

interface ClassSchedule {
  id: string;
  subject_id: string;
  day_of_week: number;     // 0=Sunday .. 6=Saturday
  start_time: string;      // returned as "HH:MM:SS"; accepted on input as "HH:MM" or "HH:MM:SS" (e.g. "9:30")
  end_time: string;        // same format; must be later than start_time (compared as times of day)
  room?: string;
  created_at: string;
}

interface ClassSession {
  id: string;
  subject_id: string;
  schedule_id?: string;
  status: SessionStatus;
  started_at?: string;
  ended_at?: string;
  created_at: string;
}

interface SessionSummary extends ClassSession {
  attendance_count: number; // students marked present in this session
}

interface SessionView extends ClassSession {
  qr_rotating: boolean;    // true if the rotation goroutine is currently running
}

interface Enrollment {
  id: string;
  subject_id: string;
  student_id: string;
  created_at: string;
}

interface EnrolledStudent {   // roster entry (subject's enrollment list)
  enrollment_id: string;
  student_id: string;
  full_name: string;
  email: string;
  student_code?: string;
  enrolled_at: string;
}

interface AttendanceRow {     // used in every attendance-listing endpoint
  id: string;
  session_id: string;
  subject_id: string;
  subject_name: string;
  subject_code: string;
  student_id: string;
  student_name: string;
  student_code?: string;
  scanned_at: string;
}

interface TokenMessage {      // what the QR board receives each tick
  session_id: string;
  token: string;
  issued_at: string;
  expires_at: string;
}
```

---

## 5. REST endpoints

### 5.1 Auth — `/api/v1/auth`

#### `POST /api/v1/auth/login` — public
Request:
```json
{ "email": "prof@example.com", "password": "secret123" }
```
Response `200`:
```json
{
  "data": {
    "access_token": "eyJ...",
    "refresh_token": "base64url...",
    "token_type": "Bearer",
    "expires_in": 1800,
    "user": { "...": "User object" }
  }
}
```
Errors: `INVALID_CREDENTIALS` (401), `ACCOUNT_INACTIVE` (403).

#### `POST /api/v1/auth/refresh` — public
Request: `{ "refresh_token": "..." }`
Response `200`: same shape as login. Tokens are **rotated**: the presented refresh token is invalidated, store the new one.
Errors: `UNAUTHORIZED` (401, invalid/expired/already-used refresh token), `ACCOUNT_INACTIVE` (403).

**Reuse detection:** presenting a refresh token that was already rotated is treated as theft, and every token of that login is revoked (the user must log in again; other devices are unaffected). Consequently the frontend must refresh **single-flight**: if several requests get a 401 at the same time, make one `/auth/refresh` call and let the others wait for its result instead of each refreshing with the same token.

#### `POST /api/v1/auth/logout` — public (just needs the refresh token)
Request: `{ "refresh_token": "..." }`
Response `200`: `{ "data": { "status": "logged_out" } }`

#### `GET /api/v1/auth/me` — any authenticated role
Response `200`: `{ "data": { "...": "User object" } }`

#### `POST /api/v1/auth/change-password` — any authenticated role
Request:
```json
{ "current_password": "old", "new_password": "newpass123" }
```
Response `200`: `{ "data": { "status": "password_changed" } }`
Note: this revokes all of the user's refresh tokens — after this call, force a re-login.
Errors: `INVALID_CREDENTIALS` (current password wrong), `VALIDATION_ERROR` (new == current).

#### `POST /api/v1/auth/register` — **admin only**
Request:
```json
{
  "full_name": "Jane Doe",
  "email": "jane@example.com",
  "password": "TempPass123!",
  "role": "student",              // "admin" | "professor" | "student"
  "student_code": "S12345"        // required only when role = "student"
}
```
Response `201`: `{ "data": { "...": "User object" } }` — created user always has `must_change_password: true`.
Errors: `VALIDATION_ERROR` (422), `CONFLICT` (409, email or student_code taken).

---

### 5.2 Admin — `/api/v1/admin` (admin only)

#### `GET /api/v1/admin/users?role=student&active=true&limit=50&offset=0`
All query params optional (`role`: admin/professor/student, `active`: true/false, `limit` default 50, `offset` default 0).
Response `200`: `{ "data": [ User, ... ] }`

#### `PATCH /api/v1/admin/users/{id}`
Request (all fields optional, send only what changes):
```json
{ "full_name": "New Name", "student_code": "S999", "is_active": false }
```
Response `200`: `{ "data": { "...": "updated User" } }`
Note: setting `is_active: false` takes effect immediately: the user's refresh tokens are revoked **and** their current access token stops working (`401 UNAUTHORIZED` on the next request, REST and WebSocket handshake).
Errors: `NOT_FOUND`, `CONFLICT` (student_code taken).

#### `DELETE /api/v1/admin/users/{id}`
Response `204` (empty body). Errors: `NOT_FOUND`.

#### `GET /api/v1/admin/subjects`
Response `200`: `{ "data": [ Subject, ... ] }` — every subject system-wide.

#### `GET /api/v1/admin/attendance?session_id=&subject_id=&student_id=&limit=500&offset=0&format=csv`
All query params optional. Pass `format=csv` to get a CSV file download instead of JSON (`Content-Disposition: attachment; filename="attendance.csv"`, columns: `record_id, session_id, subject_code, subject_name, student_code, student_name, scanned_at`).
Response `200` (JSON mode): `{ "data": [ AttendanceRow, ... ] }`

---

### 5.3 Professor — Subjects, schedules, enrollments — `/api/v1/subjects` (professor only)

#### `POST /api/v1/subjects`
Request: `{ "name": "Databases 101", "code": "CS101" }` (code is uppercased server-side)
Response `201`: `{ "data": { "...": "Subject" } }`
Errors: `CONFLICT` (code taken).

#### `GET /api/v1/subjects`
Response `200`: `{ "data": [ Subject, ... ] }` — only subjects owned by the calling professor.

#### `GET /api/v1/subjects/{id}`
Response `200`:
```json
{
  "data": {
    "subject": { "...": "Subject" },
    "schedules": [ ClassSchedule, ... ],
    "roster": [ EnrolledStudent, ... ]
  }
}
```
Errors: `FORBIDDEN` (not the owner), `NOT_FOUND`.

#### `PATCH /api/v1/subjects/{id}`
Request (both optional): `{ "name": "New Name", "code": "NEWCODE" }`
Response `200`: `{ "data": { "...": "Subject" } }`

#### `DELETE /api/v1/subjects/{id}`
Response `204`. Cascades: deletes schedules, enrollments, sessions, attendance for that subject.

#### `POST /api/v1/subjects/{id}/schedules`
Request:
```json
{ "day_of_week": 1, "start_time": "10:00", "end_time": "11:30", "room": "B204" }
```
`day_of_week`: 0=Sunday..6=Saturday. `room` optional. `end_time` must be after `start_time`.
Response `201`: `{ "data": { "...": "ClassSchedule" } }`

#### `GET /api/v1/subjects/{id}/schedules`
Response `200`: `{ "data": [ ClassSchedule, ... ] }`

#### `GET /api/v1/subjects/{id}/sessions`
Every session of the subject (live and ended, including earlier days), newest first. Use it for the professor's session history: list here, then open one with `GET /api/v1/sessions/{session_id}/attendance`.
Response `200`: `{ "data": [ SessionSummary, ... ] }` (`[]` if none)
Errors: `FORBIDDEN` (not owner), `NOT_FOUND` (subject not found).

#### `POST /api/v1/subjects/{id}/enrollments`
Request — provide **either** `student_id` or `student_code`:
```json
{ "student_id": "uuid-of-student" }
```
or
```json
{ "student_code": "S12345" }
```
Response `201`: `{ "data": { "...": "Enrollment" } }`
Errors: `NOT_FOUND` (student not found), `VALIDATION_ERROR` (target isn't a student / is inactive), `CONFLICT` (already enrolled).

#### `GET /api/v1/subjects/{id}/enrollments`
Response `200`: `{ "data": [ EnrolledStudent, ... ] }` — the subject's roster.

---

### 5.4 Professor — Sessions & QR — `/api/v1/sessions` (professor only unless noted)

#### `POST /api/v1/sessions/{subject_id}/start`
Note: `{subject_id}` here — starts a session **for a subject**.
Request body is **optional** (can be omitted entirely, or empty `{}`):
```json
{ "schedule_id": "uuid-of-a-schedule-slot" }
```
Response `201`: `{ "data": { "...": "ClassSession", "status": "live", "started_at": "..." } }`
Errors: `FORBIDDEN` (not owner), `CONFLICT` (a session for this subject is already live — only one live session per subject allowed), `VALIDATION_ERROR` (schedule_id doesn't belong to this subject).
On that `CONFLICT`, `details` is `{ "message": "...", "live_session_id": "uuid" }` — use it to offer "Resume" (open the board for that session) or "Stop" (`POST /api/v1/sessions/{live_session_id}/stop`) instead of a dead-end error. This is the only way to rediscover a live session after the board tab is closed.
This kicks off server-side QR rotation immediately (a token is minted right away, then every `QR_ROTATION_INTERVAL_MS`, default 1000ms).

#### `POST /api/v1/sessions/{session_id}/stop`
Note: `{session_id}` here — a session ID, not a subject ID.
Response `200`: `{ "data": { "...": "ClassSession", "status": "ended", "ended_at": "..." } }`
Errors: `CONFLICT` (session is not live).

#### `GET /api/v1/sessions/{session_id}` — professor (owner) or admin
Response `200`: `{ "data": { "...": "SessionView (ClassSession + qr_rotating: boolean)" } }`

#### `GET /api/v1/sessions/{session_id}/qr/current` — polling fallback for the QR board
Response `200`: `{ "data": { "...": "TokenMessage" } }`
Errors: `SESSION_NOT_LIVE` (409), `NOT_FOUND` (no token minted yet — rare, right after start).
Poll this at ~500ms–1s if you don't use the WebSocket (§6.2 is strongly preferred — it's push-based and lower latency).

#### `GET /api/v1/sessions/{session_id}/attendance`
Response `200`: `{ "data": [ AttendanceRow, ... ] }` — everyone marked present for this specific session, newest first.

---

### 5.5 Student — `/api/v1/student` and `/api/v1/attendance` (student only)

#### `GET /api/v1/student/subjects`
Response `200`: `{ "data": [ Subject, ... ] }` — subjects the calling student is enrolled in.

#### `POST /api/v1/attendance/scan`
Rate-limited per student (default 5 req/sec, small burst) — a `429 RATE_LIMITED` is possible if the client fires scans too fast; add a debounce/cooldown on the scan button.
Request:
```json
{ "session_id": "uuid-from-the-qr", "token": "the-token-string-from-the-qr" }
```
Response `200` (marked):
```json
{
  "data": {
    "status": "marked",
    "record": { "...": "AttendanceRecord" },
    "scanned_at": "2026-09-13T10:15:31Z"
  }
}
```
Response `200` (idempotent repeat scan):
```json
{ "data": { "status": "already_marked" } }
```
Errors (show a clear message per code):
- `SESSION_NOT_LIVE` (409) — session hasn't started or already ended.
- `TOKEN_INVALID` (400) — token doesn't exist / doesn't belong to this session (e.g. stale QR image, wrong session).
- `TOKEN_EXPIRED` (410) — token existed but its ~1.5s window passed; tell the user to rescan the (now-updated) QR.
- `NOT_ENROLLED` (403) — student isn't on the roster for this subject.

#### `GET /api/v1/student/attendance`
Response `200`: `{ "data": [ AttendanceRow, ... ] }` — the student's own attendance history, across all subjects, newest first.

---

## 6. Real-time QR delivery (professor board screen)

This is the trickiest part of the frontend — it's a dedicated "board" view the professor projects/displays in class.

### 6.1 Recommended: WebSocket
```
GET ws://localhost:8080/ws/sessions/{session_id}/qr?access_token=<JWT_ACCESS_TOKEN>
```
- Professor role only, and only the session's owning professor, with an active account and no pending temporary password. Rejected connections fail the **handshake** with an HTTP error (401 bad/missing token or deactivated account, 403 wrong role / not the owner / `PASSWORD_CHANGE_REQUIRED`, 404 unknown session, 409 `SESSION_NOT_LIVE`) instead of opening and then closing. Browsers do not expose that status to `WebSocket`: you get an `error` event followed by `close` with code `1006`. Treat it as "cannot open the board" and, if needed, call `GET /api/v1/sessions/{session_id}` to find out why.
- **Auth via query param, not header** — browsers can't set custom headers during the WS handshake, so pass the JWT as `?access_token=...` on the URL itself.
- On connect, the server immediately sends the current token, then a new one every rotation interval (~1s), as JSON text frames:
  ```json
  { "session_id": "...", "token": "...", "issued_at": "...", "expires_at": "..." }
  ```
- The server pings every 20s to detect dead connections; the `ws` browser API auto-responds to pings, no action needed client-side.
- The socket closes normally when the professor calls `stop` on the session (server sends close code `1000`) — treat any close as "session ended, redirect off the board view."
- On each message: regenerate the QR code image client-side (e.g. with a library like `qrcode.react`) encoding **both** `session_id` and `token` (e.g. as a small JSON string or a `session_id:token` pair) — since the scan endpoint needs both.

### 6.2 Fallback: polling
If WebSocket isn't feasible, poll `GET /api/v1/sessions/{session_id}/qr/current` on an interval close to 1s. It's the same `TokenMessage` shape. Less precise (added HTTP round-trip latency eats into the ~1.5s token lifetime), so prefer the WebSocket.

### 6.3 Student scan flow
The student side doesn't need a WebSocket — it's a single fire-and-forget `POST /api/v1/attendance/scan` per scanned QR frame, using whatever `session_id`/`token` pair the scanned QR encoded.

---

## 7. Suggested frontend structure

- **Auth**: login page, forced change-password page (when `must_change_password`), token refresh interceptor (on 401, call `/auth/refresh` once, **single-flight**: concurrent 401s must wait for the same refresh call, see §5.1; redirect to login on failure), redirect to the change-password page on `403 PASSWORD_CHANGE_REQUIRED`, logout.
- **Admin dashboard**: user list/filter/create (via register)/deactivate/delete, system-wide subject list, attendance export (JSON table + a "Download CSV" button hitting `?format=csv`).
- **Professor dashboard**:
  - Subjects: list/create/edit/delete, detail page (roster + schedules), add schedule, enroll student (by ID or code).
  - Sessions: start/stop a live session per subject (guard the "Start" button when a live session already exists), session detail.
  - **Board view**: full-screen QR display for a live session, driven by the WebSocket in §6.1.
  - Attendance: per-session attendance table.
- **Student dashboard**: enrolled subjects list, a scan page (camera QR reader → POST scan, show success/already-marked/error banner), personal attendance history table.

---

## 8. Things to double check against a running instance

- Health checks: `GET /healthz` and `GET /readyz` (no auth) — useful for a "is the API up" indicator in dev.
- CORS is configured via the `CORS_ALLOWED_ORIGINS` env var on the backend — make sure your React dev server's origin (e.g. `http://localhost:5173`) is added there, or the backend is left at its `*` default.
- `QR_TOKEN_TTL_MS` (default 1500) and `QR_ROTATION_INTERVAL_MS` (default 1000) are configurable — if a deployment changes them, adjust your scan-retry/QR-refresh UX expectations accordingly.
