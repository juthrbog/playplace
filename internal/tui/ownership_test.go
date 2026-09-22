package tui

import (
	"strings"
	"testing"

	"playplace/internal/core"
)

func TestDamagedOwnedAccountPresentation(t *testing.T) {
	m := fixture(t, 120, 35)
	a := core.FromRemote(core.RemoteAccount{ProviderID: "123456789012", Name: "damaged-owned", Status: "ACTIVE", Tags: map[string]string{
		core.TagManaged: "true", core.TagExpires: "broken", core.TagBudget: "broken", core.TagCloseRequested: "broken",
	}}, core.DefaultConfig(), t0)
	m.rows = []row{{Account: a}}
	m.applyFilter()
	if m.inSection(m.rows[0], secExpiring) {
		t.Fatal("unknown expiry was treated as due")
	}
	for _, detail := range []bool{false, true} {
		m.showDetail = detail
		out := plain(m.content())
		if !strings.Contains(out, "unknown") || !strings.Contains(out, "active !") {
			t.Fatalf("missing unknown value/repair indicator: %s", out)
		}
		for _, bad := range []string{"0001-01-01", "106751d ago", "untagged", "$0 / $0"} {
			if strings.Contains(out, bad) {
				t.Fatalf("misleading %q: %s", bad, out)
			}
		}
		if detail && (!strings.Contains(out, core.TagExpires) || !strings.Contains(out, "operator tag repair required")) {
			t.Fatalf("missing actionable details: %s", out)
		}
	}
	next, cmd := m.openExtend()
	m = run(t, next.(model), cmd)
	if m.form != nil {
		t.Fatal("extension offered for unknown baseline")
	}
	next, _ = m.openClose()
	if next.(model).dialog == nil {
		t.Fatal("explicit closure unavailable")
	}
}
