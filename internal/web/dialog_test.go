package web

import (
	"regexp"
	"strings"
	"testing"
)

var (
	// dialogTag is a dialog of the page and everything in it.
	dialogTag = regexp.MustCompile(`(?s)<dialog id="([a-z-]+)"[^>]*>.*?</dialog>`)
	// idAttr is the id of an element.
	idAttr = regexp.MustCompile(` id="([^"]+)"`)
)

// dialogIn returns the dialog with this id in body, or "".
func dialogIn(body, id string) string {
	for _, m := range dialogTag.FindAllStringSubmatch(body, -1) {
		if m[1] == id {
			return m[0]
		}
	}
	return ""
}

// The home page of an approver holds the deny dialog and the summary
// dialog once, after the queue and the fleet, so no poll or morph touches
// them. An htmx answer never carries them, and the page keeps one live
// region. Someone who is not an approver gets no dialog. The ids of the
// page stay unique.
func TestDialogsRenderOnceOutsideTheMorphTargets(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	d.ask(t, "carls-box", "carl@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)

	for _, id := range []string{"deny-dialog", "summary-dialog"} {
		if n := strings.Count(home, `<dialog id="`+id+`"`); n != 1 {
			t.Errorf("the home page has %d %s, want 1", n, id)
		}
		at := strings.Index(home, `<dialog id="`+id+`"`)
		for _, region := range []string{`<section id="pending"`, `<section id="accounts"`} {
			from := strings.Index(home, region)
			if from < 0 {
				t.Fatalf("the home page has no %s", region)
			}
			if at > from && at < from+len(between(home, region, "</section>")) {
				t.Errorf("%s sits inside %s, so a morph would touch it", id, region)
			}
		}
	}
	for attr, want := range map[string]int{`id="notice"`: 1, "aria-live=": 1, `role="status"`: 1} {
		if got := strings.Count(home, attr); got != want {
			t.Errorf("the home page has %d %s, want %d", got, attr, want)
		}
	}
	seen := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(home, -1) {
		if seen[m[1]] {
			t.Errorf("the id %s shows twice", m[1])
		}
		seen[m[1]] = true
	}
	assertNoNestedForms(t, home)

	// An htmx answer to a decision carries no dialog.
	ref := refIn(t, home, "bobs-box")
	_, answer := d.decide(t, "lead@example.com", true, actionApprove, decideForm("", ref), true)
	if strings.Contains(answer, "<dialog") {
		t.Errorf("an htmx answer carries a dialog:\n%s", answer)
	}
	_, bobs := do(t, d.ts, "GET", "/", "bob@example.com", false, nil, false)
	if strings.Contains(bobs, "<dialog") {
		t.Errorf("someone who is not an approver gets a dialog:\n%s", bobs)
	}
}

// The deny dialog asks for a reason of 1 to 500 characters with the help of
// the old prompt, takes the focus on the reason, and ends with Cancel and
// the red Deny button. Its refusal slot is not a live region. The summary
// dialog names the account limit and that an approval cannot be undone,
// and Cancel takes the focus first. Both forms close their dialog, and
// Cancel skips the checks of the form.
func TestDialogsHoldTheirParts(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)

	deny := dialogIn(home, "deny-dialog")
	for _, want := range []string{
		`<dialog id="deny-dialog" class="dialog" aria-labelledby="deny-title">`,
		`<form method="dialog" class="dialog-form">`,
		`<h2 id="deny-title">`,
		`<ul id="deny-list" class="dialog-list"></ul>`,
		`<label class="field" for="deny-reason">Reason for denying</label>`,
		`<textarea id="deny-reason" class="dialog-reason" rows="4" maxlength="500" required autofocus aria-describedby="deny-help deny-error"></textarea>`,
		`<p id="deny-help" class="help">1 to 500 characters. playplace sends it with the denied notice and keeps it in history.</p>`,
		`<div id="deny-error" class="refusal"></div>`,
		`<div class="dialog-foot"><button type="submit" value="cancel" formnovalidate>Cancel</button> <button type="submit" value="confirm" id="deny-confirm" class="btn-deny">Deny</button></div>`,
	} {
		if !strings.Contains(deny, want) {
			t.Errorf("the deny dialog lacks %s:\n%s", want, deny)
		}
	}
	summary := dialogIn(home, "summary-dialog")
	for _, want := range []string{
		`<dialog id="summary-dialog" class="dialog" aria-labelledby="summary-title" aria-describedby="summary-facts">`,
		`<form method="dialog" class="dialog-form">`,
		`<ul id="summary-list" class="dialog-list dialog-rows"></ul>`,
		`<p>Each new account counts toward the account limit of the organization until 90 days after it closes.</p>`,
		`<p>An approval cannot be undone.</p>`,
		`<button type="submit" value="cancel" formnovalidate autofocus>Cancel</button>`,
		`id="summary-confirm" class="btn-approve"`,
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary dialog lacks %s:\n%s", want, summary)
		}
	}
	for name, dialog := range map[string]string{"deny": deny, "summary": summary} {
		for _, bad := range []string{"aria-live", `role="status"`, "hx-"} {
			if strings.Contains(dialog, bad) {
				t.Errorf("the %s dialog holds %s:\n%s", name, bad, dialog)
			}
		}
	}
	if i, j := strings.Index(summary, `value="cancel"`), strings.Index(summary, `value="confirm"`); i < 0 || j < i {
		t.Errorf("Cancel does not come before Approve in the summary:\n%s", summary)
	}
}

