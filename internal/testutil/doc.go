// Package testutil holds shared helpers for integration tests:
//
//   - RunWithDatabase / NewDB: one Postgres container per test package, and a
//     fresh migrated database per test (cloned from a template).
//   - NewServer: the real router + middleware + services served by httptest.
//   - Server.AsAdmin / AsProfessor / AsStudent / Guest: authenticated clients.
//   - Factory: builders for users, subjects, sessions, QR tokens, attendance.
//   - Response.Data / Error / RequireError: envelope-aware assertions.
//
// See CONTRIBUTING.md for conventions and examples.
package testutil
