// Small page behaviours. Kept out of inline attributes so the page can run
// under a Content-Security-Policy without unsafe-inline or unsafe-eval.

// This file loads in <head> without defer, so the class is on <html> before
// the first paint. The stylesheet hides the phone menu only under it, so a
// page without script keeps every shell item in view.
document.documentElement.classList.add("js");

// After a form with data-reset-on-success submits through htmx and the
// server did not flag a validation error, clear it.
document.addEventListener("htmx:afterRequest", function (ev) {
  var form = ev.target && ev.target.closest && ev.target.closest("form[data-reset-on-success]");
  if (!form || !ev.detail.successful) {
    return;
  }
  if (ev.detail.xhr && ev.detail.xhr.getResponseHeader("X-Form-Error")) {
    return;
  }
  form.reset();
});

// After an action, the refusal slots and the marked fields show only what
// the last answer said. The slot under a form and the slot above the
// accounts of the home page empty on success. A refusal fills its own slot
// and can name the field it is about. That field gets aria-invalid and
// points to the slot, so a screen reader reads the refusal with the field.
// The poll and other GETs leave both alone.
document.addEventListener("htmx:afterRequest", function (ev) {
  var d = ev.detail;
  if (!d.successful || !d.xhr || !d.requestConfig || d.requestConfig.verb === "get") {
    return;
  }
  var marked = document.querySelectorAll("[aria-invalid=true]");
  for (var i = 0; i < marked.length; i++) {
    marked[i].removeAttribute("aria-invalid");
    marked[i].removeAttribute("aria-describedby");
  }
  if (!d.xhr.getResponseHeader("X-Form-Error")) {
    var slots = ["form-error", "account-error"];
    for (var s = 0; s < slots.length; s++) {
      var err = document.getElementById(slots[s]);
      if (err) {
        err.textContent = "";
      }
    }
    return;
  }
  var field = d.xhr.getResponseHeader("X-Invalid-Field");
  var slot = d.xhr.getResponseHeader("X-Invalid-Slot");
  var form = d.requestConfig.elt;
  var input = field && slot && form && form.elements ? form.elements.namedItem(field) : null;
  if (input && input.setAttribute) {
    input.setAttribute("aria-invalid", "true");
    input.setAttribute("aria-describedby", slot);
  }
});

// The phone menu. The listener sits on the document, because the toggle
// does not exist yet when this file runs.
document.addEventListener("click", function (ev) {
  var toggle = ev.target && ev.target.closest && ev.target.closest(".menu-toggle");
  if (!toggle) {
    return;
  }
  var shell = toggle.closest(".shell");
  var open = shell.classList.toggle("menu-open");
  toggle.setAttribute("aria-expanded", open ? "true" : "false");
});

// Escape closes the menu and gives focus back to its toggle. While a
// dialog is open, Escape closes only the dialog, which gives the focus
// back to the control that opened it, a link of the menu too.
document.addEventListener("keydown", function (ev) {
  if (ev.key !== "Escape" || dialogOpen()) {
    return;
  }
  var shell = document.querySelector(".shell.menu-open");
  if (!shell) {
    return;
  }
  shell.classList.remove("menu-open");
  var toggle = shell.querySelector(".menu-toggle");
  if (toggle) {
    toggle.setAttribute("aria-expanded", "false");
    toggle.focus();
  }
});

// The keys. keysKey holds the off switch of the footer in localStorage, so
// it lasts for the browser, in every tab and after a reload. keysOn is the
// state itself, so the switch still works when the storage throws, as in
// some private windows. The keys are on by default.
var keysKey = "playplace.keys";
var keysOn = true;

// readKeys reads the switch from the storage and shows it. When the
// storage throws, the state in memory stays.
function readKeys() {
  try {
    keysOn = window.localStorage.getItem(keysKey) !== "off";
  } catch (err) {
    // Keep the state in memory.
  }
  showKeys();
}

// showKeys puts the keys-off class on <html>. The stylesheet then shows
// the word of the switch that fits, Keys on or Keys off, and the note of
// the key list. The switch holds both words, so it never shows the wrong
// one while the page loads. showKeys also names the keys on Approve and
// Deny only while they are on.
function showKeys() {
  document.documentElement.classList.toggle("keys-off", !keysOn);
  syncShortcuts();
}
// The class is on <html> before the first paint.
readKeys();

// syncShortcuts sets aria-keyshortcuts on each button with data-key while
// the keys are on, and removes it while they are off. While a box is
// checked, a and d press the bulk buttons, so only the bulk buttons name
// them. The server cannot know the switch, so showKeys and syncPicks call
// it, and syncPicks runs after each morph and each change of a box.
function syncShortcuts() {
  var buttons = document.querySelectorAll("[data-key]");
  var picked = buttons.length > 0 && pickedCount() > 0;
  for (var i = 0; i < buttons.length; i++) {
    var b = buttons[i];
    if (keysOn && (!picked || b.hasAttribute("data-bulk"))) {
      b.setAttribute("aria-keyshortcuts", b.getAttribute("data-key"));
    } else {
      b.removeAttribute("aria-keyshortcuts");
    }
  }
}

// setKeys turns the keys on or off for this browser. Every read and write
// of the storage sits in try/catch.
function setKeys(on) {
  keysOn = on;
  try {
    if (on) {
      window.localStorage.removeItem(keysKey);
    } else {
      window.localStorage.setItem(keysKey, "off");
    }
  } catch (err) {
    // The switch then lasts only until the page reloads.
  }
  showKeys();
}

// The switch of the footer turns the keys on or off and says so in the
// live region. The Key list button opens the list of the keys.
document.addEventListener("click", function (ev) {
  var t = ev.target && ev.target.closest ? ev.target : null;
  if (t && t.closest("#keys-toggle")) {
    setKeys(!keysOn);
    tell(keysOn ? "Keys on." : "Keys off. The Key list button still lists them.");
  } else if (t && t.closest("#keys-show")) {
    openKeys();
  }
});

// Another tab of the browser can turn the keys on or off.
window.addEventListener("storage", function (ev) {
  if (ev.key === keysKey || ev.key === null) {
    readKeys();
  }
});

// typing reports whether el takes text, so a key there is a character
// and never a shortcut: an input other than a checkbox or a radio, a
// textarea, a select, which picks an option by its first letter, or an
// editable element.
function typing(el) {
  if (!el || !el.tagName) {
    return false;
  }
  if (el.isContentEditable || el.tagName === "TEXTAREA" || el.tagName === "SELECT") {
    return true;
  }
  return el.tagName === "INPUT" && el.type !== "checkbox" && el.type !== "radio";
}

// keyLists are the lists that j and k move through: the slips of the
// queue or of Your requests, the rows of the fleet or the loan cards, and
// the rows of the Needs attention ledger. want is the control of a row
// that takes the focus: its name or its link. Enter then opens a slip of
// the desk or follows the link, as a click does. A slip of Your requests
// and an edit slip take the focus on their heading, which has no action
// for Enter, so the focus never lands on Save.
var keyLists = [
  { rows: "#pending li.slip", want: "button.slip-name, .slip-title[tabindex]" },
  { rows: "#accounts tbody > tr, #accounts li.loan", want: "td a, .loan-name a" },
  { rows: "#attention li", want: "a.ledger-row" }
];

// moveRow moves the focus by step rows, 1 for j and -1 for k, in the list
// that holds the focus. It skips a slip that no longer waits, because it
// has no control, and it stops at each end. With the focus in no list, it
// goes to the first row of the main list of the page: the queue of an
// approver, else the accounts. It reports whether the focus moved.
function moveRow(step) {
  var active = document.activeElement;
  for (var l = 0; l < keyLists.length; l++) {
    var row = active && active.closest ? active.closest(keyLists[l].rows) : null;
    if (!row) {
      continue;
    }
    var rows = document.querySelectorAll(keyLists[l].rows);
    for (var i = Array.prototype.indexOf.call(rows, row) + step; i >= 0 && i < rows.length; i += step) {
      if (focusRow(rows[i], keyLists[l].want)) {
        return true;
      }
    }
    return false;
  }
  var order = document.querySelector("#pending.desk") ? [0, 1, 2] : [1, 0, 2];
  for (var o = 0; o < order.length; o++) {
    var list = document.querySelectorAll(keyLists[order[o]].rows);
    for (var r = 0; r < list.length; r++) {
      if (focusRow(list[r], keyLists[order[o]].want)) {
        return true;
      }
    }
  }
  return false;
}

