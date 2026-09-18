// Small page behaviours. Kept out of inline attributes so the page can run
// under a Content-Security-Policy without unsafe-inline or unsafe-eval.

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
