# Review findings

Three passes so far: two on 2026-09-17 and one on 2026-10-09. The first two
passes are fixed except the items under "Open, accepted for now". The third
pass found more than the fixes below cover. Line numbers come from the
reviews, and some have drifted.

## Third pass

A review of the whole tree on 2026-10-09 with adversarial verification. It
kept 242 findings. The ids in brackets are the ids of that report. The fixes
landed on 2026-10-10 and 2026-10-11.

### Fixed

- [x] **A login-flow cookie signed in as a session** (websec-1). The flow
  cookie and the session cookie had the same signature, so the signed flow
  cookie from `/auth/login` worked as a session with an empty identity. The
  MAC now covers the cookie name, and `Authenticate` refuses a session
  without an email (tests in both directions).
- [x] **Sign-in failed open** (websec-5, cli-4, docs-5). Without
  `--oidc-issuer` or `--oidc-preset`, `serve` ignored the other sign-in flags
  and made every visitor an admin. `serve` now refuses to start when a
  sign-in flag is set without an issuer or a preset (test).
- [x] **The TUI tests failed after 2026-10-01** (tui-1). `loadWith` read the
  wall clock instead of the injected clock. It captures the clock now.
- [x] **Month-to-date spend used the local month** (web-17, the month part).
  Cost Explorer reports whole UTC days. `core.MonthToDate` sums the UTC month
  of the injected clock, for the TUI and the web UI (test).
- [x] **Go 1.27.1 had two `net/http` vulnerabilities** on reachable paths
  (GO-2026-6617, GO-2026-6613). `go.mod`, `mise.toml`, and the AUR templates
  require 1.27.2.
- [x] **A zero `--default-ttl` or `--request-ttl` was destructive** (core-6,
  cli-2, arch-5). Zero closed every new or adopted account, or dropped every
  request. Both must be positive now. `--warn-before` must be shorter than
  `--default-ttl`. When `--max-ttl` is set, `--default-ttl` must be at most
  that value. When `--max-budget` is set, `--default-budget` must be at most
  that value (table test).
- [x] **The GitLab flow gave management-account credentials to requesters**
  (deploy-1, docs-6, cli-1, deploy-2, deploy-3, deploy-5, deploy-6,
  deploy-14, docs-11). Through pipeline variables, a requester was able to
  run code with the exported keys and change every guardrail. The flow is
  removed: the CI file, the issue template, the trust policy, and the docs.
- [x] **Normal budget states failed `approve` and `create`** (core-9,
  budget-4, aws-2). Playplace puts protection in the states setup-pending,
  restricting, recovering, and retiring while AWS works. These states now
  fail a pass only after two hours past `playplace:budget-checked`. During a
  recovery, a `REVERSE_FAILURE` or `RESET_FAILURE` is sent again and fails
  the pass at once. Other errors still fail at once. Readiness waits for
  settled protection before it grants access. The fake now creates the
  budget action in two passes, as AWS does, and the CLI wait names what the
  account waits for (tests).
- [x] **Budget notices on setup completion** (budget-6, in part). Setup
  completion sends no notice now, and a repair from `error` is announced
  once. The `--alert-email` rotation part is fixed further down.
- [x] **Refresh moved quarantined accounts back into the OU, and any command
  created a missing organization or OU** (gap-1, aws-1, deploy-4, core-5).
  Placement happens once, only from the organization root, and only for an
  account that playplace created and has not placed yet. `PlaceAccount`
  refuses any other parent. Only `init` creates the organization or the OU.
  Every other command uses a read-only lookup that fails closed. A creation
  that waits in the root shows as `creating` and is retried on every pass,
  with no age limit. The bootstrap statements moved to
  `deploy/aws/playplace-bootstrap-policy.json`, which you attach only while
  `init` runs (tests).
- [x] **Decisions acted on whatever request held the name** (core-7,
  websec-3). Approve, deny, withdraw, and edit take a version: the journey id
  and a digest of every shown field. Web forms, Slack buttons, and the TUI
  row send the version that they showed. CLI `approve` binds the version
  that it prints. CLI `deny`, `withdraw`, and `edit-request` bind the version
  that they read in the same command. A stale version is refused and drops
  the cached queue. `playplace approve` now prints the
  request and asks `[y/N]`. Scripts must pass `-y` (tests).
- [x] **`create` took the name of a pending request, and placement deleted
  that request** (core-2, hist-1). `create` refuses the name of a
  pending request. Placement removes only the approved record of its own
  journey (tests).
- [x] **A request named like an account id hijacked that id** (websec-2).
  `Resolve` checks exact ids before names (test). A name still wins over an
  id prefix, so the CLI prefix lookup can still be shadowed. Web routes use
  full ids.