// focusRow moves the focus to the control want of row, and reports
// whether row has one. The row scrolls into view as little as it must.
// The root keeps the height of the bulk bar and 8 px clear at the top, so
// the row never ends under the bar.
function focusRow(row, want) {
  var control = controlIn(row, want);
  if (!control) {
    return false;
  }
  control.focus({ preventScroll: true });
  row.scrollIntoView({ block: "nearest" });
  return true;
}

// keySlip is the slip of the desk that holds the focus, or null. Only an
// approver has the desk.
function keySlip() {
  var active = document.activeElement;
  if (!active || !active.closest || !document.querySelector("#pending.desk")) {
    return null;
  }
  return active.closest("#pending li.slip");
}

// cannotPick says why x, a or d does nothing on the slip row.
function cannotPick(row) {
  var name = row.id.slice(4);
  if (row.getAttribute("data-state") !== "pending") {
    return name + " is no longer waiting.";
  }
  if (row.querySelector("form[data-editing]")) {
    return "Save or cancel your changes to " + name + " first.";
  }
  if (row.querySelector(".chip-mine")) {
    return name + " is your request. Another approver must decide it.";
  }
  return "You cannot decide " + name + ".";
}

// pickKey checks or clears the box of the focused slip, as a click does,
// so the change listener updates the count and the bulk controls. A slip
// with no box says why in the live region. It reports whether it acted.
function pickKey() {
  var row = keySlip();
  if (!row) {
    return false;
  }
  var box = row.querySelector('.slip-pick input[name="req"]');
  if (box) {
    box.click();
  } else {
    tell(cannotPick(row));
  }
  return true;
}

// decideKey approves or denies, for kind "approve" or "deny". With checked
// boxes it clicks Approve selected or Deny selected, which keep the cap
// and the dialogs. Else it clicks Approve or Deny of the focused slip. A
// single Approve does not ask again, and Deny opens its dialog. Before
// the click, the focus goes to the name of the slip, so the Deny dialog
// gives the focus back to the name, and the focus rule then moves on to
// the name of the next slip, never to a button that decides. A slip that
// cannot be decided says why. It reports whether it acted.
function decideKey(kind) {
  var bulk = document.getElementById(kind === "approve" ? "desk-approve" : "desk-deny");
  if (bulk && pickedCount() > 0) {
    bulk.click();
    return true;
  }
  var row = keySlip();
  if (!row) {
    return false;
  }
  var button = row.querySelector('form[data-action="' + kind + '"] button');
  if (!button) {
    tell(cannotPick(row));
    return true;
  }
  var name = row.querySelector("button.slip-name");
  if (name) {
    name.focus({ preventScroll: true });
  }
  button.click();
  return true;
}

// keysFrom is the control that had the focus when the key list opened,
// with its slip or row, so keysClosed can find a place when a poll took
// the control away.
var keysFrom = null;

// openKeys opens the list of the keys, with Close in the focus. It opens
// only when no other dialog is open. Another dialog that Escape closed
// can leave asking set, so it clears asking first, and the submit
// listener lets Close shut the list. It returns false when the page has
// no list.
function openKeys() {
  var dialog = document.getElementById("keys-dialog");
  if (!dialog || dialogOpen()) {
    return false;
  }
  asking = null;
  var active = document.activeElement;
  var row = active && active.closest ? active.closest(slipRows + ", " + fleetRows + ", " + ledgerRows) : null;
  keysFrom = { el: active, row: row && row.id ? row.id : null, rows: null };
  if (row) {
    keysFrom.rows = row.matches(slipRows) ? slipRows : row.matches(ledgerRows) ? ledgerRows : fleetRows;
  }
  dialog.addEventListener("close", keysClosed, { once: true });
  dialog.returnValue = "";
  dialog.showModal();
  var close = dialog.querySelector('button[value="cancel"]');
  if (close) {
    close.focus();
  }
  return true;
}

// keysClosed runs when the key list closes, by Close or by Escape, after
// the browser gave the focus back. When a poll took the control away
// while the list was open, the focus goes to the same control in its own
// slip or row, as after the other dialogs. Else it goes to the name of
// the first slip that waits, or to the heading of the section. A slip
// that left gets a message in the region.
function keysClosed() {
  var from = keysFrom;
  keysFrom = null;
  if (!from || !from.el || !from.rows || document.body.contains(from.el)) {
    return;
  }
  var row = from.row ? document.getElementById(from.row) : null;
  var want = from.rows === slipRows ? "button.slip-name, .slip-title[tabindex]"
    : from.rows === ledgerRows ? "a.ledger-row" : "td a, .loan-name a";
  var target = controlIn(row, want);
  if (!target) {
    target = from.rows === slipRows ? firstWaiting()
      : document.getElementById(from.rows === ledgerRows ? "attention-title" : "accounts-title");
  }
  if (target) {
    target.focus();
  }
  if (from.rows === slipRows && from.row && !row) {
    say(goneText(from.row.slice(4)));
  }
}

// The keys of the page. A key never acts while a modifier is held, while
// the browser composes text, in a field that takes text, or while a
// dialog is open: a key would then reach a form behind the dialog, and
// askFirst lets a request go when a dialog is open already. The switch
// turns off every key, ? too. The Key list button of the footer still
// opens the list. A held key repeats only j, k and /.
document.addEventListener("keydown", function (ev) {
  if (ev.defaultPrevented || ev.isComposing || ev.ctrlKey || ev.metaKey || ev.altKey) {
    return;
  }
  if (typing(ev.target) || dialogOpen()) {
    return;
  }
  if (!keysOn) {
    return;
  }
  var key = ev.key;
  if (key === "?") {
    if (!ev.repeat && openKeys()) {
      ev.preventDefault();
    }
    return;
  }
  var done = false;
  if (key === "j" || key === "k") {
    done = moveRow(key === "j" ? 1 : -1);
  } else if (key === "/") {
    var box = document.getElementById("fleet-q");
    if (box) {
      box.focus();
      box.select();
      done = true;
    }
  } else if (ev.repeat) {
    return;
  } else if (key === "x") {
    done = pickKey();
  } else if (key === "a") {
    done = decideKey("approve");
  } else if (key === "d") {
    done = decideKey("deny");
  }
  if (done) {
    ev.preventDefault();
  }
});

// A click on the name of a slip opens or closes its details. Enter and
// Space click a button, so the keyboard needs no listener of its own.
document.addEventListener("click", function (ev) {
  var button = ev.target && ev.target.closest && ev.target.closest("button.slip-name");
  if (button) {
    setOpen(button, button.getAttribute("aria-expanded") !== "true");
  }
});

// setOpen opens or closes the slip of a name button: each part that its
// aria-controls names, the details and the tools.
function setOpen(button, open) {
  var ids = (button.getAttribute("aria-controls") || "").split(" ");
  var parts = [];
  for (var i = 0; i < ids.length; i++) {
    var part = ids[i] ? document.getElementById(ids[i]) : null;
    if (part) {
      parts.push(part);
    }
  }
  if (!parts.length) {
    return;
  }
  button.setAttribute("aria-expanded", open ? "true" : "false");
  for (var j = 0; j < parts.length; j++) {
    if (open) {
      parts[j].removeAttribute("hidden");
    } else {
      parts[j].setAttribute("hidden", "");
    }
  }
}

// The element that sent the request whose answer htmx is about to swap. In
// htmx:beforeSwap, detail.elt is the target, so the source comes from the
// request config. The keep rules below read it.
var swapSource = null;
document.addEventListener("htmx:beforeSwap", function (ev) {
  swapSource = (ev.detail.requestConfig && ev.detail.requestConfig.elt) || null;
  // htmx does not swap a 4xx answer. A 403 refusal carries HX-Reswap: none
  // and only out-of-band parts, the slot and the region, so the page can
  // take them. So does the 400 or 413 of a decision form that playplace
  // refused. Such an answer is then not an error, and htmx logs nothing.
  // X-Form-Error still keeps the form as the viewer filled it.
  var xhr = ev.detail.xhr;
  var refused = xhr && (xhr.status === 400 || xhr.status === 403 || xhr.status === 413);
  if (refused && xhr.getResponseHeader("HX-Reswap") === "none") {
    ev.detail.shouldSwap = true;
    ev.detail.isError = false;
  }
  // A poll that started before the latest action carries the state from
  // before it, so its answer is dropped.
  if (xhr && xhr.pollSeq !== undefined && xhr.pollSeq !== actionSeq) {
    ev.detail.shouldSwap = false;
  }
  // The focus rule runs after the swap, from the place that the focus
  // has now.
  if (ev.detail.shouldSwap) {
    notePlace(xhr, swapSource);
  }
});

