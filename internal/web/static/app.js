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

// The element that sent the request whose answer htmx is about to swap. In
// htmx:beforeSwap, detail.elt is the target, so the source comes from the
// request config. The keep rules below read it.
var swapSource = null;
document.addEventListener("htmx:beforeSwap", function (ev) {
  swapSource = (ev.detail.requestConfig && ev.detail.requestConfig.elt) || null;
  // htmx does not swap a 4xx answer. A 403 refusal carries HX-Reswap: none
  // and only out-of-band parts, the slot and the region, so the page can
  // take them. It is then not an error, and htmx logs nothing. X-Form-Error
  // still keeps the form as the viewer filled it.
  var xhr = ev.detail.xhr;
  if (xhr && xhr.status === 403 && xhr.getResponseHeader("HX-Reswap") === "none") {
    ev.detail.shouldSwap = true;
    ev.detail.isError = false;
  }
});

// keepNode reports whether a morph leaves this node as it is. An open edit
// row stays through every answer but the one to its own form, so a poll or
// another row's action never drops what the admin typed. A filled refusal in
// the pending card stays through a poll, so the viewer can still read it.
// The rules use ids, not classes, so a node that idiomorph pairs by shape
// is never kept by mistake.
function keepNode(node) {
  if (node.tagName === "TR" && node.querySelector("form[data-editing]")) {
    return !(swapSource && node.contains(swapSource));
  }
  if (node.id === "pending-error" && node.firstChild) {
    return !!swapSource && swapSource.id === "accounts";
  }
  return false;
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
// arrives after an action would put the old state back. Any other request,
// Show closed included, stops the poll in flight. A poll that is due while
// another request runs waits for the next one.
var running = [];
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
  htmx.trigger(accounts, "htmx:abort");
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

// A decision takes its row out of the pending card, and the focused button
// leaves with it. The focus then goes to the same button on the row that
// takes its place, or on the row before when the row was the last. In an
// empty card it goes to the heading. This happens only when the focus is
// lost, so a viewer who moved on keeps their place.
var leaving = null;
document.addEventListener("htmx:beforeRequest", function (ev) {
  var form = ev.detail.requestConfig && ev.detail.requestConfig.elt;
  var action = form && form.getAttribute && form.getAttribute("data-action");
  var row = action ? form.closest("#pending tbody > tr") : null;
  if (ev.defaultPrevented || !row || !row.contains(document.activeElement)) {
    return;
  }
  leaving = {xhr: ev.detail.xhr, action: action, index: Array.prototype.indexOf.call(row.parentNode.children, row)};
});
document.addEventListener("htmx:afterSettle", function (ev) {
  if (!leaving || ev.detail.xhr !== leaving.xhr) {
    return;
  }
  var was = leaving;
  leaving = null;
  if (document.activeElement && document.activeElement !== document.body) {
    return;
  }
  var rows = document.querySelectorAll("#pending tbody > tr");
  var row = rows.length ? rows[Math.min(was.index, rows.length - 1)] : null;
  var button = row && (row.querySelector('form[data-action="' + was.action + '"] button') || row.querySelector("button"));
  var target = button || document.getElementById("pending-title");
  if (target) {
    target.focus();
  }
});

// The list holds only requests that htmx sends, so a request that another
// listener cancels never blocks the poll.
document.addEventListener("htmx:beforeSend", function (ev) {
  var accounts = document.getElementById("accounts");
  if (!ev.detail.xhr || (accounts && ev.detail.elt === accounts)) {
    return;
  }
  running = running.filter(function (xhr) {
    return xhr.readyState > 0 && xhr.readyState < 4;
  });
  running.push(ev.detail.xhr);
});
