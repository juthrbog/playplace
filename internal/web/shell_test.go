package web

import (
	"log/slog"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"playplace/internal/core"
	"playplace/internal/provider/fake"
)

func TestShellByRole(t *testing.T) {
	ts, _ := newServer(t)
	home := `<a class="nav-link" href="/" aria-current="page">Home</a>`
	for _, c := range []struct {
		who             string
		user            string
		admin, approver bool
		want, not       []string
	}{
		{"engineer", "anna@example.com", false, false,
			[]string{home, `<span class="person-email">anna@example.com</span>`},
			[]string{`href="/#pending"`, `href="/#accounts"`, "Sync with AWS", `class="badge"`}},
		{"approver", "lead@example.com", false, true,
			[]string{home, `href="/#pending"`},
			[]string{`href="/#accounts"`, "Sync with AWS", `class="badge"`}},
		{"admin", "ops@example.com", true, true,
			[]string{`href="/#pending"`, `href="/#accounts"`, "Sync with AWS", `class="badge">admin<`},
			[]string{">Home</a>"}},
	} {
		t.Run(c.who, func(t *testing.T) {
			_, body := doAs(t, ts, "GET", "/", c.user, c.admin, c.approver, nil, false)
			for _, w := range c.want {
				if !strings.Contains(body, w) {
					t.Errorf("shell should contain %s", w)
				}
			}
			// History and Notices come with their pages in a later phase.
			for _, n := range append(c.not, `href="/history"`, `href="/notices"`) {
				if strings.Contains(body, n) {
					t.Errorf("shell should not contain %s", n)
				}
			}
			// The menu toggle names a container that is on the page.
			m := regexp.MustCompile(`class="menu-toggle"[^>]*aria-controls="([^"]+)"`).FindStringSubmatch(body)
			if m == nil || !strings.Contains(body, `id="`+m[1]+`"`) {
				t.Errorf("the menu toggle should control an element on the page, got %v", m)
			}
			if t.Failed() {
				t.Logf("body:\n%s", body)
			}
		})
	}
	// Only Home is a page of its own, so no link is current elsewhere.
	_, body := do(t, ts, "GET", "/accounts/111111111111", "anna@example.com", false, nil, false)
	if strings.Contains(body, "aria-current") {
		t.Fatalf("no shell link is the account page:\n%s", body)
	}
}

func TestQueueCountTracksPending(t *testing.T) {
	ts, _ := newServer(t)
	// An engineer has no Queue link, so an answer must not carry a count
	// that htmx cannot place.
	res, body := do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-req"}, "budget": {"20"}}, true)
	if res.Header.Get("X-Form-Error") != "" || strings.Contains(body, `id="queue-count"`) {
		t.Fatalf("an engineer's answer should have no queue count: %v\n%s", res.Header, body)
	}
	do(t, ts, "POST", "/requests", "carl@example.com", false, url.Values{"name": {"carls-req"}}, true)

	_, page := doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	if !strings.Contains(page, `id="queue-count"`) || !strings.Contains(page, `>2<span class="vh"> pending requests`) {
		t.Fatalf("the approver should see 2 pending in the shell:\n%s", page)
	}

	// An approval updates the count with the card.
	res, body = doAs(t, ts, "POST", "/requests/carls-req/approve", "lead@example.com", false, true, versionIn(t, page, "carls-req"), true)
	if res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`) || !strings.Contains(body, `>1<span class="vh"> pending requests`) {
		t.Fatalf("approve should send the new count out of band: %v\n%s", res.Header, body)
	}

	// A refusal for a changed request renders the card again, and the count
	// with it.
	seen := versionIn(t, page, "bobs-req")
	_, form := do(t, ts, "GET", "/requests/bobs-req/edit", "ops@example.com", true, nil, true)
	edit := versionIn(t, form, "bobs-req")
	edit.Set("owner", "bob@example.com")
	edit.Set("budget", "40")
	if res, body := do(t, ts, "POST", "/requests/bobs-req/edit", "ops@example.com", true, edit, true); res.Header.Get("X-Form-Error") != "" {
		t.Fatalf("edit = %s", body)
	}
	res, body = doAs(t, ts, "POST", "/requests/bobs-req/approve", "lead@example.com", false, true, seen, true)
	if res.Header.Get("X-Form-Error") != "1" || !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`) {
		t.Fatalf("a stale approve should send the count with the fresh card: %v\n%s", res.Header, body)
	}

	// Sync renders the card too.
	_, body = do(t, ts, "POST", "/refresh", "ops@example.com", true, url.Values{}, true)
	if !strings.Contains(body, `<span id="queue-count" hx-swap-oob="true">`) {
		t.Fatalf("sync should send the count out of band:\n%s", body)
	}

	// At zero the count is empty, so the shell shows no pill.
	_, page = doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	res, _ = doAs(t, ts, "POST", "/requests/bobs-req/approve", "lead@example.com", false, true, versionIn(t, page, "bobs-req"), true)
	if res.Header.Get("X-Form-Error") != "" {
		t.Fatal("approving the current request should work")
	}
	_, page = doAs(t, ts, "GET", "/", "lead@example.com", false, true, nil, false)
	if !strings.Contains(page, `<span id="queue-count"></span>`) || strings.Contains(page, `class="count-pill"`) {
		t.Fatalf("an empty queue should show no count:\n%s", page)
	}
}

