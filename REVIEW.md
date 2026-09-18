# Review findings

Two passes so far, both on 2026-09-17. Everything is fixed except the items
under "Open, accepted for now" in each pass. Line numbers are from the
reviews and may have drifted.

## Second pass

Fresh review of the whole tree after the first pass landed: core, web, AWS
provider, audit sinks, worker, CLI, TUI, and the deploy files.

### Fixed

- [x] **No cross-origin check on mutating POSTs.** Every state change is a
  form POST guarded only by a SameSite=Lax cookie, and NoAuth mode has no
  cookie at all, so a page on another origin could submit an approve or
  close for a signed-in browser (or anyone on the network, with NoAuth).
  `Handler` now wraps the stack in `net/http`'s `CrossOriginProtection`;
  `/slack/` is bypassed because Slack signs its own calls, and `--base-url`
  is a trusted origin for proxies that rewrite `Host` (tests).
- [x] **A malformed request tag panicked the decoder.** `splitFields` ignored
  the `Atoi` error on a length prefix, so a prefix past `int` sliced with a
  negative bound. In `serve` that crash ran on the worker goroutine and would
  have taken the process down. The prefix is checked, and the worker now
  recovers and logs a panic in a pass instead of exiting (test).
- [x] **Budgets could be `NaN`.** `ParseFloat` and pflag accept `NaN` and
  `Inf`; `NaN` compares false with the ceiling, so it passed `checkLimits`,
  was written to the OU tag, and made approval fail at AWS Budgets. Core
  refuses non-finite budgets in every entry point; the web form, CLI, and TUI
  also refuse `NaN`, `Inf`, and non-positive lifetimes on every parse branch,
  where before only the bare-number branch was checked (tests).
- [x] **Environment variables did not count as "given".** `applyEnv` used
  `Flag.Value.Set`, which does not mark the flag `Changed`, so
  `PLAYPLACE_OVERRIDE_LIMITS=true playplace edit-request` was ignored. It goes
  through `FlagSet.Set` now (test).
- [x] **GitLab pipeline variables could loosen every guardrail.** Any CI
  flag is also a `PLAYPLACE_*` variable, and the Run pipeline form lets the
  runner add variables, so `PLAYPLACE_MAX_BUDGET=0` or
  `PLAYPLACE_ALLOW_SELF_APPROVAL=true` were one text field away, and
  `GITLAB_USER_EMAIL` (lowest precedence) could be forged to defeat the
  self-approval rule. `before_script` unsets the guardrail variables and reads
  the operator from the `user_email` claim of the job's OIDC token, which
  GitLab signs.
- [x] **`--every 30` meant thirty days.** Intervals and waits went through
  the lifetime parser, where a bare number is days. `serve --every` and
  `create --timeout` now require a unit.
- [x] **`show` and `costs` printed `$0.00` after a failed Cost Explorer
  pull.** They print `unavailable`.
- [x] **One torn history line broke every history read.** The file sink now
  skips a line it cannot parse, and a record after a torn line starts on a
  fresh line so it stays readable (test).
- [x] **Identity Center polling had no bound of its own.** `GrantAccess` and
  `RevokeAccess` give up after five minutes; the CLI and TUI have no other
  deadline.
- [x] **OIDC callback echoed the provider's error body.** The browser gets a
  short message; the detail goes to the log.
- [x] **IAM policy: the `IdentityCenterProvisioning` statement granted
  nothing**, because `iam:AWSServiceName` is only in the request context for
  `CreateServiceLinkedRole`; and Identity Center provisions member-account
  roles through its own service role, so the tool never needed it. Removed.
  `CreateOrganization` does need `iam:CreateServiceLinkedRole` for the
  Organizations role; added as a second bootstrap statement.
- [x] **Fake provider accepted any root email.** It now fails a creation with
  `EMAIL_ALREADY_EXISTS` when an account or a live request holds the email,
  so the closed-name guard is exercised by the fake, not only by a knob.
  `DailyCost` is read under the mutex.
- [x] **Approved record of a placed account could linger.** If the tag removal
  in `place` failed, the record held a queue slot until AWS forgot the
  creation. `expireRequests` drops approved records whose creation succeeded
  more than a day ago (test).
