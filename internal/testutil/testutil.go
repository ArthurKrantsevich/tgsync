// Package testutil holds helpers shared by tests.
package testutil

import (
	"testing"
	"time"
)

// Eventually polls cond for up to 3 seconds.
func Eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
