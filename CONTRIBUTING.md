# Contributing

This guide gets a fresh clone to a green test run, then explains where tests
live and how to write new ones.

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | the `go` line in `go.mod` (1.27) | everything |
| Docker | Docker Desktop (Windows/macOS) or Docker Engine (Linux) | integration tests |
| C compiler | gcc/clang; on Windows e.g. `winget install BrechtSanders.WinLibs.POSIX.UCRT` | `-race` (the race detector needs cgo) |
| golangci-lint | v2.14.0: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` | `make lint` |
| make | optional | the Makefile shortcuts (Windows users can run the plain `go` commands below) |

On Windows, after installing a C compiler run `go env -w CGO_ENABLED=1` once so
that `-race` works.

## Setup in 3 commands

```sh
git clone <repo-url> fictin && cd fictin
go mod download
go test ./...
```

The last command runs the unit tests, which need no database, network or
Docker. With Docker running, `go test -tags=integration ./...` runs the full
suite.

## Running tests

| What | Linux / macOS | Windows (PowerShell) |
|---|---|---|
| Unit tests | `make test` | `go test -race ./...` |
| Integration tests only | `make test-integration` | `go test -race -tags=integration -count=1 ./tests/integration/...` |
| Everything | `make test-all` | `go test -race -tags=integration -count=1 ./...` |
| Coverage | `make cover` | see [Coverage](#coverage) |
| Coverage + threshold | `make cover-check` | see [Coverage](#coverage) |
| Lint | `make lint` | `golangci-lint run ./...` |
| Vet | `make vet` | `go vet ./... ; go vet -tags=integration ./...` |

Without a C compiler, drop `-race` (on make: `make test RACE=`). CI always
runs with `-race`.

`-count=1` stops Go from reusing cached results for the integration suite;
the database changes between runs, so a cached result tells you nothing.

### How integration tests get a database

- Each integration test package starts **one** Postgres container
  (`postgres:18-alpine`, the production major version) in `TestMain`, applies
  the migrations to a template database, and removes the container at the end.
- **Every test gets its own fresh database**, cloned from that template
  (`CREATE DATABASE ... TEMPLATE ...`, tens of milliseconds). Tests never see
  each other's data, can run in parallel with `t.Parallel()`, and don't
  depend on the order they run in. Rolling back a transaction isn't an
  option here: the real router uses a connection pool, and the QR rotation
  goroutines write on their own connections.
- If Docker isn't running, the suite stops with a message explaining how to
  start it.

Optional environment variables:

| Variable | Effect |
|---|---|
| `TEST_POSTGRES_IMAGE` | Use a different image, e.g. `postgres:18` |
| `TEST_POSTGRES_URL` | Use an existing server instead of Docker (see below) |
| `TEST_LOG=1` | Print application logs during tests (discarded by default) |
| `RUN_KNOWN_ISSUES=1` | Run tests marked with `testutil.KnownIssue` (see below) |

### Running integration tests without Docker

Set `TEST_POSTGRES_URL` to a Postgres server **dedicated to tests**. The suite
creates and drops databases on it (only ones named `fictin_test_*`, never
anything else). As a guard against pointing it at real data, **the suite
refuses to start unless the user name or the database name in the URL
contains `test`**. The user needs the `CREATEDB` privilege, and the URL must be
in `postgres://` form.

```sql
-- once, as a superuser, on your local Postgres 18
CREATE ROLE attendance_test LOGIN CREATEDB PASSWORD 'attendance_test_pw';
```

```sh
# Linux / macOS
TEST_POSTGRES_URL='postgres://attendance_test:attendance_test_pw@localhost:5432/postgres?sslmode=disable' \
  go test -tags=integration -count=1 ./...
```

```powershell
# Windows
$env:TEST_POSTGRES_URL = "postgres://attendance_test:attendance_test_pw@localhost:5432/postgres?sslmode=disable"
go test -tags=integration -count=1 ./...
```

Never use the application's own role or its `attendance_db` database here.
The guard rejects that combination anyway.

### Coverage

CI enforces **75%** total statement coverage for the full suite (unit and
integration together, every package except the test helpers). The number
lives in `COVERAGE_MIN` in `.github/workflows/ci.yml` and in the `Makefile`.

```sh
make cover-check
```

```powershell
$pkgs = (go list ./... | Where-Object { $_ -notmatch '/internal/testutil|/tests/' }) -join ','
go test -race -tags=integration -count=1 -covermode=atomic "-coverpkg=$pkgs" -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | Select-Object -Last 1
go tool cover -html=coverage.out   # browse uncovered lines
```

## Where tests go