- [x] **LocalStack and Dex listened on every interface.** Compose binds them
  to `127.0.0.1`.
- [x] **Docs:** `costs` is cached hourly, not live; duration defaults read
  `14d` not `14`; `--no-worker` and the `0 = no limit` values are listed;
  intervals need a unit.
- [x] **TUI approve dialog defaulted to Approve.** The [design rule](DESIGN.md#rules) is that
  dialogs default to the safe button, and approval creates an account that
  cannot be closed for four days. It defaults to Cancel now (test).
- [x] **TUI went stale in silence.** After the first good load, a failed poll
  kept the old rows and said nothing. The header now shows a stale marker and
  a failed reload flashes the error; rows are kept (test).
- [x] **TUI load results could land out of order.** A slow `r` reload could
  overwrite the result of a later action with older rows. Loads and history
  reads carry a sequence number and stale results are dropped (test).
- [x] **TUI dialogs and forms accepted a second confirm** while the action
  ran, firing it twice. They close on confirm and the header spins until the
  action returns (test).
- [x] **TUI ctrl+c did nothing** inside a filter, form, dialog, or the
  notifications window. It quits from every mode; `q` still types (test).
- [x] **TUI detail hint bar advertised ↑/↓**, which the detail screen ignores.
  Dropped from the hint.
- [x] **TUI `parseSpan` accepted `-5d`, `0d`, `NaN`, and dates in the days
  field** (a date there became a 292-year TTL). Every branch requires a
  positive finite value; the request and edit forms refuse dates (table
  test).
- [x] **TUI layout:** a pane rule panicked below width 2; scrolling then
  enlarging the window left blank lines under the last row (tests).

### Open, accepted for now

- [ ] **Raw AWS SDK errors reach the page** on `/` and in form failures
  (`server.go` `fail` and `index`). They can name the management account and
  role. Core has no typed user-facing errors to filter on; until it does, the
  audience is signed-in engineers.
- [ ] **No `Strict-Transport-Security` header.** Expected from the
  TLS-terminating proxy; add it there.
- [ ] **Sessions are stateless bearers**; sign-out clears the cookie but a
  copied value stays valid until it expires (12 h). Common tradeoff for a
  tool this size.
- [ ] **Account-id enumeration through `Resolve`'s prefix match** (404 vs 403
  on `/accounts/{id}`). Account ids are not secrets.
- [ ] **`EnsureBudget` never adds alert subscribers to an existing budget.**
  A budget created while `--alert-email` was empty keeps no alerts after the
  flag is set. Needs `DescribeNotificationsForBudget` and
  `CreateNotification`; deferred.
- [ ] **CloudWatch history filter is case-sensitive** where the file sink
  folds case, and `%q` escapes non-ASCII in the pattern. Filtering
  client-side would scan the whole group; deferred.
- [ ] **`RevokeAccess` may report a missing assignment as failed** when
  Identity Center returns a `FAILED` status instead of
  `ResourceNotFoundException`. Core tolerates it and closes anyway.
- [ ] **TUI deny sends a fixed reason** ("denied in the TUI"). Needs an input
  field.
- [ ] **CI: `PLAYPLACE_PERMISSION_SET` and `PLAYPLACE_EMAIL_PATTERN` can
  still be overridden from the Run pipeline form**, which turns off owner
  validation for that request. Approval still gates creation. Pin them as
  flags on the script lines if it matters; the file says so.
- [ ] **CI image and packages are unpinned** (`golang:1.27`, `awscli`, `jq`),
  and `PLAYPLACE_VERSION` is unused.
- [ ] **`ListPlaygroundAccounts` makes one `ListTagsForResource` call per
  account.** Carried from the first pass; a longer inventory TTL is the first
  lever if throttling appears.

## First pass

Every item is addressed except the ones marked open under "Low and parity".
See "Done" at the bottom.

## Medium (all done, kept for the record)

