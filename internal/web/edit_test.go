package web

import (
	"context"
	"log/slog"
	"math"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"playplace/internal/audit"
	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

// editFormValues reads the inline edit form for name the way a browser
// submits it untouched: every named input with its prefilled value, and a
// checkbox only when it is ticked. A number input whose value its own min or
// step refuses fails the test, because the browser would not submit the form.
func editFormValues(t *testing.T, body, name string) url.Values {
	t.Helper()
	row := regexp.MustCompile(`(?s)<tr id="req-` + regexp.QuoteMeta(name) + `">.*?</tr>`).FindString(body)
	if !strings.Contains(row, `name="ttl"`) {
		t.Fatalf("no edit form for %s in:\n%s", name, body)
	}
	attr := func(tag, key string) (string, bool) {
		m := regexp.MustCompile(`\s` + key + `(?:="([^"]*)")?[\s>/]`).FindStringSubmatch(tag)
		if m == nil {
			return "", false
		}
		return m[1], true
	}
	out := url.Values{}
	for _, tag := range regexp.MustCompile(`<input[^>]*>`).FindAllString(row, -1) {
		key, ok := attr(tag, "name")
		if !ok {
			continue
		}
		value, _ := attr(tag, "value")
		switch typ, _ := attr(tag, "type"); typ {
		case "checkbox":
			if _, checked := attr(tag, "checked"); checked {
				out.Set(key, "on")
			}
			continue
		case "number":
			numberAccepts(t, tag, key, value, attr)
		}
		out.Set(key, value)
	}
	return out
}

// numberAccepts fails the test when a browser would refuse value in a number
// input: below min, or not min plus a whole number of steps.
func numberAccepts(t *testing.T, tag, key, value string, attr func(tag, key string) (string, bool)) {
	t.Helper()
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		t.Fatalf("%s holds %q, which a number input drops", key, value)
	}
	base := 0.0
	if s, ok := attr(tag, "min"); ok {
		if base, err = strconv.ParseFloat(s, 64); err != nil || v < base {
			t.Fatalf("%s holds %s, below its min %q: %s", key, value, s, tag)
		}
	}
	step := "1"
	if s, ok := attr(tag, "step"); ok {
		step = s
	}
	if step == "any" {
		return
	}
	n, err := strconv.ParseFloat(step, 64)
	if err != nil {
		t.Fatalf("%s has a bad step: %s", key, tag)
	}
	if k := (v - base) / n; math.Abs(k-math.Round(k)) > 1e-9 {
		t.Fatalf("%s holds %s, which its step refuses: %s", key, value, tag)
	}
}

// editServer is newServer without seeded accounts, with the service and
// the history at hand.
func editServer(t *testing.T) (*httptest.Server, *core.Service, *audit.Memory) {
	t.Helper()
	prov := fake.New()
	prov.Now = func() time.Time { return t0 }
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(prov, nil, cfg, nil)
	svc.Now = func() time.Time { return t0 }
	hist := &audit.Memory{}
	svc.SetAuditor(hist)
	ts := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), headerAuth{}, nil, hist).Handler())
	t.Cleanup(ts.Close)
	return ts, svc, hist
}

