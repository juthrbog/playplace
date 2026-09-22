package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"playplace/internal/core"
)

func TestDamagedOwnedAccountViewsAndOperations(t *testing.T) {
	ts, prov := newServer(t)
	id := "111111111111"
	if err := prov.SetTags(context.Background(), id, map[string]string{core.TagExpires: "broken", core.TagBudget: "broken", core.TagCloseRequested: "broken"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/accounts", "/accounts/" + id} {
		res, body := do(t, ts, http.MethodGet, path, "anna@example.com", false, nil, true)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, res.StatusCode, body)
		}
		for _, want := range []string{"unknown", core.TagExpires, core.TagBudget, core.TagCloseRequested, "operator tag repair required"} {
			if !strings.Contains(body, want) {
				t.Fatalf("missing %q in %s: %s", want, path, body)
			}
		}
		for _, bad := range []string{"0001-01-01", "not yet tagged", "/ $0.00", `hx-post="/accounts/` + id + `/extend"`} {
			if strings.Contains(body, bad) {
				t.Fatalf("misleading %q in %s: %s", bad, path, body)
			}
		}
	}
	_, body := do(t, ts, http.MethodGet, "/accounts/"+id, "ops@example.com", true, nil, false)
	if strings.Contains(body, "Change monthly budget") {
		t.Fatal("budget form offered for damaged guardrail facts")
	}
	for _, op := range []struct {
		path string
		form url.Values
	}{
		{"extend", url.Values{"by": {"7d"}}},
		{"budget", url.Values{"budget": {"75"}, "reason": {"not a repair"}, "override": {"on"}}},
	} {
		_, body := do(t, ts, http.MethodPost, "/accounts/"+id+"/"+op.path, "ops@example.com", true, op.form, true)
		if !strings.Contains(body, "operator tag repair required") {
			t.Fatalf("%s bypassed repair guard: %s", op.path, body)
		}
	}
	if tags := prov.Tags(id); tags[core.TagExpires] != "broken" || tags[core.TagBudget] != "broken" || tags[core.TagCloseRequested] != "broken" {
		t.Fatalf("ordinary mutation repaired tags: %v", tags)
	}
	res, body := do(t, ts, http.MethodPost, "/accounts/"+id+"/close", "anna@example.com", false, nil, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("explicit close blocked: %d %s", res.StatusCode, body)
	}
	r, err := prov.GetAccount(context.Background(), id)
	if err != nil || r.Status != "CLOSED" {
		t.Fatalf("explicit closure: %+v %v", r, err)
	}
}