- [x] **Two refresh passes can overlap.** The worker (`internal/worker/worker.go:44`),
  the web Sync button (`internal/web/server.go:390`), and every CLI command call
  `Service.Refresh` with no lock. Overlap means duplicate warnings, notifications,
  and audit lines, and two attempts to place the same account. Add a mutex in
  `Refresh`.
- [x] **`Request` ignores the inventory error** at `internal/core/service.go:363`
  (`all, _ := s.Inventory(ctx, true)`). If listing fails the duplicate-name guard
  is skipped and CreateAccount runs anyway. Return the error.
- [x] **CloudWatch sink data race.** `ready` and `stream` in
  `internal/audit/cloudwatch.go` are written without a lock while `serve` records
  from the worker goroutine and HTTP handlers at once. The stream name is fixed on
  first use and never rolls to a new day. The `PutRetentionPolicy` error is dropped.
- [x] **Approve is not atomic** (`internal/core/service.go:863`). `GetRequest`,
  `Request`, and the OU tag rewrite are three calls. A web click and a Slack click
  in the same window can both pass the pending check. If the tag rewrite fails it
  is only logged, so the request stays pending and can be approved twice.
- [x] **Only one close-quota reason is mapped.** `internal/provider/aws/aws.go:388`
  handles `CLOSE_ACCOUNT_QUOTA_EXCEEDED` but not
  `CLOSE_ACCOUNT_REQUESTS_LIMIT_EXCEEDED`. The intent tag survives and refresh
  retries, but every pass logs an error instead of "deferred".
- [x] **Closed names cannot come back.** The name check at
  `internal/core/service.go:823` only looks at open accounts, but the root email is
  derived from the name and AWS refuses any email ever used by an account. Approving
  a re-request of a closed name fails at AWS after the approver acted. Check closed
  accounts too, or add a unique suffix to the email pattern.
- [x] **The queue is capped by AWS tag limits.** A resource holds 50 tags, so
  pending plus approved-in-flight requests share that budget. The fake models it
  (`internal/provider/fake/fake.go:91`) but nothing documents it and the AWS error
  arrives raw. Document it and give a friendly error.
- [x] **htmx from a CDN with no integrity hash** (`internal/web/views.templ:17`),
  and no Content-Security-Policy header. Vendor the script or add SRI.
- [x] **`GET /requests/{name}/row` leaks other people's requests.** `editCancel`
  (`internal/web/server.go:296`) has no ownership check, so any signed-in engineer
  can read the owner, purpose, and budget of any pending request. Gate it like
  `editForm`, or like `withdraw`.

## Low and parity (done unless marked open)

- [x] **Withdraw exists only in the web UI.** No CLI command, no TUI key.
- [x] **Edit exists in web and CLI but not the TUI.**
- [x] **TUI request form has no override flag**, so an operator who hits a ceiling
  there is told an admin can override with no way to do it. (The TUI extend form
  now has one; mirror it on `n`.)
- [x] **Editing clears the override flag.** `UpdateRequest`
  (`internal/core/service.go:950`) overwrites `OverrideLimits` with whatever the
  caller passes, so `edit-request NAME --budget 20` silently drops an existing
  override. The audit diff never mentions override changes. The web form pre-checks
  the box so only the CLI is affected.
- [x] **Self-approval compares against the requester only.** If an admin requests
  on behalf of an approver, that approver can approve their own account. Consider
  refusing when approver equals owner too.
- [x] **Sub-hour TTLs become fourteen days.** `Encode` truncates to whole hours
  (`internal/core/request.go:102`). A request under one hour round-trips as zero
  and `Approve` substitutes DefaultTTL because zero means unset.
- [x] **TUI approve dialog shows the wrong day count** (`internal/tui/tui.go`,
  `openApprove`). It derives days from a pseudo-account whose expiry was computed
  at inventory time, so it is often one short. Use the request TTL.
- [x] **Withdraw button visibility is case-sensitive** (`internal/web/views.templ:189`)
  while the handler uses `EqualFold` (`internal/web/server.go:352`).
- [x] **`.pill.requested` CSS never matches**; the status is `pending`, which has
  no style (`internal/web/views.templ:32`).
- [x] **`refresh` answers non-admins with a 200 error fragment** where every other
  handler uses `forbid` (`internal/web/server.go:385`).
