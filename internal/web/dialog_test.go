package web

import (
	"net/http"
	"net/url"
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

// The home page of an approver holds the deny dialog, the summary dialog
// and the close dialog once, after the queue and the fleet, so no poll or
// morph touches them. An htmx answer never carries them, and the page
// keeps one live region. Someone who is not an approver gets only the
// close dialog, because anyone can own an account. The ids of the page
// stay unique.
func TestDialogsRenderOnceOutsideTheMorphTargets(t *testing.T) {
	d := newDesk(t, nil, nil, nil)
	d.ask(t, "bobs-box", "bob@example.com")
	d.ask(t, "carls-box", "carl@example.com")
	_, home := doAs(t, d.ts, "GET", "/", "lead@example.com", false, true, nil, false)

	for _, id := range []string{"deny-dialog", "summary-dialog", "close-dialog"} {
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
	if n := strings.Count(bobs, "<dialog"); n != 1 || strings.Count(bobs, `<dialog id="close-dialog"`) != 1 {
		t.Errorf("someone who is not an approver gets %d dialogs, want only the close dialog:\n%s", n, bobs)
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
	wantDecls(t, "components", top, ".dialog-who", map[string]string{"font-weight": "600", "overflow-wrap": "anywhere"})
	wantDecls(t, "components", top, ".btn-deny:hover", map[string]string{"background": "var(--red-deep)", "border-color": "var(--red-deep)", "color": "var(--paper)"})
}

// The close dialog names the consequences of a closure, as the TUI and the
// docs do: closure is asynchronous, charges can continue while it is
// pending, commitments and subscriptions can outlive it, recovery needs
// AWS Support within 90 days, and the account counts toward the account
// limit for 90 days after it closes. Cancel comes first and takes the
// focus, and the final button is the red Close account. It holds no live
// region and no htmx attribute.
func TestCloseDialogHoldsTheConsequences(t *testing.T) {
	ts, _ := newServer(t)
	_, home := do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	dialog := dialogIn(home, "close-dialog")
	for _, want := range []string{
		`<dialog id="close-dialog" class="dialog" aria-labelledby="close-title" aria-describedby="close-who close-facts">`,
		`<form method="dialog" class="dialog-form">`,
		`<h2 id="close-title">Close account?</h2>`,
		`<p id="close-who" class="dialog-who"></p>`,
		`<p>AWS closes the account asynchronously. The account stays closing until AWS confirms that it is closed.</p>`,
		`<p>Charges can continue while closure is pending.</p>`,
		`<p>Commitments and subscriptions can outlive closure.</p>`,
		`<p>Recovery needs AWS Support during the 90-day post-closure period.</p>`,
		`<p>The account counts toward the account limit of the organization until 90 days after it closes.</p>`,
		`<div class="dialog-foot"><button type="submit" value="cancel" formnovalidate autofocus>Cancel</button> <button type="submit" value="confirm" id="close-confirm" class="btn-deny">Close account</button></div>`,
	} {
		if !strings.Contains(dialog, want) {
			t.Errorf("the close dialog lacks %s:\n%s", want, dialog)
		}
	}
	for _, bad := range []string{"aria-live", `role="status"`, "hx-"} {
		if strings.Contains(dialog, bad) {
			t.Errorf("the close dialog holds %s:\n%s", bad, dialog)
		}
	}
	if strings.Count(dialog, "autofocus") != 1 || strings.Index(dialog, "autofocus") > strings.Index(dialog, `value="confirm"`) {
		t.Errorf("Cancel is not the one control that takes the focus first:\n%s", dialog)
	}
}

// The Close button of a fleet row opens the close dialog. Its form names
// the dialog and carries the name, the AWS account ID and the owner that
// the row shows, and still ends with data-action="close" for the focus
// rule. No form asks with the confirm of htmx for a closure any more, and
// the row button keeps the plain word Close. The form still closes the
// account when the dialog sends it.
func TestCloseOpensTheDialog(t *testing.T) {
	ts, _ := newServer(t)
	_, home := do(t, ts, "GET", "/", "anna@example.com", false, nil, false)
	form := regexp.MustCompile(`(?s)<form class="inline" hx-post="/accounts/111111111111/close"[^>]*>.*?</form>`).FindString(home)
	if form == "" {
		t.Fatalf("the home page has no Close form:\n%s", home)
	}
	if want := ` hx-sync="this:drop" data-dialog="close" data-name="annas-box" data-account="111111111111" data-owner="anna@example.com" data-action="close">`; !strings.Contains(form, want) {
		t.Errorf("the Close form lacks %s:\n%s", want, form)
	}
	if !strings.Contains(form, `<button type="submit">Close<span class="vh"> annas-box</span></button>`) {
		t.Errorf("the Close button is not the plain word Close:\n%s", form)
	}
	if strings.Contains(form, "btn-deny") {
		t.Errorf("the row button is red; only the final button of the dialog is:\n%s", form)
	}
	if strings.Contains(form, "hx-confirm") {
		t.Error("a Close form still asks with hx-confirm")
	}
	res, body := do(t, ts, "POST", "/accounts/111111111111/close", "anna@example.com", false, url.Values{}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Requested closure of annas-box.") {
		t.Errorf("the Close form after the dialog = %d:\n%s", res.StatusCode, body)
	}
	if strings.Contains(body, "<dialog") {
		t.Errorf("the answer to a closure carries a dialog:\n%s", body)
	}
}

// app.js opens the close dialog from the one htmx:confirm listener. It
// writes the title and the line with the AWS account ID and the owner as
// text, never as markup, puts the focus on Cancel and holds the request
// until the viewer confirms.
func TestCloseDialogLivesInAppJS(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "askFirst", funcBody(t, js, "function askFirst(elt, issue) {"),
		`if (kind === "close") {`, `return openClose(elt, issue);`)
	body := funcBody(t, js, "function openClose(elt, issue) {")
	holdsEach(t, "openClose", body,
		`document.getElementById("close-dialog")`,
		`elt.getAttribute("data-name")`,
		`elt.getAttribute("data-account")`,
		`elt.getAttribute("data-owner")`,
		`document.getElementById("close-title").textContent = "Close account " + name + "?";`,
		`document.getElementById("close-who").textContent =`,
		`kind: "close"`,
		`dialog.showModal();`,
		`button[value="cancel"]`,
		`cancel.focus();`)
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML"} {
		if strings.Contains(body, bad) {
			t.Errorf("openClose writes markup with %s", bad)
		}
	}
}

// A poll can morph the queue while a dialog is open. The deny dialog of a
// slip notes the hidden values of its form when it opens and puts them back
// before the post, so the post carries the version that the viewer saw and
// the server refuses a request that changed. A refused reason that opens
// the dialog again keeps the same values.
func TestDenyDialogPostsWhatTheViewerSaw(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "hiddenOf", funcBody(t, js, "function hiddenOf(elt) {"),
		`input[type="hidden"]:not([name="reason"])`,
		`out.push([fields[i].name, fields[i].value]);`)
	holdsEach(t, "putHidden", funcBody(t, js, "function putHidden(elt, noted) {"),
		`noted[j][0] === fields[i].name`,
		`fields[i].value = noted[j][1];`)
	holdsEach(t, "openDeny", funcBody(t, js, "function openDeny(elt, names, issue, text, note) {"),
		"if (!elt.hasAttribute(\"data-bulk\")) {\n    asking.hidden = hiddenOf(elt);\n  }")
	confirm := funcBody(t, js, "function confirmAsk(ask, text) {")
	holdsEach(t, "confirmAsk", confirm,
		"putHidden(elt, ask.hidden);",
		"hidden: ask.hidden };")
	if strings.Index(confirm, "putHidden(elt, ask.hidden);") > strings.Index(confirm, "ask.issue(true);") {
		t.Error("confirmAsk puts the noted values back after the post")
	}
	reopen := false
	for _, body := range listeners(js, "htmx:afterRequest") {
		if strings.Contains(body, "openDeny(ask.elt, ask.names, ask.issue, ask.text, refusal)") && strings.Contains(body, "asking.hidden = ask.hidden;") {
			reopen = true
		}
	}
	if !reopen {
		t.Error("the deny dialog that opens again on a refused reason drops the noted values")
	}
}

