# Backend Testing Summary

What was built to test the Go + PostgreSQL backend, what the tests cover,
which bugs they found, and what is still open. For how to run and write tests,
see [CONTRIBUTING.md](CONTRIBUTING.md).

## At a glance

| | |
|---|---|
| Test functions | **102** (45 unit, 57 integration); **458** passing checks including subtests |
| Known-issue tests (skipped) | 9 (they check the correct behavior for bugs that aren't fixed yet) |
| Coverage, full suite | **84.1%** of statements, `cmd/api` included |
| Coverage, unit tests alone | 22.3% (most logic needs the database) |
| CI minimum | 75% |
| Stability | 25 full runs in a row with no failures, plus a shuffled-order run |
| Bugs fixed with a regression test | 3 |
| Bugs documented as known issues | 7 (6 known-issue tests plus a linter finding) |

## How the tests are organized

| Kind | Where | Needs | Command |
|---|---|---|---|
| Unit | next to the code (`internal/<pkg>/*_test.go`) | nothing | `go test ./...` |
| Integration | `tests/integration/` (build tag `integration`) | Docker, or `TEST_POSTGRES_URL` | `go test -tags=integration ./...` |

**Integration tests go through the real app.** They run the production
router, middleware, services and repositories (built by `internal/app`, the
same code `main` uses) against a real PostgreSQL 18 database. They send HTTP
requests the way the frontend does.

**Isolation.** Each test package starts one `postgres:18-alpine` container
and applies the migrations once, to a template database. Every test then
gets its own fresh copy of that database. Tests run in parallel, share no
data, and don't depend on each other's order.

**Time without sleeps.** An injectable clock (`internal/clock`) lets tests
move time forward: QR token expiry, JWT expiry and rate limits are tested this
way. Background work, such as QR rotation, is awaited by polling with a
timeout.

**Helpers** (`internal/testutil`):

- the per-test database
- a test server built from the real app
- ready-made clients for guest, admin, professor and student
- data builders (users, subjects, enrollments, sessions, QR tokens,
  attendance)
- assertions that understand the `{data}` / `{error}` response envelope
- WebSocket clients

**Safety.** Without Docker, the suite can use an existing Postgres server
through `TEST_POSTGRES_URL`. It refuses any URL whose user or database name
doesn't contain `test`, and it only ever creates and drops databases named
`fictin_test_*`.

**Tooling.**

- A GitHub Actions workflow runs on every push and pull request to
  `main`/`master`: module checks, `go vet`, golangci-lint, unit tests with
  `-race`, integration tests with `-race` and coverage, the 75% coverage
  gate, and a build.
- A Makefile provides `test`, `test-integration`, `test-all`, `cover`,
  `cover-check` and `lint`.
- `docker-compose.yml` runs the development database.
- `.gitattributes` keeps LF line endings on all platforms.

## Unit tests (no database)

| Area | What was tested |
|---|---|
| Password hashing | Correct and wrong passwords, empty and case-changed input, salted hashes, invalid hashes, the bcrypt cost fallback |
| JWT access tokens | Round trip for all 3 roles; expiry at the exact second (fake clock); rejection of tokens with another secret, `alg: none`, HS512, a missing user id, a tampered payload or garbage |
| Refresh tokens | Random, only the hash is stored, expiry taken from the clock |
| Auth middleware | 401 for a missing, malformed, foreign or expired token; `Bearer` and `?access_token=` (WebSocket) both accepted; the user identity passed to handlers |
| Role checks | 403 for every wrong role, including unknown roles; 401 without an identity |
| Response envelope | The `{data}` shape, status helpers, 204 with an empty body, the `{error}` shape with and without `details` |
| Error mapping | All 15 domain errors map to the right HTTP status and error code; wrapped errors keep their code; unexpected errors become a 500 without leaking internal messages |
| Request validation | Empty, malformed, oversized and unknown-field JSON bodies (400); the validation rules of every request type (422, with per-field details) |
| QR tokens | Valid up to exactly the expiry time and expired 1ms later; a token from another session is invalid; tokens are 32 random URL-safe bytes and never repeat |
| QR rotation manager | First token minted immediately with the clock's time; rotation reaches all subscribed boards; stop closes the boards; start is idempotent; a token that failed to save is never shown; live sessions resume after a restart; logs never contain full tokens |
| CSV export | Header row, one row per record, commas/quotes/newlines escaped (RFC 4180), empty cells, a header-only file for no data, the download headers |
| Rate limiter | Burst size, separate budget per user, refill over time (fake clock), removal of idle users |
| Configuration | Defaults, overrides, missing required settings, invalid numbers, bcrypt range, development requires the seed admin |
| Other | Role validity, schedule time parsing, the `TEST_POSTGRES_URL` safety guard |

## Integration tests (real HTTP + PostgreSQL)

### Authentication
- **Login:**
  - success returns the documented token response; email matching ignores case
  - wrong password and unknown email → 401 `INVALID_CREDENTIALS`
  - deactivated account → 403 `ACCOUNT_INACTIVE`, but only when the password is correct
  - validation → 422; unknown fields and empty bodies → 400
- **Temporary password flow:**
  - `must_change_password` is `true` at first login
  - change-password rejects a wrong current password (401), the same password again (422) and a password that is too short (422)
  - success returns **200** `{"status":"password_changed"}`
  - after the change, the flag is cleared, all refresh tokens are revoked, the old password fails and the new one works
- **Refresh tokens:**
  - a valid token rotates the pair
  - reused, expired and unknown tokens → 401
  - a deactivated user can't refresh
- **Logout** returns `{"status":"logged_out"}` and revokes the refresh token; logging out again is harmless.
- **Other:** `/auth/me` returns the documented user fields and never the password hash; an expired access token → 401.

### Authorization
- Every protected route (26 of them, WebSocket included) returns 401 to guests and to invalid tokens.
- Every protected route returns 403 to every role that isn't allowed on it.
- Login, refresh and logout need no token.
- A professor gets 403 on another professor's subjects, schedules, enrollments, sessions, QR tokens and attendance (12 endpoints).

### Admin
- **Create users:**
  - emails are lowercased and student codes trimmed; new users must change their password
  - `student_code` is ignored for non-students
  - duplicate email (also with different letter case) or duplicate student code → 409
  - student without a code, invalid role or weak password → 422
- **List users:** filters by role and by active/inactive, pagination, and an invalid filter → 422.
- **Update users:** rename; deactivate, which blocks login and refresh; reactivate, which restores them. A taken student code → 409, a blank name → 422, an unknown user → 404, a bad id → 400.
- **Delete users:** → 204, a second delete → 404, and the deleted user can't log in.
- The admin sees every professor's subjects and can open any session.

### Professor
- **Subjects:**
  - create uppercases the code, trims the name and makes the caller the owner
  - duplicate code (any letter case, any professor) → 409
  - the list shows only the caller's own subjects
- **Subject detail** includes the schedules and the roster, in the documented shapes.
- **Update and delete:**
  - update changes the name and code; a code taken by another subject → 409; keeping its own code is fine; an empty update changes nothing
  - delete → 204 and also removes that subject's sessions and attendance
- **Schedules:** created and listed; end before or equal to start, a bad time format or a day out of range → 422.
- **Enrollments:**
  - by student id, or by student code (surrounding spaces trimmed)
  - already enrolled → 409, unknown student → 404, a non-student or inactive student → 422
  - the enrolled student then sees the subject
- **Sessions:**
  - start → 201 `live`, with QR rotation running
  - a second live session for the same subject → 409
  - the polling endpoint returns the current token
  - stop → 200 `ended`; stopping again → 409; polling afterwards → 409 `SESSION_NOT_LIVE`
  - a new session can start after the previous one ends
  - starting with a schedule from another subject → 422

### Student and scanning
- **Valid scan:** a valid token → 200 `marked` with the attendance record. Scanning again → 200 `already_marked`. The scan appears in the student's history.
- **Rejected scans:** expired token → 410 `TOKEN_EXPIRED`; unknown token or a token from another session → 400 `TOKEN_INVALID`; ended session → 409 `SESSION_NOT_LIVE`; unknown session → 404; a student who isn't enrolled → 403 `NOT_ENROLLED`. None of these record attendance.
- **Expiry boundary** (fake clock): a token still works exactly at its expiry and fails 1ms later.
- **End to end:** the professor starts a real session, the student scans the rotating token, the professor sees the attendance, and scanning after stop fails.
- **Rate limiting:** a burst over the limit → 429 `RATE_LIMITED`; the limit is per student; the budget refills after a second.
- **10 simultaneous scans** by the same student: all return 200, exactly one says `marked`, and the database holds exactly one record.

### WebSocket QR board
- **Rotation:** the board receives a new token every rotation, each in the documented shape and valid for exactly 1.5s. A streamed token is accepted by the scan endpoint.
- **Auth:** works through `?access_token=` (browsers) and through the `Authorization` header.
- **Rejected connections:** guests or invalid tokens (401); students, admins and other professors (403); ended (409), unknown (404) or malformed (400) sessions. Each rejection carries the error envelope.
- **Stop:** stopping the session closes the board with code **1000**.
- **Long streams:** the board keeps streaming well past the HTTP request timeout.

### Attendance export
- **Admin CSV:**
  - exact columns, and values with commas and quotes escaped
  - `text/csv` content type and the `attendance.csv` download filename
  - filters by session, subject, student and combinations, plus `limit`/`offset`, in both CSV and JSON
  - no matching data → a header-only CSV
  - an invalid filter id → 400
- **Professors** see only their own session's attendance.

### CORS and security headers
- **REST:**
  - allowed origins are echoed back with `Vary: Origin` and the allowed methods and headers
  - unknown origins get no allow header
  - preflight requests → 204
  - the development default `*` works
- **WebSocket:** the handshake rejects a disallowed origin (403) and accepts an allowed one.
- **Security headers** (`nosniff`, `DENY`, `no-referrer`) are on every response, including 404s.

### Resilience and background jobs
- **Database down:** `/healthz` stays 200, `/readyz` → 503 `DB_UNAVAILABLE`, and API calls → 500 `INTERNAL` without leaking internal details.
- **Startup:**
  - the development admin is seeded once (lowercased email, working password, no forced password change)
  - nothing is seeded outside development
  - QR rotation resumes for sessions still live after a restart, and not for ended ones
- **Cleanup job:**
  - removes QR tokens older than 3h and expired refresh tokens
  - keeps fresh tokens and valid refresh tokens
  - exits when the server shuts down

### Frontend contract
The JSON field names of every response type are compared with
`FrontendDirections.md`: users, subjects, sessions, schedules, enrollments,
rosters, attendance rows and records, QR token messages and the login
response. A missing, renamed or leaked field (for example `password_hash`)
fails the test, and every timestamp must be RFC3339.

## Bugs found and fixed (each with a regression test)

| Bug | Fix | Test |
|---|---|---|
| `scanned_at` came back as `"2026-09-27 20:43:13.58+02"` (database text format, local time zone) in attendance lists and the CSV, and with a local offset in scan responses | Always RFC3339 UTC, e.g. `2026-09-13T10:15:30Z`. No frontend change needed. | `TestAttendance_ScannedAtIsRFC3339UTCEverywhere` |
| The professor's QR board was disconnected after 30 seconds by the request timeout | The WebSocket stream is excluded from the timeout, which is now configurable (`HTTP_REQUEST_TIMEOUT_SECONDS`) | `TestWS_BoardOutlivesRequestTimeout` |
| Test infrastructure: cleaning up a test database could fail when Postgres's autovacuum was running in it (non-superuser test role only) | New connections are blocked first, and the drop is retried briefly | 25 full runs in a row without failures |

## Known issues (tests written, skipped until fixed)

Each test checks the **correct** behavior. They are skipped by default so the
suite stays green, and run with `RUN_KNOWN_ISSUES=1`. All of them were
confirmed to fail today.

| # | Issue | Severity | Test |
|---|---|---|---|
| 1 | `must_change_password` isn't enforced by the API; a user with a temporary password can use every endpoint | Security | `TestAuth_MustChangePasswordIsEnforcedServerSide` |
| 2 | Deactivating a user doesn't cut off their current access token (it keeps working up to 30 minutes) | Security | `TestAuth_DeactivationRevokesAccessTokens` |
| 3 | Reusing an old refresh token doesn't revoke the tokens issued after it (no stolen-token detection) | Security | `TestAuth_RefreshTokenReuseRevokesFamily` |
| 4 | Empty lists return `{"data": null}` instead of `[]` | Frontend contract | `TestProfessor_EmptyListsAreNullNotArrays` |
| 5 | Validation errors name ID fields wrongly (`session_i_d` instead of `session_id`) | Frontend contract | `TestRequestValidation` (3 cases), `TestToSnake/SessionID` |
| 6 | Schedule times are compared as text: a 9:30–10:00 slot is rejected, and 10:00–10:00:00 causes a 500 | Bug | `TestProfessor_ScheduleTimesCompareAsTimes` |

Also reported, without a test:

- **Security (linter finding):** the `RealIP` middleware trusts client-supplied
  IP headers, which allows IP spoofing.
- **Frontend contract:** other timestamps (`created_at`, `updated_at`, …) use
  the server's local time zone instead of UTC, and `enrolled_at` uses the
  database text format.
- **Frontend contract:** `FrontendDirections.md` says rejected WebSocket
  connections are "closed", but they actually fail the handshake with an HTTP
  error.
- **Frontend contract:** `ALREADY_MARKED` is listed as an error, but it is a
  200 response.
- **Minor:**
  - `JWT_REFRESH_SECRET` is required but unused.
  - User pagination has no tiebreaker, so pages can be inconsistent when
    timestamps tie.

## Not covered, and verification status

- **`cmd/api` (`main`) isn't tested:** reading the environment, signal
  handling and the HTTP server's graceful shutdown. It is kept deliberately
  thin; everything it starts (wiring, startup tasks, the cleanup job and its
  shutdown) lives in `internal/app` and is tested there.
- **`-race` hasn't run yet.** The race detector needs a C compiler, which the
  development machine doesn't have yet; CI runs every test with `-race`.
- **The Docker path hasn't run yet.** Local runs used `TEST_POSTGRES_URL`
  against PostgreSQL 18; only the "Docker isn't running" message was checked.
  The Docker path runs in CI.
- **The CI workflow hasn't run yet**, because the repository has no remote.
- **PostgreSQL 18 is an assumption.** It was chosen to match the local
  database. If production uses another version, set `TEST_POSTGRES_IMAGE`
  and update `docker-compose.yml`.