| Kind | Location | Build tag | Needs |
|---|---|---|---|
| Unit | next to the code: `internal/<pkg>/<file>_test.go`, same package | none | nothing |
| Integration | `tests/integration/<area>_test.go` | `//go:build integration` | Docker or `TEST_POSTGRES_URL` |
| Shared helpers | `internal/testutil/` | none | — |

- **Unit tests** cover pure logic: validation, hashing, JWTs, error mapping,
  CSV formatting, QR token rules. Anything that talks to the database, the
  clock or randomness goes through a small interface or the injected
  `clock.Clock`, so a fake can be used instead.
- Unit tests in `internal/service`, `internal/handlers` and the other packages
  that `internal/app` imports **cannot import `internal/testutil`** (that
  would be an import cycle). Use local fakes there, as `qr_service_test.go`
  does.
- **Integration tests** drive the real router, middleware, services and
  Postgres over HTTP, the same way the frontend does.

## Conventions

- Use the standard `testing` package with `testify` (`require` to stop on
  failure, `assert` to keep going).
- When there are several cases, write a table-driven test with `t.Run`
  subtests and descriptive names: `"expired token"`, not `"case 3"`.
- **No `time.Sleep`.** For time-dependent logic, move the fake clock
  (`srv.Clock.Advance(...)` in integration tests, `clock.NewFake(...)` in unit
  tests). To wait for background goroutines, poll with a timeout
  (`require.Eventually`, or a read with a context deadline).
- Every test cleans up after itself: register teardown with `t.Cleanup`.
  `testutil.NewServer` and `testutil.NewDB` already do this.
- Tests must not depend on order or on each other's data. Integration tests
  call `t.Parallel()` and get their own database.
- Everything must pass with `-race`.
- Assert the **frontend contract**, not just the status code: use
  `RequireError(t, status, "CODE")`, and use `assertShape` (in
  `tests/integration/contract_test.go`) for JSON field names.
  `FrontendDirections.md` is the reference.
- **Known bugs:** write the test for the *correct* behavior and start it with
  `testutil.KnownIssue(t, "why")`. It is skipped by default so the suite stays
  green, and runs with `RUN_KNOWN_ISSUES=1` so you can confirm the bug or check
  a fix. Remove the call when you fix the bug.

## Adding a test for a new endpoint

Suppose you add `GET /api/v1/subjects/{id}/sessions` for professors.

1. **Authorization.** Add the route to `protectedRoutes()` in
   `tests/integration/authz_test.go` with its allowed roles. That alone
   checks the 401 for guests and the 403 for every other role.
2. **Behavior.** Add a test to the matching file, here
   `tests/integration/professor_test.go`:

   ```go
   func TestProfessor_ListSessions(t *testing.T) {
       t.Parallel()
       srv := testutil.NewServer(t)          // real app + fresh database
       prof := srv.AsProfessor(t)             // user + access token
       subj := srv.Factory.Subject(t, prof.User.ID)
       srv.Factory.EndedSession(t, subj.ID)
       live := srv.Factory.LiveSession(t, subj.ID)

       var sessions []map[string]any
       prof.Get(t, "/api/v1/subjects/"+subj.ID.String()+"/sessions").
           RequireStatus(t, http.StatusOK).
           Data(t, &sessions)                 // unwraps {"data": ...}

       require.Len(t, sessions, 2)
       assertListShape(t, sessionShape, sessions)
       assert.Equal(t, live.ID.String(), sessions[0]["id"])

       // Ownership: other professors are rejected.
       srv.AsProfessor(t).Get(t, "/api/v1/subjects/"+subj.ID.String()+"/sessions").
           RequireError(t, http.StatusForbidden, "FORBIDDEN")
   }
   ```

3. **Error cases.** Add a table of invalid inputs, each with the expected
   status and `error.code`.
4. **Pure logic.** If the endpoint adds logic that doesn't need the database
   (parsing, formatting, a rule), unit-test that function directly next to
   it.
5. Run `make test-all` (or the PowerShell equivalent) and `make lint`.

Useful helpers (see `internal/testutil`):

- `NewServer(t, WithConfig(func(c *config.Config) {...}))`: the app with
  tweaked configuration (timeouts, rate limits, ...).
- `AsAdmin` / `AsProfessor` / `AsStudent` / `Guest` / `ClientFor(user)` /
  `WithToken(raw)`: clients.
- `Factory.User`, `Subject`, `Enroll`, `LiveSession`, `EndedSession`,
  `QRToken`, `Attendance`, `Classroom`: test data with overridable fields
  (`WithEmail`, `Inactive()`, `WithSubjectCode`, ...).
- `DialWS` / `ReadJSON`: WebSocket clients for the QR board.

## Line endings

`.gitattributes` keeps LF line endings on every OS, which gofmt and
golangci-lint expect. If you cloned before it existed, run
`git add --renormalize .` once.
