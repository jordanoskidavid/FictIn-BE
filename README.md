# QR Class Attendance — Backend

Go + PostgreSQL backend for tracking class attendance via a rotating QR code.

- **Admin** creates all accounts (registration is admin-only).
- **Professors** own subjects, schedule classes, run live sessions.
- While a session is **live** the backend mints a fresh QR token every second and
  streams it to the professor's board over WebSocket (with a REST polling fallback).
- **Students** scan the displayed code; the backend marks them present if the
  token is still valid, the session is live, and they are enrolled.

## Stack

| Concern | Choice |
|---|---|
| Language | Go 1.27 |
| Router | `go-chi/chi/v5` |
| DB | PostgreSQL 18 (`jackc/pgx/v5` + `pgxpool`, hand-written SQL) |
| Migrations | `golang-migrate/migrate/v4`, SQL files embedded, applied on startup |
| Auth | JWT access tokens (`golang-jwt/jwt/v5`) + opaque DB-backed refresh tokens, bcrypt |
| WebSocket | `coder/websocket` |
| Validation | `go-playground/validator/v10` |
| Logging | stdlib `log/slog` (JSON) |
| Tests | `testing` + `testify` |

## Layout

```
cmd/api/            entrypoint, graceful shutdown, background cleanup
internal/app/       wiring: config + pool -> services -> router (shared with tests)
internal/clock/     injectable clock (real + fake) for time-dependent logic
internal/config/    env loading + validation
internal/database/  pgx pool, embedded migration runner
internal/models/    domain structs
internal/apperr/    transport-agnostic error type (code + HTTP status)
internal/auth/      bcrypt, JWT, AuthRequired / RequireRole middleware
internal/middleware/ request logging, CORS, security headers, per-user rate limit
internal/repository/ data-access layer (one file per aggregate)
internal/service/   business logic (auth, subject, session, qr rotation, attendance, admin)
internal/handlers/  HTTP handlers (decode -> service -> JSON envelope)
internal/router/    route table + middleware wiring
migrations/         000001_init_schema.{up,down}.sql (+ embed.go)
internal/testutil/  test helpers: DB per test, httptest server, role clients, factories
tests/integration/  HTTP + Postgres integration tests (build tag: integration)
```

## Configuration

Copy `.env.example` to `.env` and adjust. Variables:

| Var | Default | Notes |
|---|---|---|
| `APP_ENV` | `development` | `development` enables debug logs + admin seeding |
| `APP_PORT` | `8080` | |
| `DATABASE_URL` | — | **required**, `postgres://user:pass@host:5432/db?sslmode=disable` |
| `JWT_ACCESS_SECRET` | — | **required**; signs access tokens (refresh tokens are opaque and stored hashed, so no refresh secret is needed) |
| `JWT_ACCESS_TTL_MINUTES` | `30` | |
| `JWT_REFRESH_TTL_HOURS` | `168` | |
| `QR_TOKEN_TTL_MS` | `1500` | token validity window (buffer over the 1s rotation) |
| `QR_ROTATION_INTERVAL_MS` | `1000` | |
| `HTTP_REQUEST_TIMEOUT_SECONDS` | `30` | timeout for regular HTTP requests; the WebSocket QR stream is exempt |
| `BCRYPT_COST` | `12` | |
| `ADMIN_SEED_EMAIL` / `ADMIN_SEED_PASSWORD` | — | required in development; seeds one admin if none exists |
| `CORS_ALLOWED_ORIGINS` | `*` | comma-separated; `*` only for local dev |
| `SCAN_RATE_LIMIT_PER_SEC` / `SCAN_RATE_LIMIT_BURST` | `5` / `5` | per-student limit on `/attendance/scan` |

## Running locally (native Postgres)

```powershell
# 1. create DB + role (once) — needs your postgres superuser password
psql -U postgres -h localhost -c "CREATE ROLE attendance_app LOGIN PASSWORD 'attendance_local_pw';"
psql -U postgres -h localhost -c "CREATE DATABASE attendance_db OWNER attendance_app;"

# 2. configure
copy .env.example .env      # then edit DATABASE_URL + JWT secrets

# (or, instead of step 1, run the database in Docker: docker compose up -d db)

# 3. run (migrations apply automatically on boot)
go run ./cmd/api
```

## Running tests

```sh
go test ./...                              # unit tests: no database or Docker needed
go test -tags=integration ./...            # full suite: needs Docker (starts postgres:18-alpine)
```

Add `-race` when a C compiler is available (always on in CI). `make test`,
`make test-integration`, `make test-all`, `make cover` and `make lint` wrap the
same commands. See **[CONTRIBUTING.md](CONTRIBUTING.md)** for setup on
Linux/macOS/Windows, running integration tests without Docker, coverage,
conventions, and how to add a test for a new endpoint.

