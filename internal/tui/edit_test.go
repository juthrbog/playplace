package tui

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"playplace/internal/audit"
	"playplace/internal/core"
)

// An operator who saves the edit form without touching the lifetime or the
// budget keeps them exactly, even when they are not whole days or dollars.
// An owner or a purpose longer than most form fields take stays whole too.
func TestEditFormKeepsUntouchedLifetimeAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		lifetime       time.Duration
		usd            float64
		days           string
		noted          bool
		owner, purpose string
	}{
		{"odd", 36 * time.Hour, 12.5, "36h", true, "dana@example.com", "old"},
		{"short", 12 * time.Hour, 20, "12h", true, "dana@example.com", "old"},
		{"thirteen", 13 * time.Hour, 0.99, "13h", true, "dana@example.com", "old"},
		{"whole", 3 * 24 * time.Hour, 40, "3", false, "dana@example.com", "old"},
		{"long purpose", 36 * time.Hour, 12.5, "36h", true, "dana@example.com", strings.Repeat("p", core.MaxPurpose)},
		{"long owner", 36 * time.Hour, 12.5, "36h", true, strings.Repeat("d", 100) + "@example.com", "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			svc, _ := seeded(t)
			hist := &audit.Memory{}
			svc.SetAuditor(hist)
			edits := func() []string {
				t.Helper()
				events, err := hist.Query(ctx, audit.Filter{Account: "pend", Event: "edited"})
				if err != nil {
					t.Fatal(err)
				}
				var out []string
				for _, e := range events {
					out = append(out, e.Details["changes"])
				}
				return out
			}
			before, err := svc.SubmitRequest(ctx, core.RequestInput{Name: "pend", Owner: tc.owner, RequestedBy: "dev@example.com", TTL: tc.lifetime, BudgetUSD: tc.usd, Purpose: tc.purpose}, "web")
			if err != nil {
				t.Fatal(err)
			}
			m := newModel(ctx, svc, Options{Operator: "ops@example.com"})
			m.now = func() time.Time { return t0 }
			m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
			open := func(key rune) {
				t.Helper()
				m = run(t, m, m.load(true))
				for i, r := range m.visible {
					if r.Account.Name == "pend" {
						m.grid.cursor = i
					}
				}
				m = step(t, m, tea.KeyPressMsg{Code: key})
			}
			edit := func() {
				t.Helper()
				open('e')
				if m.form == nil {
					t.Fatalf("e should open the edit form:\n%s", plain(m.content()))
				}
				m.form.openedAt = t0.Add(-time.Second)
			}
			save := func() {
				t.Helper()
				next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				m = run(t, next.(model), cmd)
				if m.form != nil {
					t.Fatalf("the form should save: %s", m.form.errMsg)
				}
			}

			edit()
			if m.form.fields[1].input.Value() != tc.days || m.form.fields[2].input.Value() != strconv.FormatFloat(tc.usd, 'f', -1, 64) {
				t.Fatalf("the form should hold the exact values: %q %q", m.form.fields[1].input.Value(), m.form.fields[2].input.Value())
			}
			if m.form.fields[0].input.Value() != tc.owner || m.form.fields[3].input.Value() != tc.purpose {
				t.Fatalf("the form should hold the whole owner and purpose: %q %q", m.form.fields[0].input.Value(), m.form.fields[3].input.Value())
			}
			// A lifetime that is not whole days shows next to the field.
			if form := plain(m.formView()); strings.Contains(form, "(now "+core.Lifetime(tc.lifetime)+")") != tc.noted {
				t.Fatalf("the note next to the days should show only for a part day:\n%s", form)
			}

			// Saved untouched, nothing changes and nobody is told.
			save()
			if got, err := svc.GetRequest(ctx, "pend"); err != nil || got != before || len(edits()) != 0 {
				t.Fatalf("an untouched save changed the request: %+v %v, was %+v, edits %q", got, err, before, edits())
			}
			flash := "✓ pend: " + tc.owner + ", " + core.Lifetime(tc.lifetime) + ", " + core.Dollars(tc.usd) + "/month"
			if last := m.notices[len(m.notices)-1]; last.text != flash {
				t.Fatalf("the flash should be %q, got %q", flash, last.text)
			}

			// A purpose-only change keeps the lifetime and the budget.
			edit()
			m.form.fields[3].input.SetValue("new")
			save()
			want := before
			want.Purpose = "new"
			if got, err := svc.GetRequest(ctx, "pend"); err != nil || got != want || len(edits()) != 1 || edits()[0] != "purpose updated" {
				t.Fatalf("a purpose edit changed other values: %+v %v, edits %q", got, err, edits())
			}

			// The approve dialog still shows the exact values.
			open('a')
			if body := ": " + core.Dollars(tc.usd) + "/month for " + core.Lifetime(tc.lifetime) + "?"; m.dialog == nil || !strings.Contains(m.dialog.body, body) {
				t.Fatalf("the approve dialog should show %q: %+v", body, m.dialog)
			}
		})
	}
}

// A field the operator changes is still saved, even to a lifetime that is
// not a whole number of days.
func TestEditFormSavesChangedLifetime(t *testing.T) {
	ctx := context.Background()
	m, svc, _ := pendingFixture(t)
	m = step(t, m, tea.KeyPressMsg{Code: 'e'})
	if m.form == nil {
		t.Fatal("e should open the edit form")
	}
	m.form.openedAt = t0.Add(-time.Second)
	m.form.fields[1].input.SetValue("36h")
	m.form.fields[2].input.SetValue("7.25")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = run(t, next.(model), cmd)
	if q, err := svc.GetRequest(ctx, "pend"); err != nil || q.TTL != 36*time.Hour || q.BudgetUSD != 7.25 {
		t.Fatalf("the edit should save the new values: %+v %v", q, err)
	}
	if last := m.notices[len(m.notices)-1]; last.text != "✓ pend: dana@example.com, 36 hours, $7.25/month" {
		t.Fatalf("the flash should show the exact values, got %q", last.text)
	}
}

// The edit leaves out every field that still holds its prefill, so only
// what the operator changed reaches the service.
func TestRequestEditSendsOnlyChangedFields(t *testing.T) {
	prefill := []string{"dana@example.com", "36h", "12.5", "old", "y"}
	edit := func(i int, value string) core.RequestEdit {
		t.Helper()
		v := slices.Clone(prefill)
		if i >= 0 {
			v[i] = value
		}
		e, err := requestEdit(prefill, v)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	if e := edit(-1, ""); e != (core.RequestEdit{}) {
		t.Fatalf("an untouched form should send nothing: %+v", e)
	}
	if e := edit(3, "new"); e.Purpose == nil || *e.Purpose != "new" || e.Owner != "" || e.TTL != 0 || e.BudgetUSD != 0 || e.OverrideLimits != nil {
		t.Fatalf("a purpose edit should send only the purpose: %+v", e)
	}
	if e := edit(4, "yes"); e != (core.RequestEdit{}) {
		t.Fatalf("yes is the same override as y: %+v", e)
	}
	if e := edit(4, ""); e.OverrideLimits == nil || *e.OverrideLimits || e.Purpose != nil {
		t.Fatalf("clearing the override should send only the override: %+v", e)
	}
	if e := edit(1, "2"); e.TTL != 48*time.Hour || e.BudgetUSD != 0 {
		t.Fatalf("a days edit should send only the lifetime: %+v", e)
	}
	if e := edit(1, ""); e != (core.RequestEdit{}) {
		t.Fatalf("an empty days field should keep the lifetime: %+v", e)
	}
}
