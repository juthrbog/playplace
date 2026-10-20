package core

import (
	"testing"
	"time"
)

// The restore of a failed approval has a time limit of its own. A test that
// waits for the restore cannot see a change of the value within its bound.
// So this test pins the value, and a change becomes a deliberate act.
func TestTheRestoreTimeoutIsPinned(t *testing.T) {
	const why = "one write of a tag takes about a second, so only a write that AWS never answers reaches the limit"
	if want := 30 * time.Second; restoreTimeout != want {
		t.Errorf("restoreTimeout = %s, want %s, because %s", restoreTimeout, want, why)
	}
}