## API

Base path `/api/v1`. All responses are wrapped:

```jsonc
// success
{ "data": { ... } }
// error
{ "error": { "code": "SNAKE_CASE", "message": "...", "details": ... } }
```

Auth: send `Authorization: Bearer <access_token>`. For the WebSocket route,
pass `?access_token=<token>` (browsers can't set handshake headers).

### Auth

| Method | Path | Access | Body |
|---|---|---|---|
| POST | `/auth/login` | public | `{email, password}` → `{access_token, refresh_token, token_type, expires_in, user}` |
| POST | `/auth/refresh` | public | `{refresh_token}` → new token pair (old refresh token is rotated + revoked) |
| POST | `/auth/logout` | public | `{refresh_token}` — revokes it |
| GET | `/auth/me` | authenticated | — |
| POST | `/auth/change-password` | authenticated | `{current_password, new_password}` — clears the forced-change flag, revokes all refresh tokens |
| POST | `/auth/register` | **admin** | `{full_name, email, password, role, student_code?}` — `student_code` required when `role=student`. New users get `must_change_password=true`. |

### Admin

| Method | Path | Description |
|---|---|---|
| GET | `/admin/users?role=&active=&limit=&offset=` | list users |
| PATCH | `/admin/users/{id}` | `{full_name?, student_code?, is_active?}` — deactivating revokes the user's refresh tokens |
| DELETE | `/admin/users/{id}` | hard delete |
| GET | `/admin/subjects` | all subjects |
| GET | `/admin/attendance?session_id=&subject_id=&student_id=&format=csv` | attendance records / CSV export |

### Professor — subjects, schedules, enrollments

| Method | Path | Description |
|---|---|---|
| POST | `/subjects` | `{name, code}` — owned by caller |
| GET | `/subjects` | caller's subjects |
| GET | `/subjects/{id}` | detail incl. `schedules` + `roster` |
| PATCH | `/subjects/{id}` | `{name?, code?}` |
| DELETE | `/subjects/{id}` | |
| POST | `/subjects/{id}/schedules` | `{day_of_week (0-6), start_time "HH:MM", end_time "HH:MM", room?}` |
| GET | `/subjects/{id}/schedules` | |
| POST | `/subjects/{id}/enrollments` | `{student_id}` or `{student_code}` |
| GET | `/subjects/{id}/enrollments` | roster |

### Professor — sessions & QR

| Method | Path | Description |
|---|---|---|
| POST | `/sessions/{subject_id}/start` | optional `{schedule_id}` — creates a `live` session, starts rotation. `409 CONFLICT` if one is already live for the subject. |
| POST | `/sessions/{session_id}/stop` | ends the session, stops rotation |
| GET | `/sessions/{session_id}` | professor (owner) or admin — session + `qr_rotating` flag |
| GET | `/sessions/{session_id}/qr/current` | professor (owner) — `{session_id, token, issued_at, expires_at}` (polling fallback) |
| GET | `/sessions/{session_id}/attendance` | professor (owner) — attendance rows |
| WS | `/ws/sessions/{session_id}/qr?access_token=` | professor (owner) — pushes `{session_id, token, issued_at, expires_at}` every second |

### Student

| Method | Path | Description |
|---|---|---|
| GET | `/student/subjects` | enrolled subjects |
| GET | `/student/attendance` | own history |
| POST | `/attendance/scan` | `{session_id, token}` — rate-limited per student |

Scan error codes: `SESSION_NOT_LIVE` (409), `TOKEN_INVALID` (400), `TOKEN_EXPIRED`
(410), `NOT_ENROLLED` (403). A repeat scan returns `200 {"data":{"status":"already_marked"}}`.

## Design notes / deviations from the spec

- **Schema additions:** `users.must_change_password` (forced password change on first
  login, spec §6) and a `refresh_tokens` table (DB-backed revocation, spec §13 default).
- **Query layer:** hand-written SQL over `pgx` instead of `sqlc`, to keep the build to
  `go build` with no codegen step.
- **WebSocket auth** via `?access_token=` query param — unavoidable for browser WS clients. Enforce WSS in production.
- **Server restart:** live sessions are reconciled on boot — rotation goroutines are
  resumed for every session still marked `live`. Graceful shutdown cancels rotation
  goroutines but leaves session status untouched.
- **Rate limiting** is in-process (per-instance token bucket keyed by user id); a
  multi-node deployment would need a shared store.
- `qr_tokens` older than 3h and expired `refresh_tokens` are purged by a background
  ticker every 30 minutes.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01EbZkJny9XFSCPi7YEBPgDC