// The cleanup after an action takes aria-describedby from every marked
// field, and the deny dialog is never rendered again. So openDeny links the
// reason to its help and its refusal slot each time it opens.
func TestDenyReasonKeepsItsDescription(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "openDeny", funcBody(t, js, "function openDeny(elt, names, issue, text, note) {"),
		`reason.removeAttribute("aria-invalid");`,
		`reason.setAttribute("aria-describedby", "deny-help deny-error");`)
	ts, _ := newServer(t)
	_, home := do(t, ts, "GET", "/", "anna@example.com", true, nil, false)
	if !strings.Contains(dialogIn(home, "deny-dialog"), `aria-describedby="deny-help deny-error"`) {
		t.Error("the server renders the reason with another description than openDeny sets")
	}
}

// A dialog opens only when no dialog is open, so two dialogs never stack
// and never share asking.
func TestDialogsNeverStack(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "dialogOpen", funcBody(t, js, "function dialogOpen() {"), `document.querySelector("dialog[open]")`)
	for _, head := range []string{"function openDeny(elt, names, issue, text, note) {", "function openSummary(elt, names, issue) {", "function openClose(elt, issue) {"} {
		body := funcBody(t, js, head)
		if !strings.Contains(body, "dialogOpen()") || strings.Contains(body, "dialog.open") {
			t.Errorf("%s does not check every dialog before it opens:\n%s", head, body)
		}
	}
}