// keepNode reports whether a morph leaves this node as it is. The live
// region in the queue panel stays, so a morph of the panel never clears the
// message that an answer just wrote into it. An open edit slip stays through
// every answer but the one to its own form, so a poll or another row's
// action never drops what the admin typed. A filled refusal slot of the
// queue stays through a poll, so the viewer can still read it. The rules use
// ids, not classes, so a node that idiomorph pairs by shape is never kept by
// mistake.
function keepNode(node) {
  if (node.id === "notice") {
    return true;
  }
  if (node.tagName === "LI" && node.querySelector("form[data-editing]")) {
    return !(swapSource && node.contains(swapSource));
  }
  if (node.id === "pending-error" && node.firstChild) {
    return !!swapSource && swapSource === poller();
  }
  return false;
}

// deskPicks are the checkboxes of the slips. Each one joins the desk form
// with form="desk-form", and its value is the name and the version of its
// request.
var deskPicks = '#pending .slip-pick input[name="req"]';

// keepPick keeps a checked box of a slip through a morph while its value
// stays the same. The server never renders a box checked, so the new node
// is never checked. idiomorph copies the checked property from the new
// node to the old one when the two differ. Before it does, it asks
// beforeAttributeUpdated, but that callback gets only the name and the old
// node, never the new value. beforeNodeMorphed runs first and gets both
// nodes. So when the old box is checked and the new box has the same value,
// keepPick checks the new node too, and idiomorph finds nothing to change.
// A request that changed has a new version and so a new value, and its box
// clears. A bulk decision then never acts on values that the approver did
// not see. A decided slip has no box, so its box leaves with it.
function keepPick(node, next) {
  if (node.tagName !== "INPUT" || node.type !== "checkbox" || node.name !== "req" || !node.checked) {
    return;
  }
  if (next && next.nodeType === 1 && next.tagName === "INPUT" && next.type === "checkbox" &&
      next.getAttribute("name") === "req" && next.getAttribute("value") === node.getAttribute("value")) {
    next.checked = true;
  }
}

// syncPicks keeps the bulk controls in step with the checked slips. The
// count names how many are checked. The select-all box is checked when
// every box is, and mixed when only some are. The server renders the
// copies of the shown values in each box label disabled, and syncPicks
// turns on only the copies of a checked box. A disabled field is not
// sent, so a bulk post carries the copies of the checked slips only, and
// a long queue does not make the post too large. A morph and a page load
// both reset the controls, so the afterSettle listener and the
// DOMContentLoaded listener call it, and so does each change of a box.
// It also names the keys of the buttons that a and d press.
// The htmx:confirm listener and confirmAsk call it before a bulk post.
function syncPicks() {
  var boxes = document.querySelectorAll(deskPicks);
  var n = pickedCount();
  for (var b = 0; b < boxes.length; b++) {
    var copies = boxes[b].parentNode.querySelectorAll('input[type="hidden"][form="desk-form"]');
    for (var c = 0; c < copies.length; c++) {
      copies[c].disabled = !boxes[b].checked;
    }
  }
  var count = document.getElementById("desk-count");
  if (count) {
    count.textContent = n + " selected";
  }
  var all = document.getElementById("desk-all");
  if (all) {
    all.checked = boxes.length > 0 && n === boxes.length;
    all.indeterminate = n > 0 && n < boxes.length;
  }
  syncShortcuts();
}

// pickedCount is the number of checked boxes of the desk.
function pickedCount() {
  return document.querySelectorAll(deskPicks + ":checked").length;
}

// A box of a slip changes the count. The select-all box checks or clears
// every box of the desk first. A mixed select-all box checks every box,
// as a click on it does in the browser.
document.addEventListener("change", function (ev) {
  var box = ev.target;
  if (!box || !box.matches) {
    return;
  }
  if (box.id === "desk-all") {
    var boxes = document.querySelectorAll(deskPicks);
    for (var i = 0; i < boxes.length; i++) {
      boxes[i].checked = box.checked;
    }
  } else if (!box.matches(deskPicks)) {
    return;
  }
  syncPicks();
});

// overCap reports whether the checked boxes are more than one post of the
// bulk button can decide: 10 approvals or 50 denials. The server refuses
// such a post and decides nothing. So app.js shows the same refusal first,
// in the refusal slot of the queue, and does not post. The button carries
// the cap and the words of the server, so the server stays the authority.
function overCap(button) {
  var cap = parseInt(button.getAttribute("data-cap"), 10);
  if (!(cap > 0) || pickedCount() <= cap) {
    return false;
  }
  refuse(button.getAttribute("data-cap-note"));
  return true;
}

// htmx asks this listener before each request. An element with
// data-dialog whose request already runs sends nothing and opens no
// dialog, as the drop of hx-sync does. A bulk button over its cap sends
// nothing. An element with data-dialog asks in its dialog first, and the
// dialog sends the request when the viewer confirms. Any other request
// goes on.
document.addEventListener("htmx:confirm", function (ev) {
  var elt = ev.detail.elt;
  if (!elt || !elt.hasAttribute) {
    return;
  }
  if (elt.hasAttribute("data-dialog") && syncBusy(elt)) {
    ev.preventDefault();
    return;
  }
  if (elt.hasAttribute("data-bulk")) {
    syncPicks();
  }
  if (elt.hasAttribute("data-bulk") && overCap(elt)) {
    ev.preventDefault();
    return;
  }
  if (elt.hasAttribute("data-dialog") && askFirst(elt, ev.detail.issueRequest)) {
    ev.preventDefault();
  }
});

// asking is the request that an open dialog holds back: the element that
// sends it, the kind of dialog, the request names that the dialog shows,
// and the issueRequest function of htmx. lastDeny is the last denial that
// a dialog sent, with its reason, so a refused reason opens the dialog
// again with the same text.
var asking = null;
var lastDeny = null;

// dialogOpen reports whether any dialog of the page is open. A dialog
// opens only when no other one is, so two dialogs never stack and never
// share asking.
function dialogOpen() {
  return !!document.querySelector("dialog[open]");
}

// syncBusy reports whether a request already runs for the hx-sync element
// of elt. The bulk buttons share the desk form, and a slip form or a
// Close form syncs on itself. A running request keeps aria-busy on its
// button. htmx would drop a second request without a word, so app.js
// checks first.
function syncBusy(elt) {
  if (elt.hasAttribute("data-bulk")) {
    return !!document.querySelector('[form="desk-form"][aria-busy="true"]');
  }
  return !!(elt.querySelector && elt.querySelector('[aria-busy="true"]'));
}

// busyNote is the note when a dialog cannot send its request, because
// another request of the same form still runs.
var busyNote = "Another decision is still being sent. Wait for it, then try again.";

// hiddenOf returns the name and the value of each hidden field of the
// form of elt, except the reason. The deny dialog of a slip notes them
// when it opens. A poll can change them while the dialog is open, so
// confirmAsk puts them in the form only while htmx reads the post. The
// post then carries the version that the viewer saw, and the server
// refuses it as changed. The form then holds the live values again, so a
// later dialog notes what the slip shows.
function hiddenOf(elt) {
  var form = elt.tagName === "FORM" ? elt : elt.form;
  if (!form) {
    return [];
  }
  var fields = form.querySelectorAll('input[type="hidden"]:not([name="reason"])');
  var out = [];
  for (var i = 0; i < fields.length; i++) {
    out.push([fields[i].name, fields[i].value]);
  }
  return out;
}

// putHidden writes the noted values of hiddenOf back into the form of elt.
// It matches each field by its name, and builds no selector from a name.
function putHidden(elt, noted) {
  var form = elt.tagName === "FORM" ? elt : elt.form;
  var fields = form ? form.querySelectorAll('input[type="hidden"]:not([name="reason"])') : [];
  for (var i = 0; i < fields.length; i++) {
    for (var j = 0; j < noted.length; j++) {
      if (noted[j][0] === fields[i].name) {
        fields[i].value = noted[j][1];
      }
    }
  }
}

// rowOf is the id of the slip or the fleet row that holds elt, or "" for
// a bulk button. A dialog notes it, because a poll can take elt away.
function rowOf(elt) {
  var row = elt.closest ? elt.closest(slipRows + ", " + fleetRows) : null;
  return row ? row.id : "";
}