- [x] **`RequestEditFromForm` swallows bad ttl and budget values** where `submit`
  rejects them (`internal/web/server.go:326`).
- [x] **Per-owner limit is skipped by `create` and by `Approve`** (they go through
  `Request`, not `SubmitRequest`). Probably intended for the operator escape hatch;
  document it.
- [x] **TUI history cache is cleared every tick** (`internal/tui/tui.go`, the
  `loadedMsg` case), so with the CloudWatch sink each tick issues a year-wide
  FilterLogEvents for the selected account.
- [x] **Docs overpromise.** docs/index.md says requests come from Slack, but Slack
  can only approve and deny. docs/requesting.md says requesters are told the
  outcome and get budget alerts, but notifications go to a log line or one shared
  webhook, and Budgets alerts go only to `--alert-email`. The TUI empty state
  (`internal/tui/render.go:270`) tells people to run `create`, which skips approval.
  (The "open the account page for other spans" line was removed with the extend
  fix; the account page has no extend control.)
- [x] **Dead condition** `len(e.points) >= 0` at `internal/core/service.go`
  (`Costs`).
- [x] **Repeated OU tag reads.** `PendingRequests` now reads through the
  inventory cache, so a page render costs one OU tag read. `GetRequest` stays
  live because callers act on it.
- [ ] **Open: `ListPlaygroundAccounts` makes one ListTagsForResource call per
  account.** Organizations has no batch tag read; accepted until the fleet is
  large enough to hit throttling, at which point a longer inventory TTL is the
  first lever.
- [x] **Identity Center region is undocumented.** The ssoadmin and identitystore
  clients use the default region, which must be the Identity Center home region.
- [x] **GitLab trust policy pins `ref:main`** (`deploy/aws/gitlab-oidc-trust.json`)
  while this repo is on master. Placeholder, but worth a comment.
- [x] **Cookie `clear` uses only `r.TLS` for Secure** where `setSigned` also checks
  the redirect URL scheme (`internal/web/auth.go:299`). Harmless.
- [x] **`email_verified` is only rejected when explicitly false**
  (`internal/web/auth.go:214`). Fine for Google; note it for generic issuers.

## Done

- Refresh and every queue mutation take `Service.opMu`.
- `Request` returns the inventory error.
- CloudWatch sink: mutex, one stream per host and day with rollover, retention failure logged through an optional logger.
- Approve marks the record approved before creating, restores it on failure, and a concurrent second approve finds nothing pending (test).
- `CLOSE_ACCOUNT_REQUESTS_LIMIT_EXCEEDED` also maps to `ErrCloseQuota`.
- Names of closed accounts are refused when the root email would be derived from them; `create --email` still works (test).
- `SubmitRequest` refuses when the OU has no tag left, with an explanation (test).
- htmx is vendored under `internal/web/static`, the inline `hx-on` handler moved to `app.js`, and every response carries a CSP with `script-src 'self'`; checked in a browser with zero console errors.
- `/requests/{name}/row` is gated like the pending card (test).
- Found while testing in the browser: both HTML `pattern` attributes were invalid under the `v` flag and browsers ignored them; `-` and `/` are now escaped.
- Low and parity pass: `withdraw` on the CLI and `w` in the TUI; `e` edits a pending request in the TUI; the TUI request form has an override field; `RequestEdit.OverrideLimits` is a pointer so edits keep an existing override unless told otherwise, and override changes appear in the audit diff; the self-approval rule also refuses the owner (`core.CheckApprover`, used by the web button too); sub-hour TTLs round up to one hour instead of decoding as unset; the TUI approve dialog and edit form read the request TTL from the pending row; withdraw button visibility ignores email case; the pending pill is styled; `refresh` refusals are 403; bad numbers on the edit form are errors; the TUI clears its history cache after actions, sync, and `r` rather than on the timer; docs no longer say requests come from Slack or that requesters get personal notifications; the dead cost-cache condition is gone; `PendingRequests` reads through the inventory cache; Identity Center region and `email_verified` are documented; the GitLab trust policy branch is called out; cookie clear uses the same Secure rule as set.