// An admin who saves the edit form without touching the lifetime or the
// budget keeps them exactly, even when they are not whole days or dollars.
func TestEditFormKeepsUntouchedLifetimeAndBudget(t *testing.T) {
	ts, svc, hist := editServer(t)
	ctx := context.Background()
	read := func(name string) core.Request {
		t.Helper()
		q, err := svc.GetRequest(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	edits := func(name string) []string {
		t.Helper()
		events, err := hist.Query(ctx, audit.Filter{Account: name, Event: "edited"})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range events {
			out = append(out, e.Details["changes"])
		}
		return out
	}
	for _, tc := range []struct {
		name, ttl, budget string
		lifetime          time.Duration
		usd               float64
		days              string
		noted             bool
	}{
		{"odd", "36h", "12.50", 36 * time.Hour, 12.5, "1.5", true},
		{"short", "12h", "20", 12 * time.Hour, 20, "0.5", true},
		// 13 hours is not a finite decimal number of days. The form holds
		// the shortest decimal that reads back as exactly 13 hours.
		{"thirteen", "13h", "0.99", 13 * time.Hour, 0.99, "0.5416666666666666", true},
		{"whole", "3", "40", 3 * 24 * time.Hour, 40, "3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each request has its own owner, so the per-owner limit does not
			// refuse it.
			if res, body := do(t, ts, "POST", "/requests", tc.name+"@example.com", false, url.Values{"name": {tc.name}, "ttl": {tc.ttl}, "budget": {tc.budget}, "purpose": {"old"}}, true); res.Header.Get("X-Form-Error") != "" {
				t.Fatalf("request = %s", body)
			}
			before := read(tc.name)
			if before.TTL != tc.lifetime || before.BudgetUSD != tc.usd {
				t.Fatalf("queued = %+v", before)
			}

			_, page := do(t, ts, "GET", "/requests/"+tc.name+"/edit", "ops@example.com", true, nil, true)
			form := editFormValues(t, page, tc.name)
			if form.Get("ttl") != tc.days || form.Get("budget") != strconv.FormatFloat(tc.usd, 'f', -1, 64) {
				t.Fatalf("the form should hold the exact values: %v", form)
			}
			// A lifetime that is not whole days shows next to the field.
			if strings.Contains(page, "days (now "+core.Lifetime(tc.lifetime)+")") != tc.noted {
				t.Fatalf("the hours next to the days should show only for a part day:\n%s", page)
			}

			// Saved untouched, nothing changes and nobody is told.
			if res, body := do(t, ts, "POST", "/requests/"+tc.name+"/edit", "ops@example.com", true, form, true); res.Header.Get("X-Form-Error") != "" {
				t.Fatalf("an untouched save should work: %s", body)
			}
			if got := read(tc.name); got != before || len(edits(tc.name)) != 0 {
				t.Fatalf("an untouched save changed the request: %+v, was %+v, edits %q", got, before, edits(tc.name))
			}

			// A purpose-only change keeps the lifetime and the budget.
			_, page = do(t, ts, "GET", "/requests/"+tc.name+"/edit", "ops@example.com", true, nil, true)
			form = editFormValues(t, page, tc.name)
			form.Set("purpose", "new")
			if res, body := do(t, ts, "POST", "/requests/"+tc.name+"/edit", "ops@example.com", true, form, true); res.Header.Get("X-Form-Error") != "" {
				t.Fatalf("a purpose edit should work: %s", body)
			}
			want := before
			want.Purpose = "new"
			if got, changes := read(tc.name), edits(tc.name); got != want || len(changes) != 1 || changes[0] != "purpose updated" {
				t.Fatalf("a purpose edit changed other values: %+v, edits %q", got, changes)
			}
		})
	}
}

// The edit leaves out every field that still holds its prefill, so only
// what the admin changed reaches the service.
func TestRequestEditFromFormSendsOnlyChangedFields(t *testing.T) {
	from := core.Request{Name: "odd", Owner: "bob@example.com", TTL: 36 * time.Hour, BudgetUSD: 12.5, Purpose: "old", OverrideLimits: true}
	untouched := url.Values{"owner": {"bob@example.com"}, "ttl": {"1.5"}, "budget": {"12.5"}, "purpose": {"old"}, "override": {"on"}}
	edit := func(key, value string) core.RequestEdit {
		t.Helper()
		v := url.Values{}
		for k, vs := range untouched {
			v[k] = slices.Clone(vs)
		}
		switch {
		case key == "":
		case value == "":
			v.Del(key)
		default:
			v.Set(key, value)
		}
		r := httptest.NewRequest("POST", "/requests/odd/edit", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		e, err := RequestEditFromForm(r, from)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	if e := edit("", ""); e != (core.RequestEdit{}) {
		t.Fatalf("an untouched form should send nothing: %+v", e)
	}
	if e := edit("purpose", "new"); e.Purpose == nil || *e.Purpose != "new" || e.Owner != "" || e.TTL != 0 || e.BudgetUSD != 0 || e.OverrideLimits != nil {
		t.Fatalf("a purpose edit should send only the purpose: %+v", e)
	}
	if e := edit("override", ""); e.OverrideLimits == nil || *e.OverrideLimits || e.Purpose != nil {
		t.Fatalf("unticking the override should send only the override: %+v", e)
	}
	if e := edit("ttl", "2"); e.TTL != 48*time.Hour || e.BudgetUSD != 0 {
		t.Fatalf("a days edit should send only the lifetime: %+v", e)
	}
	if e := edit("budget", "12.75"); e.BudgetUSD != 12.75 || e.TTL != 0 {
		t.Fatalf("a budget edit should send only the budget: %+v", e)
	}
}

// A field the admin changes is still saved, even to a value that is not a
// whole number of days or dollars.
func TestEditFormSavesChangedLifetimeAndBudget(t *testing.T) {
	ts, svc, _ := editServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"odd"}, "ttl": {"36h"}, "budget": {"12.50"}}, true)
	_, page := do(t, ts, "GET", "/requests/odd/edit", "ops@example.com", true, nil, true)
	form := editFormValues(t, page, "odd")
	form.Set("ttl", "0.5")
	form.Set("budget", "7.25")
	res, body := do(t, ts, "POST", "/requests/odd/edit", "ops@example.com", true, form, true)
	if res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, "<td>12 hours</td>") || !strings.Contains(body, "<td>$7.25</td>") {
		t.Fatalf("the edit should show the new values: %s", body)
	}
	q, err := svc.GetRequest(context.Background(), "odd")
	if err != nil || q.TTL != 12*time.Hour || q.BudgetUSD != 7.25 {
		t.Fatalf("stored = %+v %v", q, err)
	}
}