// firstWaiting is the control that takes the focus when the focused
// control left with no place of its own: the name of the first slip that
// waits, or else the queue heading.
function firstWaiting() {
  var target = null;
  var rows = document.querySelectorAll(slipRows);
  for (var i = 0; !target && i < rows.length; i++) {
    target = controlIn(rows[i], "button.slip-name, .slip-title[tabindex]");
  }
  return target || document.getElementById("pending-title");
}

// pickedNames are the names of the checked slips, in the order of the
// desk. The value of a box is the name, a space and the version.
function pickedNames() {
  var boxes = document.querySelectorAll(deskPicks + ":checked");
  var names = [];
  for (var i = 0; i < boxes.length; i++) {
    names.push(boxes[i].value.split(" ")[0]);
  }
  return names;
}

// askNames are the names of the requests that elt decides: the checked
// slips for a bulk button, or the request of a slip form.
function askNames(elt) {
  if (elt.hasAttribute("data-bulk")) {
    return pickedNames();
  }
  var name = elt.getAttribute("data-request");
  return name ? [name] : [];
}

// askFirst opens the dialog of elt and reports whether it did. Then the
// request waits for the dialog. The Close button of a fleet row always
// asks. A bulk button with no checked slip posts,
// so the server refuses it with its own words. Approve selected asks only
// for two or more slips, because one slip approves at once. A page with
// no such dialog posts too, and the server stays the authority.
function askFirst(elt, issue) {
  var kind = elt.getAttribute("data-dialog");
  if (kind === "close") {
    return openClose(elt, issue);
  }
  var names = askNames(elt);
  if (!names.length) {
    return false;
  }
  if (kind === "summary" && names.length >= 2) {
    return openSummary(elt, names, issue);
  }
  if (kind === "deny") {
    return openDeny(elt, names, issue, "", "");
  }
  return false;
}

// listInto fills the list of a dialog with one item for each name. The
// names go in as text, never as markup.
function listInto(list, names) {
  var items = [];
  for (var i = 0; i < names.length; i++) {
    var li = document.createElement("li");
    li.textContent = names[i];
    items.push(li);
  }
  list.replaceChildren.apply(list, items);
}

// openDeny opens the deny dialog for names. text is the reason to show,
// and note is a refusal or a note for the slot of the dialog. The reason
// field takes the focus. It returns false when the page has no deny
// dialog.
function openDeny(elt, names, issue, text, note) {
  var dialog = document.getElementById("deny-dialog");
  var reason = document.getElementById("deny-reason");
  if (!dialog || !reason || dialogOpen()) {
    return false;
  }
  var one = names.length === 1;
  document.getElementById("deny-title").textContent = one ? "Deny " + names[0] + "?" : "Deny " + names.length + " requests?";
  document.getElementById("deny-confirm").textContent = one ? "Deny" : "Deny " + names.length;
  listInto(document.getElementById("deny-list"), names);
  reason.value = text;
  reason.removeAttribute("aria-invalid");
  // The cleanup after an action takes aria-describedby from a marked
  // field, and the dialog is never rendered again, so it goes back here.
  reason.setAttribute("aria-describedby", "deny-help deny-error");
  var slot = document.getElementById("deny-error");
  if (note) {
    var span = document.createElement("span");
    span.textContent = note;
    slot.replaceChildren(alertIcon(), span);
  } else {
    slot.replaceChildren();
  }
  asking = { elt: elt, kind: "deny", names: names, issue: issue, row: rowOf(elt) };
  if (!elt.hasAttribute("data-bulk")) {
    asking.hidden = hiddenOf(elt);
  }
  dialog.returnValue = "";
  dialog.showModal();
  reason.focus();
  return true;
}

// openSummary opens the summary of a bulk approval. Each line holds the
// name and the facts that the box of its slip carries, as the slip shows
// them. Cancel takes the focus, because the safe button is the default.
function openSummary(elt, names, issue) {
  var dialog = document.getElementById("summary-dialog");
  if (!dialog || dialogOpen()) {
    return false;
  }
  var boxes = document.querySelectorAll(deskPicks + ":checked");
  var items = [];
  for (var i = 0; i < boxes.length; i++) {
    items.push(summaryLine(boxes[i]));
  }
  var list = document.getElementById("summary-list");
  list.replaceChildren.apply(list, items);
  document.getElementById("summary-title").textContent = "Approve " + names.length + " requests?";
  document.getElementById("summary-word").textContent = "Approve " + names.length;
  asking = { elt: elt, kind: "summary", names: names, issue: issue };
  dialog.returnValue = "";
  dialog.showModal();
  var cancel = dialog.querySelector('button[value="cancel"]');
  if (cancel) {
    cancel.focus();
  }
  return true;
}

// openClose opens the close dialog for the Close form elt. The title
// names the account, and one line names its AWS account ID and its owner,
// from the data attributes of the form. Every part goes in as text, never
// as markup. Cancel takes the focus, because the safe button is the
// default. It returns false when the page has no close dialog.
function openClose(elt, issue) {
  var dialog = document.getElementById("close-dialog");
  var name = elt.getAttribute("data-name");
  if (!dialog || dialogOpen() || !name) {
    return false;
  }
  document.getElementById("close-title").textContent = "Close account " + name + "?";
  document.getElementById("close-who").textContent = "AWS account " + elt.getAttribute("data-account") + ", owned by " + elt.getAttribute("data-owner") + ".";
  asking = { elt: elt, kind: "close", names: [name], issue: issue, row: rowOf(elt) };
  dialog.returnValue = "";
  dialog.showModal();
  var cancel = dialog.querySelector('button[value="cancel"]');
  if (cancel) {
    cancel.focus();
  }
  return true;
}

// summaryLine is the line of one checked box in the summary: the name,
// then the owner, the lifetime and the budget, then the check of the
// per-owner limit. Every part goes in as text, never as markup.
function summaryLine(box) {
  var li = document.createElement("li");
  var name = document.createElement("span");
  name.className = "dialog-name";
  name.textContent = box.value.split(" ")[0];
  var facts = document.createElement("span");
  facts.className = "dialog-meta";
  facts.textContent = [box.getAttribute("data-owner"), box.getAttribute("data-lifetime"), box.getAttribute("data-budget")].join(" \u00b7 ");
  var limits = document.createElement("span");
  limits.className = "dialog-meta";
  limits.textContent = box.getAttribute("data-limits") || "";
  li.append(name, facts, limits);
  return li;
}

// sameNames reports whether two lists of names are equal, in order.
function sameNames(a, b) {
  if (a.length !== b.length) {
    return false;
  }
  for (var i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) {
      return false;
    }
  }
  return true;
}

// selectionChanged is the note of a dialog that opens again because a
// live update cleared a box while it was open.
var selectionChanged = "A request changed while this dialog was open, so it left the selection. Check the requests again.";

// closeGone is the note of a close dialog whose Close form a live update
// took away, because the account no longer is active or expiring.
function closeGone(name) {
  return "Account " + name + " can no longer be closed, so nothing was sent.";
}

// cannotDecide is the note of a deny dialog whose slip still waits but
// lost its Deny button, for example because the viewer now owns it.
function cannotDecide(name) {
  return name + " still waits, but you can no longer decide it, so nothing was sent.";
}

// noneLeft is the note of a bulk dialog whose button a live update took
// away, because no request that the viewer can decide still waits.
var noneLeft = "None of the selected requests is still waiting, so nothing was sent.";

// askAgain opens the dialog of ask again for the selection names, which a
// live update made smaller, and reports whether it did. A denial keeps the
// reason that the viewer wrote. A summary of fewer than two requests does
// not open, and the post then decides only requests that the viewer saw
// in the summary. With no selection left, the post goes too, and the
// server refuses it with its own words.
function askAgain(ask, names, text) {
  if (!names.length) {
    return false;
  }
  if (ask.kind === "deny") {
    return openDeny(ask.elt, names, ask.issue, text, selectionChanged);
  }
  return names.length >= 2 && openSummary(ask.elt, names, ask.issue);
}

