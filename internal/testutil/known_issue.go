package testutil

import (
	"os"
	"testing"
)

// KnownIssue marks a test that asserts the CORRECT behavior for a bug that is
// not fixed yet. It skips with the reason, unless RUN_KNOWN_ISSUES=1, which
// runs these tests to confirm they still fail (or pass once fixed).
func KnownIssue(t testing.TB, reason string) {
	t.Helper()
	if os.Getenv("RUN_KNOWN_ISSUES") != "1" {
		t.Skip("known issue: " + reason)
	}
}
