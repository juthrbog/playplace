// Small page behaviours. Kept out of inline attributes so the page can run
// under a Content-Security-Policy without unsafe-inline or unsafe-eval.

// This file loads in <head> without defer, so the class is on <html> before
// the first paint. The stylesheet hides the phone menu only under it, so a
// page without script keeps every shell item in view.
document.documentElement.classList.add("js");

// After a form with data-reset-on-success submits through htmx and the
// server did not flag a validation error, clear it and its error slot.
document.addEventListener("htmx:afterRequest", function (ev) {
  var form = ev.target && ev.target.closest && ev.target.closest("form[data-reset-on-success]");
  if (!form || !ev.detail.successful) {
    return;
  }
  if (ev.detail.xhr && ev.detail.xhr.getResponseHeader("X-Form-Error")) {
    return;
  }
  form.reset();
  var err = document.getElementById("form-error");
  if (err) {
    err.textContent = "";
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
          // its children.
          beforeAttributeUpdated: function (name, node) {
            if (node.nodeType === 1 && name === "value" && node === document.activeElement &&
                (node.tagName === "INPUT" || node.tagName === "TEXTAREA")) {
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
