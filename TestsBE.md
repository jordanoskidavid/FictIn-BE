# Backend Testing Summary

What was built to test the Go + PostgreSQL backend, what the tests cover,
which bugs they found and how those were fixed, and what is still open. For
how to run and write tests, see [CONTRIBUTING.md](CONTRIBUTING.md).

## At a glance

| | |
|---|---|
| Test functions | **109** (49 unit, 60 integration); **517** passing checks including subtests |
| Skipped tests | **0**: every known issue has been fixed |
| Coverage, full suite | **83.9%** of statements, `cmd/api` included |
| Coverage, unit tests alone | 23.5% (most logic needs the database) |
| CI minimum | 75% |
| Stability | Every full run green (25 in a row after the last infrastructure fix), plus a shuffled-order run |
| Bugs found by the tests and fixed | **14** (13 in the application, 1 in the test infrastructure), each with a regression test |
| Open bugs | None known; see [Not covered](#not-covered-limitations-and-verification-status) for limitations |

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
| Live account state | With a fake account store: an active account passes; a deactivated or deleted account → 401; a lookup failure → 500; a temporary password is flagged on the identity |
| Password-change gate | Temporary password → 403 `PASSWORD_CHANGE_REQUIRED`; changed password passes; no identity → 401 |
| Role checks | 403 for every wrong role, including unknown roles; 401 without an identity |
| Client IP | Forwarding headers ignored by default (no spoofing); behind a trusted proxy the rightmost `X-Forwarded-For` entry is used and client-injected entries, `X-Real-IP` and garbage are ignored; IPv6 |
| Response envelope | The `{data}` shape, status helpers, 204 with an empty body, a nil list sent as `[]`, the `{error}` shape with and without `details` |
| Error mapping | All 15 domain errors (including `PASSWORD_CHANGE_REQUIRED`) map to the right HTTP status and error code; wrapped errors keep their code; unexpected errors become a 500 without leaking internal messages |
| Request validation | Empty, malformed, oversized and unknown-field JSON bodies (400); the validation rules of every request type (422), with `details` keyed by the exact JSON field names (`session_id`, `student_id`, …) |
| QR tokens | Valid up to exactly the expiry time and expired 1ms later; a token from another session is invalid; tokens are 32 random URL-safe bytes and never repeat |
| QR rotation manager | First token minted immediately with the clock's time; rotation reaches all subscribed boards; stop closes the boards; start is idempotent; a token that failed to save is never shown; live sessions resume after a restart; logs never contain full tokens |
| CSV export | Header row, one row per record, commas/quotes/newlines escaped (RFC 4180), empty cells, a header-only file for no data, the download headers |
| Rate limiter | Burst size, separate budget per user, refill over time (fake clock), removal of idle users |
| Configuration | Defaults, overrides, missing required settings, invalid numbers and booleans, bcrypt range, development requires the seed admin, `JWT_REFRESH_SECRET` no longer required |
| Schedule times | `HH:MM`, `HH:MM:SS` and single-digit hours parse and normalize to `HH:MM:SS`; invalid times rejected; times compare as times of day (9:30 is before 10:00) |
| Other | Role validity, the `TEST_POSTGRES_URL` safety guard |

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
- **Temporary password is enforced by the server:**
  - for a student, a professor and an admin with a temporary password, every other endpoint returns 403 `PASSWORD_CHANGE_REQUIRED`; this covers subjects, history, scanning, creating subjects, listing users, registering users and the WebSocket board
  - `/auth/me` and change-password stay available
  - the same access token works immediately after the password is changed
- **Deactivation and deletion take effect immediately:**
  - an existing access token → 401 on REST endpoints and the WebSocket handshake, for students, professors and admins
  - logging in again explains why (`ACCOUNT_INACTIVE`)
- **Refresh tokens:**
  - a valid token rotates the pair; reused, expired and unknown tokens → 401; a deactivated user can't refresh
  - **stolen-token detection:** replaying an already-rotated token revokes that whole login chain (the newest token stops working too), while the user's other logins keep working
  - **atomic rotation:** 8 simultaneous refreshes with one token → exactly one succeeds
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
- **Stable pagination:** with identical timestamps forced on every row, paging two at a time through users and through attendance returns every row exactly once, in a fixed order (newest first, then by id).
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
- **Schedules:**
  - created and listed; end before or equal to start, a bad time format or a day out of range → 422
  - times are compared as times of day and stored as `HH:MM:SS`: 9:30–10:00 and 08:05–9:45 are accepted; 10:00–10:00:00, 10:00–9:59 and 23:00–24:00 → 422 (never a 500)
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

### Migrations
- Every migration can be rolled back and re-applied: all down scripts run, then all up scripts, and the schema works again.

### Frontend contract
- **Field names:** the JSON fields of every response type are compared with
  `FrontendDirections.md`: users, subjects, sessions, schedules, enrollments,
  rosters, attendance rows and records, QR token messages and the login
  response. A missing, renamed or leaked field (for example `password_hash`)
  fails the test.
- **Timestamps:** every timestamp must be RFC3339 in UTC (ending in `Z`). This
  is checked on a machine in a non-UTC time zone.
- **Empty lists:** 8 list endpoints, and the nested lists in subject detail,
  return `[]` (never `null`) when empty.

## Bugs found and fixed

For each bug, a test for the correct behavior was written first and seen to
fail; then the code was fixed and the test passed.

### Security

| Bug | Fix | Test |
|---|---|---|
| A user with a temporary password could use the whole API; `must_change_password` was only a hint for the frontend | Every endpoint except `/auth/me` and change-password returns 403 `PASSWORD_CHANGE_REQUIRED` until the password is changed | `TestAuth_MustChangePasswordIsEnforcedServerSide` |
| Deactivated or deleted users kept full access (REST and WebSocket) until their access token expired, up to 30 minutes | The live account state is checked on every authenticated request; such accounts get 401 | `TestAuth_DeactivationRevokesAccessTokens` |
| A stolen refresh token went unnoticed: replaying an old token was rejected, but the tokens issued after it stayed valid | Tokens belong to a login chain ("family", migration `000002`); replaying a rotated token revokes the whole family | `TestAuth_RefreshTokenReuseRevokesFamily` |
| Simultaneous refreshes with one token could all succeed (2 of 8 in testing), forking the session | Rotation is a single atomic database update; exactly one wins | `TestAuth_ConcurrentRefreshRotatesOnce` |
| Client IP addresses could be spoofed through `X-Forwarded-For` / `X-Real-IP` (chi `RealIP`) | New `ClientIP` middleware ignores those headers unless `TRUST_PROXY_HEADERS=true`, and then only trusts the entry the proxy wrote | `TestClientIP` |

### Frontend contract

| Bug | Fix | Test |
|---|---|---|
| `scanned_at` came back as `"2026-09-27 20:43:13.58+02"` (database text format, local time zone) | RFC3339 UTC everywhere, e.g. `2026-09-13T10:15:30Z` | `TestAttendance_ScannedAtIsRFC3339UTCEverywhere` |
| All other timestamps used the server's local time zone (`+02:00`), and `enrolled_at` used the database text format | The database driver returns UTC; `enrolled_at` is formatted like `scanned_at` | contract checks (`assertShape`) in every integration test |
| Empty lists came back as `{"data": null}`, including the nested `schedules` and `roster` | Lists are always arrays: `[]` | `TestEmptyListsAreArrays`, `TestNilSliceRendersAsEmptyArray` |
| Validation errors named ID fields `session_i_d`, `student_i_d`, `schedule_i_d` | `details` uses the exact JSON field names | `TestRequestValidation` |

### Other bugs

| Bug | Fix | Test |
|---|---|---|
| The professor's QR board was disconnected after 30 seconds by the request timeout | The WebSocket stream is excluded from the timeout (now configurable with `HTTP_REQUEST_TIMEOUT_SECONDS`) | `TestWS_BoardOutlivesRequestTimeout` |
| Schedule times were compared as text: 9:30–10:00 was rejected, and 10:00–10:00:00 or 10:00–9:59 caused a 500 | Times are compared as times of day and stored as `HH:MM:SS` | `TestProfessor_ScheduleTimesCompareAsTimes`, `TestParseTimeOfDay` |
| Pagination could repeat or skip rows when timestamps were equal | Every list query orders by its timestamp/name, then by id | `TestAdmin_PaginationIsStableWhenTimestampsTie` |
| Startup failed without `JWT_REFRESH_SECRET`, although nothing used it | No longer required (an existing value is ignored) | `TestParseDoesNotRequireRefreshSecret` |
| Test infrastructure: deleting a test database could fail while Postgres autovacuum was running in it | New connections are blocked first, and the delete is retried briefly | 25 full runs in a row with no failures |

### Documentation fixes in `FrontendDirections.md`

- Rejected WebSocket connections fail the **handshake** with an HTTP error;
  they are not opened and then closed. The doc explains what the browser sees
  (an `error` event, then close code `1006`).
- `ALREADY_MARKED` is not an error code; a repeat scan is a 200 with
  `status: "already_marked"`. The unused error value was also removed from
  the code.
- New sections cover `PASSWORD_CHANGE_REQUIRED`, deactivation, refresh-token
  reuse detection, lists always being arrays, validation field names and the
  schedule time format.

## Changes the frontend needs

1. Handle **403 `PASSWORD_CHANGE_REQUIRED`** by showing the change-password
   screen.
2. Make the token-refresh interceptor **single-flight**. Concurrent 401s must
   wait for one `/auth/refresh` call; refreshing twice with the same token now
   counts as theft and ends that login.
3. A deactivated user now gets **401** on the next request. The existing
   flow (refresh fails, back to login, login shows `ACCOUNT_INACTIVE`)
   handles it.

Everything else is backwards compatible: field types are unchanged, and
timestamps and lists now match what the doc already promised.

## Not covered, limitations and verification status

- **An open WebSocket board keeps streaming after its professor is
  deactivated.** Account state is checked when the connection opens, not
  while it is open. The stream ends when the session stops or the professor
  reconnects.
- **Reactivation revives old access tokens.** An access token issued before
  a deactivation works again if the account is reactivated before the token
  expires (up to 30 minutes). Refresh tokens stay revoked.
- **Every authenticated request now costs one extra database query** to read
  the live account state (a primary-key lookup).
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