// The Deny button of a slip opens the deny dialog: its form names the
// dialog and the request and holds an empty reason field that the dialog
// fills. No form asks with the prompt of htmx any more. The bulk buttons
// name their dialogs too.
func TestDenyOpensTheDialog(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	form := regexp.MustCompile(`<form class="inline" hx-post="/requests/bobs-box/deny"[^>]*>`).FindString(home)
	for _, want := range []string{`data-dialog="deny"`, `data-request="bobs-box"`, `data-action="deny"`} {
		if !strings.Contains(form, want) {
			t.Errorf("the deny form lacks %s: %s", want, form)
		}
	}
	if got := slipForm(t, home, "bobs-box", "deny"); !got.Has("reason") || got.Get("reason") != "" {
		t.Errorf("the deny form has no empty reason field: %v", got)
	}
	if strings.Contains(home, "hx-prompt") {
		t.Error("a form still asks with hx-prompt")
	}
	for action, dialog := range map[string]string{actionApprove: "summary", actionDeny: "deny"} {
		button := regexp.MustCompile(`<button [^>]*data-bulk="` + action + `"[^>]*>`).FindString(home)
		if !strings.Contains(button, `data-dialog="`+dialog+`"`) {
			t.Errorf("the %s button does not open the %s dialog: %s", action, dialog, button)
		}
	}

	// The deny form posts the reason that the dialog wrote.
	fields := slipForm(t, home, "bobs-box", "deny")
	fields.Set("reason", "no budget")
	res, body := doAs(t, d.ts, "POST", "/requests/bobs-box/deny", "lead@example.com", false, true, fields, true)
	if res.Header.Get("X-Form-Error") != "" || !strings.Contains(body, "Denied bobs-box.") {
		t.Errorf("the deny form with a reason = %v:\n%s", res.Header, body)
	}
	// A refused reason names the reason field, so app.js opens the dialog
	// again with the refusal.
	d.ask(t, "carls-box", "carl@example.com")
	_, home = doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)
	res, _ = doAs(t, d.ts, "POST", "/requests/carls-box/deny", "lead@example.com", false, true, slipForm(t, home, "carls-box", "deny"), true)
	if res.Header.Get(invalidFieldHeader) != "reason" {
		t.Errorf("an empty reason names the field %q, want reason", res.Header.Get(invalidFieldHeader))
	}
}