- [x] **Approvers saw rounded values** (found while fixing core-7). The
  version binds exact values, but several views rounded a 36-hour lifetime to
  `1d` and a $12.50 budget to `$12`. `core.Lifetime` and `core.Dollars` now
  format every view where an approver decides, the request and edit notices,
  the history lines, and the CLI and TUI request confirmations (tests).
- [x] **The runtime IAM policy had no tests** (deploy-11). A test now checks
  that only the bootstrap policy can create organization resources. The
  GitLab trust policy is gone.
- [x] **Lifecycle notices were dropped by default** (cli-6). `notify.Log`
  logged at Info, and the default `--log-level` is warn. Notices now log at
  Warn, and `serve` warns at startup when `--slack-webhook` is not set. The
  TUI shows a notice as an info toast. The docs and the UI no longer say
  that the requester or the owner is told. The `warned` history event and
  the labels now name the *expiring* notice (tests).
- [x] **Rotating `--alert-email` sent restricted accounts through
  setup-pending** (budget-6, the rest). A repair that changes only the
  recipients now keeps the observed state, with no health change and no
  notice. An update that AWS rejects still fails the pass. The test for that
  case came later, with the review of how the two commits interact.
- [x] **A nested Playground OU was refused.** `--ou-id` (`PLAYPLACE_OU_ID`)
  names an OU at any depth. playplace reads it with
  `DescribeOrganizationalUnit`, refuses to start when `--ou-name` is also
  set, and `init` creates nothing for it. A valid ID of the wrong OU passes
  every check, so `init` now lists every active account in the OU that
  playplace does not manage, with what the next refresh does to it (tests,
  and a LocalStack run with a nested OU).
- [x] **The web and TUI edit forms rounded the lifetime and the budget**
  (tui-3). Both forms now prefill the exact values and send only the fields
  that the editor changed (tests, and a LocalStack run with 36 hours and
  $12.50).
- [x] **`PollCreate` treated a `PENDING_ACTIVATION` account as
  unavailable.** The wait now says that the account waits for AWS to
  activate it, and keeps polling (tests).
- [x] **`approve` and `create` hid that AWS created the account** (found in
  the LocalStack run). When a step after the creation fails, the error now
  names the account ID, says whether playplace placed the account, and says
  whether the next refresh retries the step. `core.CreatedError` carries
  this to the CLI (tests, 9 mutations caught).
- [x] **Log lines broke the progress dots of the creation wait** (found in
  the cross-review). Notices log at Warn now, so a notice sent during the
  wait landed in the middle of the dots. The wait now prints whole lines,
  one for each change of state (test).
- [x] **The AUR `go>=1.27.2` constraint did not hold.** The Arch `go`
  package has epoch 2, so pacman ranked every Arch Go version above the
  minimum. The constraint is now `go>=2:1.27.2` (checked with `vercmp`,
  test).
- [x] **`task` ran templ with a positional path**, which wrote a different
  file name into the generated views. The Taskfile now runs
  `templ generate -path internal/web`.
- [x] **The LocalStack flow was not run after the OU change.** It ran on
  2026-10-11. LocalStack keeps the `CreateAccount` tags, and placement
  works.

The next four items came from a review on 2026-10-11 of how 1c1ddfc and
874a2b9 interact. Each finding had a verifier that tried to refute it. A
second review of the fixes found more gaps, and the entries include them.

- [x] **The `init` list left out managed accounts and root creations.** It
  listed only the accounts without `playplace:managed=true`. If `--ou-id`
  named a quarantine OU by mistake, `init` printed nothing for it. The next
  refresh closed its expired accounts, and it moved each creation that waited
  in the root into that OU for good. `init` now lists the managed accounts
  in the OU and the waiting creations too. A creation with damaged tags is
  listed apart, because the refresh leaves it in the root. `init` says so
  when there is nothing to list (`core.Service.Forecast`, tests that check
  each forecast against the next refresh). It does not list the accounts in
  the OU that AWS is already closing, and the docs say so. `fake.Seed` now
  copies its tags, so the test that `init` writes no tag can fail.
- [x] **The limit refusals rounded both sides**, for example "ttl 90d exceeds
  the maximum of 90d" for 90.5 days. The request and edit refusal now shows
  both sides as approvers see them: "lifetime 2172 hours exceeds the maximum
  of 90 days" and "budget $500.40 exceeds the maximum of $500". The extend
  refusal does the same, and shows the time of the latest expiry, because it
  can fall on the requested day (tests). The web edit form that now takes
  cents and part-days made this easier to reach.
- [x] **A repair into `restricting` promised no further notice.** The
  `restricted` notice follows when the restriction is in place. The notice
  now says so, and docs/budgets.md lists the notice at the end of each kind
  of repair (test).