func TestNoAuthBand(t *testing.T) {
	const band = "Sign-in is off. Every visitor is an admin."
	cfg := core.DefaultConfig()
	cfg.InventoryTTL = 0
	svc := core.NewService(fake.New(), nil, cfg, nil)
	noauth := httptest.NewServer(New(svc, slog.New(slog.DiscardHandler), NoAuth{}, nil, nil).Handler())
	defer noauth.Close()
	if _, body := do(t, noauth, "GET", "/", "", false, nil, false); !strings.Contains(body, band) || !strings.Contains(body, `<div class="noauth" role="note">`) {
		t.Fatalf("NoAuth pages should say that sign-in is off:\n%s", body)
	}
	ts, _ := newServer(t)
	if _, body := do(t, ts, "GET", "/", "anna@example.com", false, nil, false); strings.Contains(body, band) {
		t.Fatal("with sign-in on, the band should not show")
	}
}

func TestNoticeLivesInMain(t *testing.T) {
	ts, _ := newServer(t)
	_, body := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	// The notice is empty, with nothing inside, so the stylesheet can hide
	// it with :empty.
	notice := strings.Index(body, `<p id="notice" class="notice"></p>`)
	if notice < 0 || notice < strings.Index(body, "<main>") || notice < strings.Index(body, "</header>") {
		t.Fatalf("the empty notice should sit in main, after the shell:\n%s", body)
	}
	_, body = do(t, ts, "POST", "/refresh", "ops@example.com", true, url.Values{}, true)
	if !strings.Contains(body, `<p id="notice" class="notice" hx-swap-oob="true">`) || !strings.Contains(body, "synced") {
		t.Fatalf("sync should fill the notice:\n%s", body)
	}
}

func TestEveryIconHasASymbol(t *testing.T) {
	ts, _ := newServer(t)
	do(t, ts, "POST", "/requests", "bob@example.com", false, url.Values{"name": {"bobs-req"}}, true)
	_, home := do(t, ts, "GET", "/", "ops@example.com", true, nil, false)
	_, account := do(t, ts, "GET", "/accounts/111111111111", "ops@example.com", true, nil, false)
	_, sync := do(t, ts, "POST", "/refresh", "ops@example.com", true, url.Values{}, true)
	used := map[string]bool{}
	for _, body := range []string{home, account, sync} {
		for _, m := range regexp.MustCompile(`href="#i-([\w-]+)"`).FindAllStringSubmatch(body, -1) {
			used[m[1]] = true
		}
	}
	if len(used) < 4 {
		t.Fatalf("expected the mark and the shell icons, found %v", used)
	}
	for name := range used {
		if !strings.Contains(home, `<symbol id="i-`+name+`"`) {
			t.Errorf("icon %s has no symbol in the sprite", name)
		}
	}
}