// confirmAsk sends the request that the dialog held back, after the
// dialog closed. A live update can change the selection while a dialog is
// open: a request that changed clears its box. The selection must then be
// the one that the dialog showed, or the dialog opens again with the new
// selection, so a decision never acts on requests that the viewer did not
// see. A slip form posts the hidden values that it had when the dialog
// opened, so a request that changed is refused as changed. A
// request that still runs for the same form would make htmx drop this
// one, so the dialog says so instead: the deny dialog opens again with
// the reason, and any other dialog writes the note into the region. A
// denial copies the reason into the hidden reason field of the form that
// posts: the slip form, or the desk form of a bulk button.
function confirmAsk(ask, text) {
  var elt = ask.elt;
  var now = elt.hasAttribute("data-bulk") ? pickedNames() : ask.names;
  if (!sameNames(ask.names, now) && askAgain(ask, now, text)) {
    return;
  }
  if (syncBusy(elt)) {
    if (ask.kind === "deny" && openDeny(elt, ask.names, ask.issue, text, busyNote)) {
      asking.hidden = ask.hidden;
    } else if (ask.kind !== "deny") {
      say(busyNote);
    }
    return;
  }
  var live = ask.hidden ? hiddenOf(elt) : null;
  if (live) {
    putHidden(elt, ask.hidden);
  }
  if (elt.hasAttribute("data-bulk")) {
    syncPicks();
  }
  if (ask.kind === "deny") {
    var form = elt.tagName === "FORM" ? elt : elt.form;
    var field = form && form.elements ? form.elements.namedItem("reason") : null;
    if (field) {
      field.value = text;
    }
    lastDeny = { elt: elt, names: ask.names, issue: ask.issue, text: text, hidden: ask.hidden };
  }
  // htmx reads the form while issue runs, and syncBusy made sure that no
  // request holds this one in a queue.
  ask.issue(true);
  if (live) {
    putHidden(elt, live);
  }
}

// The forms of the dialogs use method="dialog". A Cancel button skips the
// checks of the form and closes the dialog, and so does Escape. The final
// button submits only when the checks pass, so an empty reason never
// leaves the deny dialog. The dialog closes before the request starts, so
// the focus is back on the button that opened it. A poll can take that
// button away while the dialog is open: someone else decided the
// request, the viewer can no longer decide it, no slip is left to select,
// or the account began to close. The focus then goes to the slip or the
// row of the button when it still shows a control: the name of a slip
// that waits, or the account link of a fleet row or a loan card. Else it
// goes to the name of the first slip that waits, or to the heading of the
// section. On confirm the region says why nothing was sent.
document.addEventListener("submit", function (ev) {
  var form = ev.target;
  var dialog = form && form.closest ? form.closest("dialog") : null;
  if (!dialog || !asking || form.getAttribute("method") !== "dialog") {
    return;
  }
  ev.preventDefault();
  var ask = asking;
  asking = null;
  var confirmed = ev.submitter && ev.submitter.value === "confirm";
  var reason = document.getElementById("deny-reason");
  dialog.close(confirmed ? "confirm" : "cancel");
  if (!document.body.contains(ask.elt)) {
    var close = ask.kind === "close";
    var row = ask.row ? document.getElementById(ask.row) : null;
    var here = controlIn(row, close ? "td a, .loan-name a" : "button.slip-name, .slip-title[tabindex]");
    var target = here || (close ? document.getElementById("accounts-title") : firstWaiting());
    if (target) {
      target.focus();
    }
    if (confirmed) {
      say(close ? closeGone(ask.names[0])
        : ask.elt.hasAttribute("data-bulk") ? noneLeft
          : here ? cannotDecide(ask.names[0]) : goneText(ask.names[0]));
    }
    return;
  }
  if (confirmed) {
    confirmAsk(ask, ask.kind === "deny" && reason ? reason.value : "");
  }
});

// The server refuses a reason that does not pass its rule, names the
// reason field and writes its refusal into the refusal slot of the queue.
// The deny dialog then opens again with the text and that refusal, so the
// viewer fixes the reason where they wrote it, and the queue slot clears.
// htmx swaps the answer before this event, so the slot holds the refusal
// already.
document.addEventListener("htmx:afterRequest", function (ev) {
  var d = ev.detail;
  var ask = lastDeny;
  if (!ask || !d.xhr || !d.requestConfig || d.requestConfig.elt !== ask.elt) {
    return;
  }
  lastDeny = null;
  if (d.xhr.getResponseHeader("X-Invalid-Field") !== "reason") {
    return;
  }
  var slot = document.getElementById("pending-error");
  var refusal = slot ? slot.textContent.trim() : "";
  if (!document.body.contains(ask.elt)) {
    return;
  }
  if (openDeny(ask.elt, ask.names, ask.issue, ask.text, refusal)) {
    asking.hidden = ask.hidden;
    document.getElementById("deny-reason").setAttribute("aria-invalid", "true");
    // The dialog shows the refusal now, so the queue does not show it
    // twice. A dialog that does not open leaves it in the queue.
    if (slot) {
      slot.replaceChildren();
    }
  }
});

// openState reports whether the attribute name of node holds the open
// state of a slip. That is aria-expanded on its name button, or hidden
// on its details or its tools.
function openState(name, node) {
  return (name === "aria-expanded" && node.classList.contains("slip-name")) ||
    (name === "hidden" && (node.classList.contains("slip-details") || node.classList.contains("slip-tools")));
}

// The morph swap, for hx-swap="morph" and hx-swap-oob="morph". It lives here
// and not in idiomorph-ext.min.js for three reasons. Only "morph" is an
// inline swap here, so innerHTML and outerHTML out-of-band parts keep their
// plain htmx meaning. No swap style ever reaches the Function constructor,
// which the CSP blocks. And the options are plain values, so nothing needs
// eval.
htmx.defineExtension("morph", {
  isInlineSwap: function (style) {
    return style === "morph";
  },
  handleSwap: function (style, target, fragment) {
    if (style !== "morph") {
      return false;
    }
    // An error here must not reach htmx. htmx then falls back to an
    // innerHTML swap, which nests the new section inside the old one.
    try {
      if (!fragment.children.length) {
        return [];
      }
      return Idiomorph.morph(target, fragment.children, {
        morphStyle: "outerHTML",
        callbacks: {
          // idiomorph also calls these for text nodes.
          beforeNodeMorphed: function (node, next) {
            if (node.nodeType === 1 && keepNode(node)) {
              return false;
            }
            if (node.nodeType === 1) {
              keepPick(node, next);
            }
            return true;
          },
          // A focused field keeps what the viewer is typing. Only inputs and
          // text areas, because skipping a focused button would also skip
          // its children. The server never renders aria-busy, so a morph
          // while a request runs would clear the busy state of its button.
          beforeAttributeUpdated: function (name, node) {
            if (node.nodeType === 1 && name === "value" && node === document.activeElement &&
                (node.tagName === "INPUT" || node.tagName === "TEXTAREA")) {
              return false;
            }
            if (node.nodeType === 1 && name === "aria-busy") {
              return false;
            }
            // The open state of a slip is the viewer's, and the server does
            // not know it. So the morph keeps it, on an update and on a
            // removal of the attribute.
            if (node.nodeType === 1 && openState(name, node)) {
              return false;
            }
            return true;
          }
        }
      });
    } catch (err) {
      console.error("morph swap failed", err);
      return [];
    }
  }
});

// poller is the element that polls what the page shows, or null on a page
// that does not poll. The server marks it with data-poll, and a page has at
// most one.
function poller() {
  return document.querySelector("[data-poll]");
}

// The 15 s poll sends the hash of what the page shows, and the server
// answers 204 when nothing changed. Only the section's own GET carries it,
// not a control of the fleet view or a form.
document.addEventListener("htmx:configRequest", function (ev) {
  var elt = ev.detail.elt;
  if (ev.detail.verb === "get" && elt && elt.hasAttribute && elt.hasAttribute("data-poll-hash")) {
    ev.detail.headers["X-Poll-Hash"] = elt.getAttribute("data-poll-hash");
  }
});

// A segment, a sort head or Previous and Next of the accounts page asks for
// an address that holds the search of the last answer. The viewer can type
// in the search box while the debounce waits, so the request takes the text
// that is in the box now. The answer then holds that text, and the morph
// keeps it in the box. New text starts again at page 1. navQ keeps the
// search that the click sent, so the beforeRequest listener below can drop
// the debounced search that the click already covered.
var navQ = null;
document.addEventListener("htmx:configRequest", function (ev) {
  var elt = ev.detail.elt;
  var box = document.getElementById("fleet-q");
  if (!box || !elt || !elt.hasAttribute || !elt.hasAttribute("data-fleet-nav")) {
    return;
  }
  var url = new URL(ev.detail.path, location.origin);
  var q = box.value.trim();
  navQ = q;
  if (q === (url.searchParams.get("q") || "")) {
    return;
  }
  if (q) {
    url.searchParams.set("q", q);
  } else {
    url.searchParams.delete("q");
  }
  url.searchParams.delete("page");
  ev.detail.path = url.pathname + url.search;
});