- [x] **No test covered a rejected recipient rotation.** A test now has AWS
  lock the action in `restricting` and `recovering`. The pass fails, health
  shows `error`, one notice is sent, and the first accepted pass rotates the
  recipient and returns to the observed state.

### Accepted or documented

- [ ] **An admin can edit another person's pending request and then approve
  it alone** (core-8). Accepted and recorded in ARCHITECTURE.md. Admins are trusted,
  and history records the edit.
- [ ] **Approval applies the approving process's configuration** (gap-3).
  Documented in ARCHITECTURE.md and `docs/cli.md`: `--email-pattern`,
  `--permission-set`, `--max-ttl`, and `--max-budget` come from the process
  that approves.
- [ ] **Slack messages are not updated after an edit** (web-9, cli-13).
  Documented. Only web requests post a message. A click on an old message is
  refused in private, because the button carries the version.
- [ ] **Read-only CLI commands still run a refresh** (cli-12). The deploy
  README documents the one-writer rule: while `serve` runs, give read-only
  commands `--no-refresh`.
- [ ] **Nothing enforces `init` before the first refresh.** The docs say to
  run `init` first, so that the operator sees the account list before
  playplace adopts accounts. State lives in AWS tags, so playplace has no
  record that `init` ran.
- [ ] **LocalStack Pro 2026.9.1 has no Budgets API and never confirms a
  closure.** Documented in DEVELOPMENT.md. Protection shows `error`, and a
  closed account stays `unavailable`, because LocalStack sets only `Status`.

### Open

- [ ] **Creations from outside playplace show in the list** (found in the
  LocalStack run). The inventory reads the creation list of the whole
  organization. An in-progress creation by another tool shows as `creating`
  and holds its name. For one hour, so does a finished creation in another
  OU. A failed one shows as `failed` for 7 days and does not hold its name.
  Nothing acts on these accounts. The base showed a finished creation for
  24 hours. A fix must keep the name of a `create` taken while AWS is late
  to list the account.
- [ ] **A refresh error with several causes prefixes only the first line.**
  The CLI prints the joined causes after one `refresh:` prefix, so the later
  lines do not start with it.
- [ ] **A lifetime at or below `--warn-before` gets the *expiring* notice at
  once.** The first refresh after placement sends it.
- [ ] **TUI success flashes show two check marks.** `notice.go` adds a check
  mark to a flash that already starts with one.
- [ ] **A recipient rotation does not reach the budgets of closed, closing,
  or expired accounts.**
- [ ] **An owner stored with spaces around it changes on an untouched edit
  save.** The edit forms trim the owner, so the trimmed value counts as a
  change.
- [ ] **The TUI request form allows only 80 characters for the owner.** The
  edit form allows the full tag length.
- [ ] **No test fails if the TUI stops calling `useFeed`.** Without it,
  notices print over the TUI screen.
- [ ] **The not-placed error cannot say whether someone moved the
  account.** `PlaceAccount` errors are plain text, so the message says
  "unless someone moved the account to another OU".

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
- [x] **TUI approve dialog defaulted to Approve.** The [design rule](ARCHITECTURE.md#rules) is that
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
- [x] **`EnsureBudget` never adds alert subscribers to an existing budget.**
  Fixed in a436b69, and this item was not updated. `EnsureBudget` now
  creates a missing notification and replaces the email subscriber of an
  existing one (`internal/provider/aws/budget.go`). It keeps SNS
  subscribers and other notifications. Found during the third pass.
- [ ] **CloudWatch history filter is case-sensitive** where the file sink
  folds case, and `%q` escapes non-ASCII in the pattern. Filtering
  client-side would scan the whole group; deferred.
- [ ] **`RevokeAccess` may report a missing assignment as failed** when
  Identity Center returns a `FAILED` status instead of
  `ResourceNotFoundException`. Core tolerates it and closes anyway.
- [ ] **TUI deny sends a fixed reason** ("denied in the TUI"). Needs an input
  field.
- [x] **CI: `PLAYPLACE_PERMISSION_SET` and `PLAYPLACE_EMAIL_PATTERN` can
  still be overridden from the Run pipeline form**, which turns off owner
  validation for that request. Closed on 2026-10-10: the GitLab flow was
  removed. A later review found that the unset list covered 6 of about 30
  flags, and that any pipeline variable such as `CC` could run code with the
  exported management-account keys.
- [x] **CI image and packages are unpinned** (`golang:1.27`, `awscli`, `jq`),
  and `PLAYPLACE_VERSION` is unused. Closed on 2026-10-10 with the GitLab
  flow.
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