// app.js holds the dialog controller. The one htmx:confirm listener opens
// the dialog of an element with data-dialog and holds the request back.
// The dialog sends it with issueRequest(true) after the viewer confirms,
// with the reason in the hidden reason field of the form that posts.
// Approve selected asks only for two or more slips. A reason that the
// server refuses opens the deny dialog again with the text and the
// refusal. The summary lines are built with DOM calls and text, never
// markup.
func TestDialogControllerLivesInAppJS(t *testing.T) {
	js := staticText(t, "app.js")
	confirm := listeners(js, "htmx:confirm")
	if len(confirm) != 1 || !strings.Contains(confirm[0], `elt.hasAttribute("data-dialog") && askFirst(elt, ev.detail.issueRequest)`) {
		t.Fatalf("the one htmx:confirm listener does not hold back a request for its dialog")
	}
	holdsEach(t, "askFirst", funcBody(t, js, "function askFirst(elt, issue) {"),
		`if (!names.length) {`,
		`kind === "summary" && names.length >= 2`,
		`kind === "deny"`)
	holdsEach(t, "openDeny", funcBody(t, js, "function openDeny(elt, names, issue, text, note) {"),
		`dialog.showModal();`, `reason.focus();`, `reason.value = text;`, `span.textContent = note;`)
	holdsEach(t, "openSummary", funcBody(t, js, "function openSummary(elt, names, issue) {"),
		`dialog.showModal();`, `button[value="cancel"]`, `cancel.focus();`, `summaryLine(boxes[i])`)
	holdsEach(t, "confirmAsk", funcBody(t, js, "function confirmAsk(ask, text) {"),
		`form.elements.namedItem("reason")`, `field.value = text;`, `ask.issue(true);`, `sameNames(ask.names, now)`)
	submit := listeners(js, "submit")
	if len(submit) != 1 || !strings.Contains(submit[0], `ev.submitter.value === "confirm"`) || !strings.Contains(submit[0], "dialog.close(") {
		t.Errorf("app.js has no submit listener that confirms a dialog")
	}
	reopen := false
	for _, body := range listeners(js, "htmx:afterRequest") {
		if strings.Contains(body, `getResponseHeader("X-Invalid-Field") !== "reason"`) && strings.Contains(body, "openDeny(ask.elt, ask.names, ask.issue, ask.text, refusal)") {
			reopen = true
		}
	}
	if !reopen {
		t.Error("no afterRequest listener opens the deny dialog again on a refused reason")
	}
	for _, head := range []string{"function summaryLine(box) {", "function listInto(list, names) {", "function openDeny(elt, names, issue, text, note) {", "function openSummary(elt, names, issue) {"} {
		body := funcBody(t, js, head)
		for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s writes markup with %s", head, bad)
			}
		}
	}
	holdsEach(t, "summaryLine", funcBody(t, js, "function summaryLine(box) {"),
		`document.createElement("li")`, `name.textContent =`, `facts.textContent =`, `limits.textContent =`,
		`box.getAttribute("data-owner")`, `box.getAttribute("data-lifetime")`, `box.getAttribute("data-budget")`, `box.getAttribute("data-limits")`)
	if n := len(listeners(js, "htmx:beforeSwap")); n != 1 {
		t.Errorf("app.js has %d htmx:beforeSwap listeners, want 1", n)
	}
}

// The dialog follows DESIGN.md: paper, a 1 px ink border, 8 px corners, the
// width with a 16 px gutter, panel rules at the head and the footer, a
// paper tint footer and the scrim behind it. The final Deny button is red
// ink with paper text and deep red ink on hover.
func TestDialogFollowsTheDesign(t *testing.T) {
	top, _ := layerRules(t, "components")
	wantDecls(t, "components", top, ".dialog", map[string]string{
		"width":         "min(640px, calc(100% - 32px))",
		"padding":       "0",
		"border":        "1px solid var(--ink)",
		"border-radius": "var(--r-panel)",
		"background":    "var(--paper)",
		"color":         "var(--ink)",
	})
	wantDecls(t, "components", top, ".dialog::backdrop", map[string]string{"background": "var(--scrim)"})
	wantDecls(t, "components", top, ".dialog-head", map[string]string{"border-bottom": "1px solid var(--rule)"})
	wantDecls(t, "components", top, ".dialog-foot", map[string]string{"border-top": "1px solid var(--rule)", "background": "var(--paper-tint)"})
	wantDecls(t, "components", top, ".btn-deny", map[string]string{"background": "var(--red-ink)", "border-color": "var(--red-ink)", "color": "var(--paper)"})
	wantDecls(t, "components", top, ".btn-deny:hover", map[string]string{"background": "var(--red-deep)", "border-color": "var(--red-deep)", "color": "var(--paper)"})
}