// A sort head sits in the scroll box of the table. When the focus lands on
// a head that is only a little past the edge of the box, the browser does
// not scroll, so the edge cuts its label and its focus ring. With
// "nearest", the box scrolls whenever any part of the head is cut.
document.addEventListener("focusin", function (ev) {
  var b = ev.target;
  if (b instanceof Element && b.matches(".scroll button.sort")) {
    b.scrollIntoView({ block: "nearest", inline: "nearest" });
  }
});

// The morph keeps the accounts section in the page, so a poll answer that
// arrives after an action would put the old state back. Every other
// request, a fleet view control included, counts as an action, and the beforeSwap
// listener drops the answer of a poll that started before the latest one.
// The poll is not aborted, because htmx logs an aborted request as two
// errors. A poll that is due while another request runs waits for the next
// one.
var running = [];
var actionSeq = 0;
function requestRunning() {
  for (var i = 0; i < running.length; i++) {
    if (running[i].readyState > 0 && running[i].readyState < 4) {
      return true;
    }
  }
  return false;
}
// The poll also waits while the viewer paused live updates and while the
// tab is hidden. A page with no poller counts no action.
document.addEventListener("htmx:beforeRequest", function (ev) {
  var poll = poller();
  if (!poll) {
    return;
  }
  if (ev.detail.elt === poll) {
    if (requestRunning() || pausedNow || document.hidden) {
      ev.preventDefault();
    }
    return;
  }
  actionSeq++;
});
// A click on a data-fleet-nav control does not stop the debounce of the
// search box, so the form still sends its search after the click. When the
// form sends because of typing, and the search is the same as the one that
// the click sent, the click already asked for this view. Then the form
// request is dropped. Without this, a click on Next can be followed by a
// search with no page, which goes back to page 1. Any request of the form
// clears navQ, and a search that is really new still goes out.
document.addEventListener("htmx:beforeRequest", function (ev) {
  var form = ev.detail.elt;
  if (!form || form.id !== "fleet-view") {
    return;
  }
  var cfg = ev.detail.requestConfig;
  var t = cfg && cfg.triggeringEvent;
  var box = document.getElementById("fleet-q");
  var q = navQ;
  navQ = null;
  if (t && t.type === "input" && box && q !== null && box.value.trim() === q) {
    ev.preventDefault();
  }
});
// busyButton is the button whose request runs, or null. Edit and Cancel are
// buttons that send a GET. Any other GET comes from the poll or the Show
// closed box and is never busy. A form names the button that submitted it,
// or else its first submit button. Only a button is ever busy.
function busyButton(detail) {
  var cfg = detail.requestConfig;
  var elt = cfg && cfg.elt;
  if (!elt) {
    return null;
  }
  if (elt.tagName === "BUTTON") {
    return elt;
  }
  if (cfg.verb === "get") {
    return null;
  }
  var submitter = cfg.triggeringEvent && cfg.triggeringEvent.submitter;
  if (submitter && submitter.tagName === "BUTTON") {
    return submitter;
  }
  if (elt.tagName === "FORM") {
    return elt.querySelector("button[type=submit]");
  }
  return null;
}

// A busy button keeps its size and its colors, and the stylesheet shows its
// verb in progress. It is never disabled, because hx-sync already drops a
// second click. The request config stays the same through the request, so
// both listeners find the same button.
document.addEventListener("htmx:beforeRequest", function (ev) {
  var button = ev.defaultPrevented ? null : busyButton(ev.detail);
  if (button) {
    button.setAttribute("aria-busy", "true");
  }
});
document.addEventListener("htmx:afterRequest", function (ev) {
  var button = busyButton(ev.detail);
  if (button) {
    button.removeAttribute("aria-busy");
  }
});

// Some answers take the focused control out of the page. Approve, Deny
// and Withdraw decide a request, so its slip turns into a decided slip
// or leaves. Edit opens the edit slip, and Save and Cancel close it
// again. Close turns the account to closing, which takes its buttons
// away. focusNext names, for each action, the control that takes the
// focus then.
var focusNext = {
  approve: 'form[data-action="approve"] button',
  deny: 'form[data-action="deny"] button',
  withdraw: 'form[data-action="withdraw"] button',
  edit: "form[data-editing] input:not([type=hidden])",
  save: 'button[data-action="edit"]',
  cancel: 'button[data-action="edit"]',
  close: "td a, .loan-name a"
};

// The slips of the queue, and the rows of the fleet or the loan cards of
// the home page. The account link of a row or a card is its name.
var slipRows = "#pending li.slip";
var fleetRows = "#accounts tbody > tr, #accounts li.loan";
// The rows of the Needs attention ledger. A poll can take a row away.
var ledgerRows = "#attention li";

// focusPlace is where the focus was when an answer started to swap. It
// holds the slip or the row and its place in the list. It also notes
// whether the viewer acted on it, and the control to look for after the
// swap. said is the last node of the region before the swap.
var focusPlace = null;

// notePlace notes focusPlace for the answer of xhr, from the element
// that has the focus now. source is the element that sent the request.
// The viewer acted on the slip or row only when source is a control of
// focusNext inside it. After any other swap, a poll above all, the focus
// goes to a control that decides nothing. That is the name of a slip,
// the heading of a row of Your requests, or the account link of a fleet
// row or a loan card. It never goes to Approve or Deny, because the viewer
// did not act.
function notePlace(xhr, source) {
  focusPlace = null;
  var active = document.activeElement;
  var region = document.getElementById("notice");
  // A control of the bulk bar has no slip. The bulk controls leave the bar
  // when no slip has a box, and the focus then goes to a slip that waits.
  if (active && active.closest && active.closest("#desk-bar")) {
    focusPlace = {
      xhr: xhr,
      desk: true,
      acted: !!(source && source.hasAttribute && source.hasAttribute("data-bulk")),
      said: region ? region.lastElementChild : null
    };
    return;
  }
  // A ledger row decides nothing, so the viewer never acted on it.
  var line = active && active.closest ? active.closest(ledgerRows) : null;
  if (line && line.id) {
    focusPlace = {
      xhr: xhr,
      row: line.id,
      rows: ledgerRows,
      index: Array.prototype.indexOf.call(line.parentNode.children, line),
      acted: false,
      want: "a.ledger-row",
      said: region ? region.lastElementChild : null
    };
    return;
  }
  var row = active && active.closest ? active.closest(slipRows + ", " + fleetRows) : null;
  if (!row || !row.id) {
    return;
  }
  var action = source && source.getAttribute ? source.getAttribute("data-action") : null;
  var acted = !!action && !!focusNext[action] && row.contains(source);
  var slip = row.matches(slipRows);
  focusPlace = {
    xhr: xhr,
    row: row.id,
    rows: slip ? slipRows : fleetRows,
    index: Array.prototype.indexOf.call(row.parentNode.children, row),
    acted: acted,
    want: acted ? focusNext[action] : (slip ? "button.slip-name, .slip-title[tabindex]" : "td a, .loan-name a"),
    said: region ? region.lastElementChild : null
  };
  // The a and d keys move the focus to the name of the slip before they
  // click, and the Deny dialog gives the focus back to the name. The
  // focus then goes on to the name of the next slip, not to its Approve
  // or Deny button, so Enter after a key never decides another request.
  if (acted && (action === "approve" || action === "deny") && active.matches("button.slip-name")) {
    focusPlace.want = "button.slip-name, .slip-title[tabindex]";
  }
  // A bulk button decides the checked slips, also when the a or d key
  // pressed it from a slip. The region then says its message again after
  // the focus moved.
  if (!acted && source && source.hasAttribute && source.hasAttribute("data-bulk")) {
    focusPlace.acted = true;
  }
}

