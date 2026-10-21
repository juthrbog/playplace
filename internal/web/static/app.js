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

// After an action, the refusal slot under a form and the marked fields show
// only what the last answer said. A refusal fills its own slot and can name
// the field it is about. That field gets aria-invalid and points to the
// slot, so a screen reader reads the refusal with the field. The poll and
// other GETs leave both alone.
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
    var err = document.getElementById("form-error");
    if (err) {
      err.textContent = "";
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

// Escape closes the menu and gives focus back to its toggle.
document.addEventListener("keydown", function (ev) {
  if (ev.key !== "Escape") {
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
    return !!swapSource && swapSource.id === "accounts";
  }
  return false;
}

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
          beforeNodeMorphed: function (node) {
            if (node.nodeType === 1 && keepNode(node)) {
              return false;
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

// The 15 s poll sends the hash of what the page shows, and the server
// answers 204 when nothing changed. Only the section's own GET carries it,
// not the Show closed box or a form.
document.addEventListener("htmx:configRequest", function (ev) {
  var elt = ev.detail.elt;
  if (ev.detail.verb === "get" && elt && elt.hasAttribute && elt.hasAttribute("data-poll-hash")) {
    ev.detail.headers["X-Poll-Hash"] = elt.getAttribute("data-poll-hash");
  }
});

// The morph keeps the accounts section in the page, so a poll answer that
// arrives after an action would put the old state back. Every other
// request, Show closed included, counts as an action, and the beforeSwap
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
document.addEventListener("htmx:beforeRequest", function (ev) {
  var accounts = document.getElementById("accounts");
  if (!accounts) {
    return;
  }
  if (ev.detail.elt === accounts) {
    if (requestRunning()) {
      ev.preventDefault();
    }
    return;
  }
  actionSeq++;
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
  close: "td a"
};

// The slips of the queue and the rows of the fleet.
var slipRows = "#pending li.slip";
var fleetRows = "#accounts tbody > tr";

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
// row. It never goes to Approve or Deny, because the viewer did not act.
function notePlace(xhr, source) {
  focusPlace = null;
  var active = document.activeElement;
  var row = active && active.closest ? active.closest(slipRows + ", " + fleetRows) : null;
  if (!row || !row.id) {
    return;
  }
  var action = source && source.getAttribute ? source.getAttribute("data-action") : null;
  var acted = !!action && !!focusNext[action] && row.contains(source);
  var slip = row.matches(slipRows);
  var region = document.getElementById("notice");
  focusPlace = {
    xhr: xhr,
    row: row.id,
    rows: slip ? slipRows : fleetRows,
    index: Array.prototype.indexOf.call(row.parentNode.children, row),
    acted: acted,
    want: acted ? focusNext[action] : (slip ? "button.slip-name, .slip-title[tabindex]" : "td a"),
    said: region ? region.lastElementChild : null
  };
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
// that waits, the focus goes to the heading. A viewer who moved on keeps
// their place. Every swap runs this rule, a poll too.
document.addEventListener("htmx:afterSettle", function (ev) {
  watchBar();
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
  var accounts = document.getElementById("accounts");
  if (!ev.detail.xhr) {
    return;
  }
  if (accounts && ev.detail.elt === accounts) {
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
// server writes a bar line for the name.
function openFromAddress(useQuery) {
  var name = null;
  if (location.hash.indexOf("#req-") === 0) {
    name = location.hash.slice(5);
  } else if (useQuery) {
    name = new URLSearchParams(location.search).get("req");
  }
  if (!name || !requestName.test(name) || !document.getElementById("pending")) {
    return;
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
    return;
  }
  var heading = document.getElementById("pending-title");
  if (heading) {
    heading.focus();
  }
  if (slip || document.getElementById("pending-failed") ||
      document.querySelector('#pending-error [data-name="' + name + '"]')) {
    return;
  }
  say(document.querySelector("#pending.desk") ? goneText(name)
    : name + " is not in Your requests. It is not yours, or it is no longer waiting.");
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
  var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "i");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("focusable", "false");
  var use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", "#i-alert");
  svg.appendChild(use);
  var span = document.createElement("span");
  span.textContent = text;
  region.replaceChildren(svg, span);
}

// forgetAddress drops the request name from the address once the page
// has used it. A reload after a decision then shows the queue, and not a
// line that says someone else decided the request. Without the hash, the
// focus also stays on the name. After the DOMContentLoaded listeners, the
// browser goes to the hash of a new page. The slip cannot take the focus,
// so that step clears the focus.
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
  watchBar();
  openFromAddress(true);
  forgetAddress();
});
window.addEventListener("hashchange", function () {
  openFromAddress(false);
  forgetAddress();
});