// htmx drops a request without a word while another request of the same
// hx-sync element runs. A dialog then does not open, and a dialog that the
// viewer confirms while such a request runs says so: the deny dialog opens
// again with the reason, and the others write the note into the region.
func TestDialogNeverSendsIntoADrop(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "syncBusy", funcBody(t, js, "function syncBusy(elt) {"),
		`'[form="desk-form"][aria-busy="true"]'`,
		`elt.querySelector('[aria-busy="true"]')`)
	confirm := listeners(js, "htmx:confirm")
	if len(confirm) != 1 || !strings.Contains(confirm[0], "if (elt.hasAttribute(\"data-dialog\") && syncBusy(elt)) {\n    ev.preventDefault();\n    return;\n  }") {
		t.Error("the htmx:confirm listener opens a dialog while its request runs")
	}
	body := funcBody(t, js, "function confirmAsk(ask, text) {")
	holdsEach(t, "confirmAsk", body,
		"if (syncBusy(elt)) {",
		`openDeny(elt, ask.names, ask.issue, text, busyNote)`,
		"say(busyNote);")
	if strings.Index(body, "if (syncBusy(elt)) {") > strings.Index(body, "lastDeny = {") {
		t.Error("confirmAsk notes lastDeny before it checks for a running request")
	}
}

// A poll can take away the button that opened a dialog. The focus then
// goes to the name of the first slip that waits, or to the queue heading,
// and a confirm says why nothing was sent. A bulk control that a decision
// takes away gives the focus the same way.
func TestFocusNeverFallsToThePageAfterADialog(t *testing.T) {
	js := staticText(t, "app.js")
	holdsEach(t, "firstWaiting", funcBody(t, js, "function firstWaiting() {"),
		`controlIn(rows[i], "button.slip-name, .slip-title[tabindex]")`,
		`document.getElementById("pending-title")`)
	submit := listeners(js, "submit")
	if len(submit) != 1 {
		t.Fatalf("app.js has %d submit listeners, want 1", len(submit))
	}
	holdsEach(t, "the submit listener", submit[0],
		`ask.kind !== "close" && !document.body.contains(ask.elt)`,
		"var target = firstWaiting();",
		`say(ask.elt.hasAttribute("data-bulk") ? noneLeft : goneText(ask.names[0]));`)
	holdsEach(t, "notePlace", funcBody(t, js, "function notePlace(xhr, source) {"),
		`active.closest("#desk-bar")`,
		"desk: true,",
		`source.hasAttribute("data-bulk")`)
	settle := listeners(js, "htmx:afterSettle")
	if len(settle) != 1 {
		t.Fatalf("app.js has %d htmx:afterSettle listeners, want 1", len(settle))
	}
	holdsEach(t, "the focus rule", settle[0], "if (was.desk) {", "var first = firstWaiting();", "repeatRegion(was.said);")
	if strings.Index(settle[0], "if (was.desk) {") < strings.Index(settle[0], "if (active && active !== document.body && !ended) {") {
		t.Error("the focus rule moves the focus from a bulk control that is still in the page")
	}
}