// controlIn finds the control want in row, or else the first control
// that shows. A control in a closed part does not show. Only a slip that
// waits has controls, so a decided or gone slip gives none. A fleet row
// has no data-state.
function controlIn(row, want) {
  if (!row || (row.hasAttribute("data-state") && row.getAttribute("data-state") !== "pending")) {
    return null;
  }
  var picks = want ? [want, "button, a[href]"] : ["button, a[href]"];
  for (var p = 0; p < picks.length; p++) {
    var found = row.querySelectorAll(picks[p]);
    for (var i = 0; i < found.length; i++) {
      if (!found[i].closest("[hidden]")) {
        return found[i];
      }
    }
  }
  return null;
}

// After a swap, the focus can be lost, or it can sit in a slip that no
// longer waits. It then goes to the control of focusPlace in its own
// slip or row. A slip that no longer waits gives its place to the next
// slip that waits, then to the one before it. In a queue with no slip
// that waits, the focus goes to the heading. A ledger row that left
// gives its place to the next row, then to the one before it, then to
// the Needs attention heading. A bulk control that left gives its place
// to the name of the first slip that waits, or to the heading. A viewer
// who moved on keeps their place. Every swap runs this rule, a poll too.
document.addEventListener("htmx:afterSettle", function (ev) {
  watchBar();
  syncPicks();
  if (!focusPlace || ev.detail.xhr !== focusPlace.xhr) {
    return;
  }
  var was = focusPlace;
  focusPlace = null;
  var active = document.activeElement;
  var ended = active && active.closest && active.closest('li.slip:not([data-state="pending"])');
  if (active && active !== document.body && !ended) {
    return;
  }
  if (was.desk) {
    var first = firstWaiting();
    if (first) {
      first.focus();
      if (was.acted) {
        repeatRegion(was.said);
      }
    }
    return;
  }
  var target = controlIn(document.getElementById(was.row), was.want);
  var rows = document.querySelectorAll(was.rows);
  var start = Math.min(was.index, rows.length - 1);
  for (var i = start; !target && i >= 0 && i < rows.length; i++) {
    target = controlIn(rows[i], was.want);
  }
  for (var j = start - 1; !target && j >= 0; j--) {
    target = controlIn(rows[j], was.want);
  }
  if (!target && was.rows === slipRows) {
    target = document.getElementById("pending-title");
  } else if (!target && was.rows === ledgerRows) {
    target = document.getElementById("attention-title");
  }
  if (!target) {
    return;
  }
  target.focus();
  // A slip that left while the viewer did nothing gets a message, so a
  // screen reader user knows why the focus moved. After an action, the
  // answer wrote the region, and the move of the focus cut it short.
  if (!was.acted && was.rows === slipRows && !document.getElementById(was.row)) {
    say(goneText(was.row.slice(4)));
  } else if (was.acted) {
    repeatRegion(was.said);
  }
});

// repeatRegion writes the message of the region again, once, 300 ms
// after the focus moved. A screen reader stops reading the region when
// the focus moves, so the message then comes after the name of the new
// focus. before is the last node of the region before the swap, so the
// function never repeats a message that this answer did not write. The
// nodes, the text and the look stay the same.
function repeatRegion(before) {
  var region = document.getElementById("notice");
  var last = region ? region.lastElementChild : null;
  if (!last || last === before) {
    return;
  }
  var text = last.textContent;
  setTimeout(function () {
    if (region.lastElementChild === last && last.textContent === text) {
      last.textContent = text;
    }
  }, 300);
}

// The list holds only requests that htmx sends, so a request that another
// listener cancels never blocks the poll. A poll notes the latest action
// it started after.
document.addEventListener("htmx:beforeSend", function (ev) {
  var poll = poller();
  if (!ev.detail.xhr) {
    return;
  }
  if (poll && ev.detail.elt === poll) {
    ev.detail.xhr.pollSeq = actionSeq;
    return;
  }
  running = running.filter(function (xhr) {
    return xhr.readyState > 0 && xhr.readyState < 4;
  });
  running.push(ev.detail.xhr);
});

// barRule holds --bar-h, the height of the bulk bar. Above 640 px the
// bar sticks to the top of the window. The stylesheet keeps that much
// room and 8 px above a focused control. A long message wraps and makes
// the bar taller. The CSP allows no inline style, so app.js adds one rule to
// the end of app.css and changes that rule. A rule is not an inline
// style, and the page keeps no style attribute.
var barRule = null;
function setBarHeight(px) {
  if (!barRule) {
    var link = document.querySelector('link[rel="stylesheet"]');
    var sheet = link && link.sheet;
    if (!sheet) {
      return;
    }
    try {
      barRule = sheet.cssRules[sheet.insertRule(":root {}", sheet.cssRules.length)];
    } catch (err) {
      return;
    }
  }
  barRule.style.setProperty("--bar-h", px + "px");
}

// A morph keeps the bar node, and a page load makes a new one. So
// watchBar looks for the bar on load and after each swap. A message that
// makes the bar taller can cover the focused control, and the browser
// scrolls only when the focus moves. So the observer scrolls the control
// clear of the bar.
var barSeen = null;
var barWatch = typeof ResizeObserver === "function" ? new ResizeObserver(function (entries) {
  var bar = entries[0].target;
  setBarHeight(bar.offsetHeight);
  var active = document.activeElement;
  if (active && active !== document.body && !bar.contains(active)) {
    var a = active.getBoundingClientRect();
    var b = bar.getBoundingClientRect();
    if (a.top < b.bottom && a.bottom > b.top) {
      active.scrollIntoView({ block: "nearest" });
    }
  }
}) : null;
function watchBar() {
  var bar = document.getElementById("desk-bar");
  if (!barWatch || bar === barSeen) {
    return;
  }
  if (barSeen) {
    barWatch.unobserve(barSeen);
  }
  barSeen = bar;
  if (bar) {
    barWatch.observe(bar);
  }
}

// requestName is the pattern of a request name, so a crafted address
// cannot put other text into a message.
var requestName = /^[a-z0-9][a-z0-9-]{1,48}[a-z0-9]$/;

// openFromAddress opens the slip that the address names and moves the
// focus to its name. A link from Slack or a notice is
// /?req=NAME#req-NAME: the query survives sign-in. Without script the
// hash scrolls to the slip. With script the focus does. On a hash change
// only the hash counts, so the Queue link to #pending does not open the
// slip of the query again. A name with no slip that waits gets the focus
// on the heading. A message follows only
// when the page does not say why already. A slip that no longer waits
// shows why, and a queue that did not load says so. On a page visit the
// server writes a bar line for the name. The function returns true when
// the page used the name: it opened the slip, or it said why no slip
// waits. A queue that did not load cannot use the name, so it returns
// false.
function openFromAddress(useQuery) {
  var name = null;
  if (location.hash.indexOf("#req-") === 0) {
    name = location.hash.slice(5);
  } else if (useQuery) {
    name = new URLSearchParams(location.search).get("req");
  }
  if (!name || !requestName.test(name) || !document.getElementById("pending")) {
    return false;
  }
  var slip = document.getElementById("req-" + name);
  if (slip && slip.getAttribute("data-state") === "pending") {
    var button = slip.querySelector("button.slip-name");
    if (button) {
      setOpen(button, true);
      button.focus();
    } else {
      var title = slip.querySelector(".slip-title");
      if (title) {
        title.focus();
      }
    }
    return true;
  }
  var heading = document.getElementById("pending-title");
  if (heading) {
    heading.focus();
  }
  if (document.getElementById("pending-failed")) {
    return false;
  }
  if (!slip && !document.querySelector('#pending-error [data-name="' + name + '"]')) {
    say(document.querySelector("#pending.desk") ? goneText(name)
      : name + " is not in Your requests. It is not yours, or it is no longer waiting.");
  }
  return true;
}

// goneText is the sentence of the server for a request that no longer
// waits (refusal.go).
function goneText(name) {
  return name + " is no longer waiting. Someone else decided it, the owner withdrew it, or playplace dropped it.";
}

// say writes a message into the live region with the alert icon, as an
// answer of the server does. The text goes in as text, never as markup.
function say(text) {
  var region = document.getElementById("notice");
  if (!region) {
    return;
  }
  var svg = alertIcon();
  var span = document.createElement("span");
  span.textContent = text;
  region.replaceChildren(svg, span);
}

// refuse shows a refusal as a refused form of the server does: the alert
// icon and the text in the refusal slot of the queue, and a visually
// hidden copy in the live region. The text goes in as text, never as
// markup.
function refuse(text) {
  var slot = document.getElementById("pending-error");
  if (slot) {
    var shown = document.createElement("span");
    shown.textContent = text;
    slot.replaceChildren(alertIcon(), shown);
  }
  var region = document.getElementById("notice");
  if (region) {
    var copy = document.createElement("span");
    copy.className = "vh";
    copy.textContent = text;
    region.replaceChildren(copy);
  }
}

// alertIcon is the alert icon of the sprite, as the server draws it.
function alertIcon() {
  var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "i");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  var use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", "#i-alert");
  svg.appendChild(use);
  return svg;
}

// forgetAddress drops the request name from the address. The listeners
// call it only when openFromAddress used the name. A reload after a
// decision then shows the queue, and not a line that says someone else
// decided the request. Without the hash, the focus also stays on the
// name. After the DOMContentLoaded listeners, the browser goes to the
// hash of a new page. The slip cannot take the focus, so that step clears
// the focus. A queue that did not load leaves the name in the address, so
// a reload opens the slip when the queue is back.
function forgetAddress() {
  var url = new URL(location.href);
  var hash = url.hash.indexOf("#req-") === 0;
  if (!hash && !url.searchParams.has("req")) {
    return;
  }
  url.searchParams.delete("req");
  if (hash) {
    url.hash = "";
  }
  history.replaceState(history.state, "", url.pathname + url.search + url.hash);
}

document.addEventListener("DOMContentLoaded", function () {
  readKeys();
  watchBar();
  syncPicks();
  var poll = poller();
  if (poll && poll.querySelector("[data-stale]")) {
    updatedAt = null;
  }
  liveTick();
  if (openFromAddress(true)) {
    forgetAddress();
  }
});
window.addEventListener("hashchange", function () {
  if (openFromAddress(false)) {
    forgetAddress();
  }
});

// The live line in the footer of a page that polls: the dot, the time of
// the last update and the Pause button. updatedAt is the time of the last
// poll that the server answered with fresh data, 200 or 204. A poll that
// failed does not reset it, so the time says how old the data is. It is
// null while the poller shows no fresh data since the page loaded.
var updatedAt = Date.now();
var liveTimer = null;

// pauseKey holds the pause in sessionStorage, so it lasts for the tab and
// survives a reload. pausedNow is the state itself, so Pause still works
// when the storage throws, as in some private windows.
var pauseKey = "playplace.paused";
var pausedNow = false;

// readPaused reads the pause of the tab from the storage and puts the
// class on <html>. The stylesheet hides the dot under the class and shows
// the recovery of an unavailable list that fits a paused page. When the
// storage throws, the state in memory stays.
function readPaused() {
  try {
    pausedNow = window.sessionStorage.getItem(pauseKey) === "1";
  } catch (err) {
    // Keep the state in memory.
  }
  document.documentElement.classList.toggle("poll-paused", pausedNow);
}
// The class is on <html> before the first paint.
readPaused();

// setPaused pauses or resumes live updates for this tab. Every read and
// write of the storage sits in try/catch.
function setPaused(on) {
  pausedNow = on;
  try {
    if (on) {
      window.sessionStorage.setItem(pauseKey, "1");
    } else {
      window.sessionStorage.removeItem(pauseKey);
    }
  } catch (err) {
    // The pause then lasts only until the page reloads.
  }
  document.documentElement.classList.toggle("poll-paused", on);
  liveTick();
}

// agoText says how long ago ms milliseconds is, in plain words.
function agoText(ms) {
  var s = Math.floor(ms / 1000);
  if (s < 5) {
    return "just now";
  }
  if (s < 60) {
    return s + " seconds ago";
  }
  var m = Math.floor(s / 60);
  if (m < 60) {
    return m === 1 ? "1 minute ago" : m + " minutes ago";
  }
  var h = Math.floor(m / 60);
  return h === 1 ? "1 hour ago" : h + " hours ago";
}

// liveTick writes the live line and the word of the Pause button. In the
// first minute the time ticks each second, and after it each minute, on
// the turn of the second or the minute.
function liveTick() {
  clearTimeout(liveTimer);
  liveTimer = null;
  var line = document.getElementById("live-ago");
  if (!line) {
    return;
  }
  var button = document.getElementById("live-pause");
  if (button) {
    button.textContent = pausedNow ? "Resume updates" : "Pause updates";
  }
  var lead = pausedNow ? "Paused. " : "";
  if (updatedAt === null) {
    line.textContent = lead + "Not updated yet";
    return;
  }
  var ms = Math.max(0, Date.now() - updatedAt);
  line.textContent = lead + "Updated " + agoText(ms);
  var step = ms < 60000 ? 1000 : 60000;
  liveTimer = setTimeout(liveTick, step - (ms % step));
}

// pollNow makes the poller poll at once, unless the viewer paused live
// updates or the tab is hidden. The beforeRequest guard still drops it
// while another request runs, and the next tick polls.
function pollNow() {
  var poll = poller();
  if (poll && !pausedNow && !document.hidden) {
    htmx.trigger(poll, "poll-now");
  }
}

// blinkDot hides the dot for one 90 ms step. The class comes off and on
// again, so the next blink starts over. Under prefers-reduced-motion the
// stylesheet gives it no animation.
function blinkDot() {
  var dot = document.querySelector(".live-dot");
  if (!dot) {
    return;
  }
  dot.classList.remove("blink");
  void dot.offsetWidth;
  dot.classList.add("blink");
}

// tell writes a short message into the live region, as text, with no icon.
function tell(text) {
  var region = document.getElementById("notice");
  if (!region) {
    return;
  }
  var span = document.createElement("span");
  span.textContent = text;
  region.replaceChildren(span);
}

// A poll that the server answered, 200 or 204, resets the Updated time,
// unless the poller shows no fresh data: an unavailable list carries
// data-stale. The beforeSwap listener drops the answer of a poll that
// started before the latest action. That answer does not reset the time,
// because the page still shows the data from before that poll. The dot
// blinks only when new data arrived: a 200 that the page swapped.
document.addEventListener("htmx:afterRequest", function (ev) {
  var d = ev.detail;
  var poll = poller();
  if (!poll || d.elt !== poll || !d.xhr || (d.xhr.status !== 200 && d.xhr.status !== 204)) {
    return;
  }
  if (poll.querySelector("[data-stale]")) {
    liveTick();
    return;
  }
  if (d.xhr.pollSeq !== actionSeq) {
    return;
  }
  updatedAt = Date.now();
  liveTick();
  if (d.xhr.status === 200) {
    blinkDot();
  }
});

// An action answer, such as Sync, +7d or a filter, can bring a new
// accounts section. On the home page it comes out of band; on the accounts
// page it is the main swap target. Fresh data resets the Updated time, also
// on a paused page. An unavailable list keeps the last good time, as a
// failed poll does. The morph keeps the node, but the id check holds either
// way. The poll's own answer is left to the afterRequest listener above.
function actionRefreshed(target, source) {
  var poll = poller();
  if (!poll || !target || target.id !== poll.id || source === poll) {
    return;
  }
  if (!poll.querySelector("[data-stale]")) {
    updatedAt = Date.now();
  }
  liveTick();
}

document.addEventListener("htmx:oobAfterSwap", function (ev) {
  actionRefreshed(ev.detail && ev.detail.target, null);
});

document.addEventListener("htmx:afterSwap", function (ev) {
  actionRefreshed(ev.detail && ev.detail.target, ev.detail && ev.detail.elt);
});

// Pause stops the poll, and Resume polls at once. Either press writes one
// short message into the live region. Actions still answer and update the
// page while live updates are paused.
document.addEventListener("click", function (ev) {
  var button = ev.target && ev.target.closest && ev.target.closest("#live-pause");
  if (!button) {
    return;
  }
  setPaused(!pausedNow);
  tell(pausedNow ? "Live updates paused." : "Live updates on.");
  pollNow();
});

// A hidden tab does not poll. When it shows again, the line catches up,
// because the browser slows timers in a hidden tab, and the page polls
// once at once, unless the viewer paused live updates. The pause is read
// again first, because a page that Back restores from the back/forward
// cache gets this event before pageshow, and another page of the tab may
// have paused since.
document.addEventListener("visibilitychange", function () {
  if (!document.hidden) {
    readPaused();
    liveTick();
    pollNow();
  }
});

// Back or Forward can show a page from the back/forward cache with its
// old state. Another page of the tab may have paused or resumed since, so
// the page reads the stored pause again, then catches up like a shown tab.
window.addEventListener("pageshow", function (ev) {
  if (!ev.persisted) {
    return;
  }
  readKeys();
  readPaused();
  liveTick();
  pollNow();
});
