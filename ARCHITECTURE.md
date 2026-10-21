# Architecture

Architecture, lifecycle rules, and interface design decisions for playplace.
For an overview and usage, see [the README](README.md); for contributor setup
and local testing, see [Development](DEVELOPMENT.md). For the visual design
system of the web UI, see [DESIGN.md](DESIGN.md).

## Principles

- **No state.** The Playground OU says which accounts exist; tags on each
  account say everything else. Anything not derivable from AWS is not kept,
  except an optional history log, which is queried for display but never used
  to decide account lifecycle state. See [No state file](#no-state-file).
- **Management account only.** The tool holds management-account
  credentials and never assumes a role into a playground account. What runs
  inside an account is somebody else's concern.
- **Every account starts as an approved request.** Requests wait as tags on
  the OU. Approval, not creation, is the human step, because a created
  account counts against the account quota of the organization until 90
  days after it closes.
- **Guardrails are data, not code paths.** Owner, expiry, budget, and the
  approval trail are tags. Ceilings and limits are configuration.
- **Cost Explorer bills per call.** Nothing polls it. Views cache for an
  hour and wait five minutes after a failed pull. `list` never touches it.

## No state file

There is no database and no state file. The playground OU decides which
accounts are playgrounds, and tags on each account hold the rest:

| tag | meaning |
|---|---|
| `playplace:managed` | `true` once the tool has tagged the account |
| `playplace:journey` | stable history correlation identity, not lifecycle authority |
| `playplace:owner` | who is responsible |
| `playplace:expires` | RFC3339 expiry |
| `playplace:budget` | desired approved monthly budget in USD |
| `playplace:budget-policy` | configured restriction SCP; prevents silently removing enforcement |
| `playplace:budget-health`, `playplace:budget-checked` | observed protection state and last state/error-change time |
| `playplace:budget-error` | bounded base64url error text |
| `playplace:budget-recovery` | compact base64url recovery intent: action ID, approved limit, pre-update spend, month and reverse/reset phase |
| `playplace:warned-at` | set once the *expiring* notice was sent |
| `playplace:close-requested` | durable close intent and start of the closure-monitoring clock |
| `playplace:closed-at` | when playplace observed AWS `CLOSED` and recorded confirmation |
| `playplace:close-alerted-at` | one-shot overdue-closure warning marker |
| `playplace:access` | Identity Center principal the owner's access was granted to |
| `playplace:handoff` | initial handoff progress: `placing`, `pending`, or `complete`; absent on legacy accounts |
| `playplace:requested-by`, `playplace:approved-by`, `playplace:purpose` | audit trail from the request |

Pending requests are tags on the Playground OU itself, keyed `playplace:req:<name>`.
AWS allows 50 tags on a resource, so the queue holds at most 50 requests,
pending and approved-but-not-yet-placed together, less any other tags on the
OU. A request past that is refused with a message saying so.

A name whose account was closed cannot be requested again while the closed
account is still listed, because AWS never frees a root email and the email
is derived from the name. `create --email` with a fresh address is the way
around it.

Everything stored in a tag obeys AWS's rules: values up to 256 characters made
of letters, numbers, spaces, and `_ . : / = + - @`. Purpose is capped at 120
characters so the request record fits. Owners, requesters, and purposes are
checked on the way in and refused with a message that names the rule.

Everything else is read live: account status from Organizations, in-flight
and failed creations from `ListCreateAccountStatus`, finished creations that
wait for placement from the organization root, spend from Cost Explorer.
Accounts that land in the OU without tags are adopted with defaults on the
next refresh. Cost Explorer bills per call, so `list` never touches it;
`show`, `costs`, the TUI, and the web UI cache results for an hour.
Spend is known only while the last pull of the account succeeded
(`CostState`). After a failed pull, a pull that is not forced waits five
minutes. A forced pull tries at once: Sync in the web UI, an account page
whose spend is not known, and `r` and `S` in the TUI. Pulls of one account
can overlap, and the cost state keeps the answer of the newest one. `show`,
`costs` and the TUI show unknown spend as `unavailable`, never as $0.

### Placement and the organization

Only `playplace init` creates the organization or the Playground OU. Every
other command looks them up and fails when either is missing. This stops a
wrong AWS profile, a typo in the OU name, or an OU nested below another OU
from creating a new, empty OU that has none of the operator's SCPs.

Playplace finds the OU in one of two ways. `--ou-name` finds it by name
among the direct children of the organization root. `--ou-id` finds it by
ID at any depth, with one read-only `DescribeOrganizationalUnit` call, and
takes the name of the OU from AWS for display. Playplace refuses to start
when the operator sets both, because it would ignore the name. When
`--ou-name` finds no OU, the error names `--ou-name` and points to `--ou-id`
for a nested OU. It names `playplace init` only for the case where no
Playground OU exists yet. When the real OU is nested, `init` without
`--ou-id` creates a second OU under the root. With `--ou-id`, `init` creates
nothing, not even the organization, because a new organization cannot hold
the OU. It verifies the OU and reports it. An ID that AWS does not find, or
that belongs to another organization, fails every command closed. AWS does
not show an OU of another organization, so a wrong profile ends there too.
A failed lookup is not cached, so a running `serve` continues once the
lookup succeeds, for example after `init` runs.

A valid ID of the wrong OU passes every check. The OU must hold only
accounts that the operator wants playplace to manage. On the first refresh,
playplace adopts every active account in the OU that does not have the
`playplace:managed=true` tag. The account keeps each valid playplace owner,
expires and budget tag. Without an owner tag, the owner is `unknown`.
Without a valid expires or budget tag, the account gets the default
lifetime or the default budget. playplace closes the account when its
lifetime ends. Two kinds of account are not adopted. playplace starts to
close an account with a `playplace:close-requested` tag at once. It leaves
an account with a damaged `playplace:close-requested` tag alone until
someone repairs the tag. So `init` lists these accounts with what the next
refresh does to each. It also lists the active accounts in the OU that have
the `playplace:managed=true` tag, and the creations of playplace that wait
in the organization root. A quarantine OU that is named by mistake shows its
accounts there. The next refresh resumes their management, and it closes
each one that expired or has a `playplace:close-requested` tag. It moves
each waiting creation into the named OU, unless the creation has damaged
tags. Placement happens once, so a creation in the wrong OU stays there
after the operator corrects the OU. `init` does not list the accounts in
the OU that AWS is already closing, because the refresh only tracks that
closure. The operator checks the list and moves every other account out of
the OU before any other command, because most commands start with a
refresh.

AWS creates every new account in the organization root. Placement moves it
from there into the OU, once. Playplace places an account only when it sits
directly under the root and its tags say `playplace:managed=true` and
`playplace:handoff=placing`. Readiness replaces `placing` right after the
move, so a placed account never qualifies again. The provider also refuses
to move an account from any parent other than the root. Placement does not
write the handoff tag. Refresh and the creation poll of `create` and
`approve` read the account again after they place it. A stale read then
cannot undo a handoff that another process finished, and the `placed`
history event and the *ready* notice are not repeated. A nested OU changes
none of this. Placement moves an account straight from the root into it, and
never from a parent OU.

An operator can move an account out of the OU, for example to a quarantine
OU. That ends playplace management for the account: playplace does not move
it back, show it, close it at expiry, or revoke owner access. The budget and
any owner access stay. Its name and its place in the per-owner limit are free
again. Moving the account back into the OU resumes management with its
existing tags. See
[the deployment guide](deploy/README.md#move-an-account-out-of-the-playground-ou).

A creation that is not placed yet stays in the root. The inventory shows it
as `creating` with its owner from the creation tags. After one hour it also
carries a note: it waits for the next reconcile and has no budget, OU
guardrail, or expiry until then. The note does not say that placement
failed, because the inventory cannot tell whether a reconcile ran. Every
refresh retries the placement and fails while it fails. Nothing ages these
accounts out, so they stay visible and keep failing reconciliation until
placement succeeds or an operator acts. To stop the retries, remove the
`playplace:handoff` tag from the account or move it to another OU.
Playplace never closes an account in the root. If the approved expiry passed
while the account waited there, the pass that places it also closes it.

AWS can take a few minutes to activate a new account after it reports the
creation as done. Until then AWS shows the account state as
`PENDING_ACTIVATION`, and nothing places the account. For one hour after AWS
created the account, the inventory and the creation poll of `create` and
`approve` show it as `creating`. The creation poll keeps going, and refresh
does not report the account. After that hour the account is `unavailable`,
and refresh reports it.

The root listing reads tags only for accounts that can be playplace
creations. It skips the management account, invited accounts, and names that
playplace never gives. If the root listing fails, the inventory still lists
the OU and the queue, and refresh reports the failure. AWS can report a
creation as done before it lists the new account. For one hour after that
report, the inventory shows the creation from its status while neither the
root nor the OU lists the account. This keeps the name taken.

### Ownership is not tag validity

The account module interprets `playplace:managed=true` as ownership independently
of expiry, approved budget, and close-intent validity. Defaults belong to genuine
adoption, not repair of owned accounts. Missing/invalid owned expiry or budget,
and nonempty malformed close intent, produce field diagnostics rather than a new
lifetime, a replacement approved amount, or assumed absence of closure intent.
Unknown expiry/budget render as unknown (JSON `null` with `tag_errors`); lifecycle
status remains derived separately. Repair never enrolls a legacy initial handoff.

Readiness, placement, and budget changes require intact guardrail facts. While
repair is needed, existing access and AWS protection remain; no new grant,
handoff, budget update, reverse or reset is attempted. Extension requires known
expiry and unambiguous close intent, not a valid budget. Neither extension nor
budget mutation doubles as corruption repair, including with an override.

There is deliberately no global all-tags-valid gate: valid expiry can still
justify closure, valid close intent still progresses, explicit close remains
available, and fresh AWS closure state supports monitoring and retirement.
Malformed close intent alone cannot initiate closure. On observed
`PENDING_CLOSURE`, a missing/unusable monitoring timestamp may be recorded as the
current observation time, not a reconstruction of when closure was requested.
Retirement ignores unrelated damaged facts and retains all fresh adapter safety
checks. Active repair errors do not stop work on other accounts.

This chooses explicit operator repair over automatic renewal, automatic closure
based on unknown expiry, or a new repair command. See the
[operator repair procedure](docs/budgets.md#damaged-account-guardrail-tags).
No provider interface or durable tag format is added.


## Budget protection

The AWS adapter reconciles budget amounts, the two owned notification thresholds,
and optional per-account AWS Budgets action configuration. The core budget module
owns recovery approval, durable phase progression, and reverse/reset ordering.
No-op passes never call
`UpdateBudget`, which resets AWS's calculated spend. New owner grants wait for
successful protection setup; failures on existing managed accounts are retried
without preventing expiry/closure. Admin increases persist intent before AWS
mutations. Reverse completion is observed before persisting the reset phase,
so a crash after reset cannot authorize reversal of a newly executed action.
Confirmed-closed accounts reconcile owned-action retirement independently of the
closure-confirmation marker. The provider rechecks CLOSED, account/OU ownership,
and the action's target/policy/role before deletion; only subsequent absence means
`retired`. Failures retry across restarts without reverting closure. Budgets and
alerts remain; retirement does not reverse/reset actions or clean up member resources.
The SCP and execution role are deployed separately; runtime never directly
attaches/detaches policies. See [Budget protection](docs/budgets.md) for coverage,
authorization, recovery, retirement, and the single-writer deployment requirement.

The budget module spans `internal/core/budget.go` and `budget_recovery.go`:
`SetBudget` inspects pre-change spend and persists the approved amount and recovery
intent before reconciliation can update AWS. `EnsureBudget` repairs configuration
and returns observations, never recovery-phase instructions or reverse/reset
requests. Core chooses explicit mutations through `ExecuteBudgetAction`; the AWS
adapter rechecks the target and owned action before submitting them. Reverse
success only advances the durable phase; reset is submitted on a later pass.
Missing action observations mean setup is pending, while observed empty/unknown
statuses remain errors. Recovery intent is retained on errors, even if the action
has progressed; accepted mutations are not completion.
A trigger repair (threshold, notification type or approval model) returns no
observation, because it changes when AWS acts. A recipient rotation that AWS
accepts returns the status that the pass read before the update, because a
recipient does not change when or how AWS acts. So an accepted change of
`--alert-email` from one address to another changes no budget health, keeps
`playplace:budget-checked`, and sends no notice. If AWS rejects the update,
for example while it locks the action during a transition, the pass fails
like any other error, and a later pass retries it. A rotation reaches only
active, unexpired accounts without a close request. Budgets of closed,
closing and expired accounts keep the old address until an operator changes
it in AWS Budgets.

Settling protection is a state that a later pass completes: `setup-pending`,
`restricting`, `recovering` or `retiring`. It is progress, not failure.
Creation polling, budget changes, closure and refresh succeed while protection
settles. New grants and the initial handoff wait for it. A pass fails only when
one state lasts more than two hours after `playplace:budget-checked`, so
protection that never settles still alerts. A recovery step that AWS reports
as `REVERSE_FAILURE` or `RESET_FAILURE` is not settling. Playplace sends it
again, and that pass fails at once while health stays `recovering`. Finished
setup sends no budget notice, because the *ready* notice covers a new
account. A repair from `error` is announced when it starts, not when it
finishes.

The fake adapter simulates remote completion of explicitly submitted operations,
not recovery policy. Like AWS, it shows a newly created action only on the next
pass. It records the alert recipient of each account, and a new recipient keeps
the observation, as in the AWS adapter. The same core decisions run against
both adapters. Recovery records retain
their existing tag encoding and resume without migration; caller commands and
health states are unchanged. Caller-interface
tests use the real AWS adapter with loopback observations and durable mock tags to
exercise interrupted writes, restarts, re-execution and period rollover together.
These tests do not establish live AWS propagation, IAM or SCP behavior.

## Lifecycle

Statuses are derived, never stored:

    pending    a request waiting for an approver (a tag on the OU)
    creating   a creation request AWS has not finished, or a finished creation
               that still waits in the organization root for placement
    failed     a creation request AWS rejected (kept 7 days)
    active     in the OU with no warning or close tag
    expiring   the warned-at tag is set
    closing    close intent is tagged or AWS reports PENDING_CLOSURE
    closed     AWS explicitly reports CLOSED
    unavailable AWS reports SUSPENDED or an unknown state, or it reports
                PENDING_ACTIVATION and the account is placed or AWS created
                it more than one hour ago

AWS lets you close 20% of member accounts or 250, whichever is higher, per
rolling 30 days (capped at 1000, at most 3 closures in flight). A closed
account counts against the account quota until AWS closes it permanently,
90 days later. The 4-day minimum age in the AWS quotas applies to removing
an account from the organization, not to closing it. A refused close leaves the
intent in the tag and each refresh retries while AWS still reports ACTIVE.
`CloseAccount` is asynchronous: accepting it is not confirmed closure. Pending
closures stay visible and consume the owner's account slot. Each refresh checks
AWS's `State` field; suspension alone is never treated as closure. Older API
emulators exposing only ambiguous `Status=SUSPENDED` show as unavailable.

After `--close-alert-after` (default `24h`) without confirmation, reconciliation
returns an error, logs the overdue account, records `closure-overdue` in history,
and sends the *overdue* notice once. Monitoring continues until AWS reports
`CLOSED`. playplace stores the confirmation and notice markers in account tags.
When it cannot write one, it reports the error and tries again on the next
refresh. Notices and history remain best-effort, so alert on failed scheduled
reconciliation as well.

Before granting playground access, deploy the purchase/trail guardrail SCP and
review existing accounts using [the deployment safeguards guide](deploy/README.md#billing-and-closure-safeguards).
The SCP blocks new commitments and Marketplace purchases and member-created
trails; organization audit trails are preserved. Nothing runs `aws-nuke` or
deletes account resources. Closure is not a guarantee of zero further bills:
prior usage, existing commitments, Marketplace subscriptions, and central
logging costs need separate operator review.

### Lifecycle notices

A lifecycle notice is a short message about a change to a request or an
account. Examples are the *approved*, *denied*, *expiring*, and *closed*
notices. `core.Service` hands each notice to one notifier. `notify.Log`
always runs and writes the notice to the log at Warn, so the default
`--log-level` of `warn` shows it. With `--slack-webhook`, `notify.Multi` also
posts it to the webhook's channel. The log line stays next to Slack on
purpose. The log then records what playplace sent, and a failed post leaves
the text of the notice in the log next to the error. Warn does not raise an
alert, because the deployment guide alerts on error logs.

A notice goes to a log or a channel, never to a person. Its owner field names
the account owner, or `approvers` for a new request. The requester is not a
recipient. When the requester or the owner must know, an approver tells them,
or they read the channel.

A notice never holds the raw text of a failed call to AWS, because that text
holds AWS request IDs and Slack shows the notice. When a budget step fails in
AWS, the budget notice says "The call to AWS failed. An admin can read the
details in the playplace log." Core then writes the raw error to the log at
Warn, with the name and the ID of the account. A refusal of core keeps its
words in the notice. The budget error tag and the history details keep the
raw error. The *creation failed* notice names the reason code that AWS
returns, for example `EMAIL_ALREADY_EXISTS`. A reason code holds no request
ID.

`serve` logs one warning at startup when only the log carries notices. Other
commands do not warn. They run on every operator call or, like `reconcile`,
on a schedule, so the same warning would repeat on every run. The TUI moves
the log notifier to its notice feed with the service logger.


## Requests and approval

Every account starts as a request, and every request needs an approver who is
not the requester. Pending requests live as tags on the Playground OU, one tag
per request, so the CLI, TUI, web UI, and Slack all see the same queue with no
database. A request records owner, TTL, budget, purpose, who asked, and when.
Approving marks the record approved first and requests the account second,
so a second approver, in the same process or another, finds nothing pending.
If AWS refuses the creation the mark is removed and the request is pending
again. The removal also runs when the approver stops waiting, for example when
the browser closes or a Slack click passes its deadline. It has a limit of 30
seconds of its own. If AWS accepts the request and reports the creation as
failed later, the approved record stays for 7 days. The first refresh or CLI
wait that sees the failure sets an `f` flag in the record, then writes the
*failed* history event and sends the *creation failed* notice. The flag comes
first, so a crash can lose the notice but a later pass in the same process
does not repeat it. Older binaries ignore the flag. Within one process the
refresh pass and every change to the queue are serialized, so the worker, the
web UI, and Slack never run the same pass or act on the same request at once.
The account carries `requested-by` and `approved-by` tags for audit. Requests
nobody acts on expire after `--request-ttl` (7 days).

A decision names the request version that the actor saw, not only the
request name. A request version is the journey id and a short digest of the
values in the pending record. It changes when an admin edits the request. It
also changes when the request ends and someone submits a new request with the
same name. playplace computes the version from the record, so the version
needs no storage. Every interface sends the version that it showed. The web
forms carry it in a hidden field. Slack buttons carry it in their value. The
TUI takes it from the loaded row. The CLI prints the request and uses the
version that it printed. A web slip shows the first 6 characters of the
digest as the version. Its forms carry the whole version. Every view where
an approver decides shows the exact budget and lifetime that the version
binds. These views are the CLI prompt, the TUI dialog, the web queue slip,
Slack, and the notices. A lifetime that is not a whole number of days shows
in hours, for example `36 hours`, never as `1 day`. The TUI grid keeps its
compact money format, for example `$1.3k`.

Approve, deny, withdraw, and edit refuse a version that no longer matches,
and the refusal tells the actor to look again. A refusal also drops the
cached inventory, so the next view reads the queue live. The web UI shows the
current queue. A refused decision marks its slip as changed and names the
values that changed. The same check stops an edit form that was filled
before another admin's edit from undoing that edit. Within one process the
check is exact. Across processes a gap of about one AWS round trip stays
open between the check and the write, because AWS tags have no
compare-and-swap (a write that fails when the value changed since the read).
Only an upgraded CLI, TUI, or `serve` checks the version. The record format
did not change, so an older binary still reads the queue and acts by name
alone. Every operator must upgrade the CLI and TUI before all decisions are
bound to a version.

Guardrails applied at request time: the name must be free, the owner must be
an Identity Center user when `--permission-set` is set, one person may hold
at most `--max-per-owner` open accounts plus pending requests (default 1),
and a request may ask for at most `--max-ttl` (90d) and `--max-budget` ($500).
Only admins can exceed the two ceilings: the override checkbox in the web
form, or `--override-limits` on the CLI. The override is recorded on the
request, shown to approvers, and carried into the account when approved.
Approval applies the configuration of the process that approves, not the
configuration of the process that queued the request. Without an override,
approval checks the ceilings again against its own `--max-ttl` and
`--max-budget`. It derives the root email from its own `--email-pattern`. It
looks up the owner in Identity Center only when its own `--permission-set` is
set. Run the CLI and TUI with the same `PLAYPLACE_*` environment as `serve`.
Otherwise an approval from the CLI or the TUI can create an account with the
default root email pattern, `aws+pp-{name}@example.com`.

The lifetime ceiling holds after approval too. An extension may not push an
account's expiry past `--max-ttl` counted from when it was created, so
repeated extensions cannot walk around the limit. Admins may go past it: in
the web UI every admin extend is an override, on the CLI and in the TUI it
is `--override-limits` or the override field in the form. Each extension is
written to history with whether the ceiling was overridden.

Who may approve:

- in the web UI, emails in `--approvers` and `--admins`
- in Slack, the same people, identified by their Slack email
- on the CLI and in the TUI, whoever holds management credentials; their
  identity is recorded from `PLAYPLACE_OPERATOR` or the local user and host,
  and it is also the requester of anything they queue, even for someone else

Admins can edit a pending request before approving it: owner, days, budget,
and purpose, from the Edit button in the web UI or `playplace edit-request`.
The admin who edits a request can then approve it alone, also after an edit
that sets the limit override. This is an accepted risk. Admins are trusted,
history records the edit, and the *edited* notice lists each change.
Self-approval is refused unless `--allow-self-approval` is set: nobody may
approve a request they asked for or would own. A requester can withdraw
their own request from the web UI, with `playplace withdraw`, or with `w` in
the TUI; admins and operators can withdraw any. `create` on the CLI skips
the queue for operators and records them as approver; it and `approve` do
not apply the per-owner limit, which is checked when a request is queued.
`create` refuses a name that a pending request holds. Deny or withdraw the
request first, so that its journey ends with a recorded decision. When
playplace places an account, it removes only the approved record of the same
journey. It never removes a record that only has the same name.

The edit forms in the web UI and the TUI hold the exact stored values, and
they send only the fields that the editor changed. An untouched lifetime or
budget keeps its exact value, even when it is not a whole number of days or
dollars. The web form shows such a lifetime as part of a day, for example
1.5, and the TUI form shows it in hours, for example `36h`. Both forms also
show it in words next to the field, for example `now 36 hours`.

### Slack approvals (optional)

With a Slack app configured, each request submitted in the web UI also posts
a message with Approve and Deny buttons to a channel. Requests queued from the
CLI or the TUI do not post a message. The setup is in
[Configuration](docs/configuration.md#slack).

A click is verified with the signing secret, the clicker's Slack email is checked against
the approver list, and the message is rewritten with the outcome. Approving in
Slack and approving in the UI act on the same queue entry. The message shows
the exact budget, the exact lifetime, and the limit override, because its
buttons bind to them. playplace does not update the message when someone
edits, withdraws, or decides the request elsewhere. The buttons carry the
request version that the message shows. After any of those changes,
playplace refuses a click with a private reply. The reply starts with "Not
approved." or "Not denied.", and then gives the reason in the words of the
web UI. A request that changed gets a reply that points to the web UI,
because the message never shows the new values. Buttons from an older
playplace carry only the name, and playplace refuses them with a reply that
points to the web UI.


## Account readiness and initial handoff

Placement in the Playground OU is not readiness. One readiness module in
`internal/core/readiness.go` advances accounts from creation polling and refresh:
observed budget protection, optional owner access, then initial handoff. Placement
history is recorded independently of protection/access success. Lifecycle status
stays derived as before; an `active` account can still be awaiting readiness.
Settling protection is not a failure. Creation polling returns the placed
account, and `approve` and `create` print what it still waits for.

An initial handoff requires an unexpired ACTIVE account with no close intent,
configured protection without an active budget restriction, and an identified
owner. With managed access enabled, the owner grant must succeed and its principal
must be recorded. Otherwise free-text owners are accepted, except blank values
and the placeholders `unknown` (case-insensitive) and `?`. Pending protection,
failed grants, and unidentified owners are retried without delaying expiry or
closure. Existing access is not revoked solely because readiness is lost.

New creation requests carry `playplace:handoff=placing` in their original tags,
so interruption before OU placement does not lose eligibility. The same tag
is the only permission to place: see
[Placement and the organization](#placement-and-the-organization). After placement,
`pending` is persisted before the best-effort placement history write. Newly
adopted accounts start at `pending` in the same write that marks them managed.
Both paths resume through refresh, including after a move succeeded but a tag
write failed. Already-managed accounts without this tag receive no retroactive
handoff notices, even when invalid lifecycle tags need repair; their protection
and access still reconcile normally.

Once readiness is observed, `complete` is persisted before attempting the existing
*ready* notice. A failed completion write fails reconciliation and
retries; a failed notification, or a crash after completion was persisted, may
lose the notice rather than repeat it. History and notification delivery remain
best-effort and are never consulted as state. Repeated polling, restarts, and later
budget/access recovery do not reset the milestone. Keep the single-writer rule;
this is not a transactional or exactly-once delivery guarantee.

## Owner access

With `--permission-set NAME_OR_ARN` (or `PLAYPLACE_PERMISSION_SET`), owners
must be IAM Identity Center users, given by email or user name. When an
account is placed in the OU the owner is assigned that permission set on it,
so the account appears in their access portal. The grant is recorded in the
`playplace:access` tag and removed when the account closes. Accounts adopted
from the OU get the same treatment when their owner tag resolves to a user.
Without the flag, owners are free text and nothing is granted.

Identity Center must be enabled in the management account, and the
permission set must already exist; create one such as `PlaygroundOwner` with
the policies your sandboxes allow. The Identity Center calls go to the
default region, so `--aws-region` (or the profile's region) must be the
Identity Center home region.


## Web UI and sign-in

`playplace serve` runs the web UI and a refresh pass every minute. Sign-in is
OpenID Connect against any provider, with a preset for Google:

    export PLAYPLACE_OIDC_CLIENT_SECRET=...
    playplace serve --oidc-preset google --oidc-client-id ... \
      --oidc-redirect-url https://playplace.example.com/auth/callback \
      --allowed-domain example.com --admins admin@example.com \
      --approvers lead@example.com --base-url https://playplace.example.com

Any other provider works with `--oidc-issuer https://your-idp/...`. The
verified email from the ID token becomes the owner of everything the person
requests. Engineers see and manage only their own accounts and requests.
Emails in `--approvers` see the queue and can approve and deny. Emails in
`--admins` see the fleet, can request on someone's behalf, edit pending
requests, override the TTL and budget ceilings, and run the sync. With
`--permission-set` set, the signed-in email must exist in Identity Center,
which is what makes the access grant land on the right person.

Sign-in turns on with `--oidc-issuer` or `--oidc-preset`. Without either, the
UI has no sign-in and every visitor is an admin. That mode is for local
development only and prints a warning. Any other sign-in flag without an
issuer makes `serve` refuse to start, so a lost issuer setting cannot open the
UI to everyone.

The UI ships its own copy of htmx and idiomorph and serves every page with a
Content-Security-Policy that allows scripts, styles and fonts only from
itself, so nothing is loaded from a third-party host. The policy allows no
inline style, inline script or inline event handler. Page behaviour lives in
`internal/web/static/app.js`, and every style lives in
`internal/web/static/app.css`. The fonts are self-hosted woff2 files under
`internal/web/static/fonts`.

`app.js` holds the htmx morph extension. The extension uses idiomorph to
merge each answer into the page, so focus, scroll and open forms stay. Every
page has one live region, the `#notice` element with `aria-live="polite"`.
On the home page of an approver it sits in the bulk bar at the top of the
approval queue. Above 640 px the bar sticks to the top of the window.
`app.js` keeps the `--bar-h` property equal to its height, so the page
never scrolls a focused control under the bar. `app.js` writes the property
into one rule that it adds to `app.css`, because the CSP allows no inline
style. On the home page of anyone else the region sits in Your requests,
under its heading. Every other page holds it at the top of `<main>`.

Each action writes a short result message into the region. A refusal shows
under the form, in the bar or in its slip, and writes a hidden copy of the
same text into the region. While a request runs, its button sets
`aria-busy` and shows its verb in progress, for example Approving.

When an answer takes the focused control out of the page, `app.js` moves
the focus. After Edit, it goes to the first field of the edit form. After
Save or Cancel, it goes to the Edit button. After Approve, Deny or
Withdraw, the slip no longer waits. The focus then goes to the same button
in the next waiting slip, then in the one before it, then to the queue
heading. When a poll takes the focused slip away, the focus goes to the
name of the next waiting slip, never to Approve or Deny. If the focused
slip left, the region says why. After an action, `app.js` writes the
message of the region again once the focus moved, so a screen reader hears
both.

The home page polls `GET /accounts` every 15 seconds. The poll sends the
`X-Poll-Hash` header with the hash of what the page shows. If nothing
changed, the server answers 204 No Content and the page stays as it is.

The queue lists each request as a slip, oldest first. The name opens the
slip in place with every deciding fact. The server renders every slip
closed, with three exceptions. They are the slip that the address names
(`?req=`), the slip that Cancel puts back, and the slip that an admin just
saved. The morph extension keeps the open state that the viewer chose,
because the server does not know it.

Notices and Slack link to `BASE/?req=<name>#req-<name>`. A page visit
without a session passes its path and query to the sign-in as `next`. The
flow cookie carries it, and the callback returns there when it is a path on
this host. `app.js` opens and focuses the slip that the address names. It
then drops the name from the address, so a reload shows the queue. If the
queue does not list that name, the server writes the reason into the
refusal slot of the queue.

A page never shows the raw text of a failed call to AWS, because that text
holds AWS request IDs. Core returns the error of every provider call in a
`core.ProviderError`. Its text stays the same, so the log, the CLI and the
TUI do not change. The web UI and the Slack replies say "The call to AWS
failed. Try again in a minute. If it fails again, ask an admin to read the
playplace log." For a throttle or a time-out they say "AWS did not answer in
time. Try again in a minute." The failed panel of the account list leaves
out "Try again in a minute", because the page tries again by itself. The
server log holds the raw error with the path, at the warn level. A stale
request, a self-approval, a request that is no longer waiting, and the
per-owner and lifetime limits get sentences of their own. Other refusals of
core keep their words, written as sentences. Core also stores the raw error
of a failed budget call, in the budget error tag and in the history details.
A page shows the same failure text in its place. A notice has a safe text of
its own, as [Lifecycle notices](#lifecycle-notices) describes.

Approve and Deny in the web UI go through one decide function. `POST
/requests/decide?action=approve` or `?action=deny` takes a list of requests.
Each `req` field holds a request name and its version, in the same form as
the value of a Slack button. The buttons of one row, `POST
/requests/{name}/approve` and `/deny`, call the same function with one
request. The checks run in order: the role of the viewer, then the form,
then the reason of a denial, then core. The function reads the form only
through a cap of 256 KB, and a larger body gets 413. A form with an unknown
action or with no request gets 400 and decides nothing. So does a form with
more than 10 approvals or 50 denials, or with a request that does not parse
or whose name is not a valid request name. The function drops a second
request with the same name. It decides the requests in queue order, oldest
first. In the web UI, nobody can approve or deny a request that they asked
for or that names them as the owner, unless `--allow-self-approval` is set.
The function applies this rule to the version that the page showed. A
request of another version changed, so core refuses it as changed.

A denial in the web UI needs a reason of 1 to 500 characters. The reason
goes into the notice, Slack and history, so playplace folds its line breaks
and runs of spaces into single spaces. The reason cannot hold control
characters or text direction characters. A notice in Slack shows Slack
markup as plain text, so a reason cannot mention the whole channel.

A decision on one request runs to its end, also when the browser goes
away. A call that is cut can write the claim in AWS while playplace sees
an error, and then nothing puts the request back. Only a call that AWS
never answers ends, after 2 minutes. The AWS SDK sets no time limit on an
answer, and core holds its lock for the whole decision. Without this limit,
one hung call stops every other decision and the refresh pass. Core
restores a failed approval with a time limit of its own.

Each approval runs a forced inventory and CreateAccount, and a common proxy
cuts an answer at 60 seconds. So before each request, the function checks a
budget of 45 seconds, counted from the start of the post. After the budget,
or after the first failed call to AWS, it does not try the rest. Any other
refusal of one request never stops the others. When the server stops, it
waits up to 60 seconds for the answers in flight. A Slack click answers
Slack at once and decides after the answer, so the stop also waits for those
decisions, in the same 60 seconds. So a stop does not cut a decision. Give
the server at least 60 seconds to stop. [The deployment
guide](deploy/README.md) names the setting for ECS, Kubernetes and systemd.

The region says how many requests went through. A request that went
through keeps a slip with its stamp, the version and what happens next. A
request that did not shows the reason in its slip. A request that left the
queue keeps a slip in its old place with the reason. Each slip posts the
time of its request as a shown field, which gives that place. A result with
no place shows in the bulk bar.

The answer carries the poll hash of what it shows, results included, so the
next poll redraws the queue without them. If the account list fails after
the decisions, the answer still holds every result. The server log has one
line at the info level for each request, with the action, the name, the
version and the result.

The server needs management-account credentials, because AWS only allows
CreateAccount and CloseAccount from there. Run it in the management account
under a role limited to Organizations, Budgets, Cost Explorer, the two
Identity Center APIs, and, with `--history cloudwatch`, its log group.


## History

**Redesign in progress:** [Lifecycle history redesign](docs/design/lifecycle-history.md)
tracks the implemented identity/pagination checkpoint and remaining work. Its
[architecture decision](docs/adr/0001-dedicated-lifecycle-history.md) defines the
future shared store, delivery guarantees, historical visibility, and retention.
The no-database principle still describes the current runtime.

Lifecycle operations attempt best-effort writes to a history sink: requested,
edited, approved, denied, withdrawn, placed, access granted, extended, warned,
expired, close requested, closed, adopted, failed, and budget/closure health
changes. Successful writes record when, who, a channel when supplied, and a
sentence saying what happened, with details such as a deny reason or an edit diff.
New events have opaque IDs; request records carry a journey ID through approval
into account tags. Web/TUI account timelines use that identity, never names.
Legacy accounts without one use a provider-account-ID origin for new events;
uncorrelated old events remain available only through trusted CLI searches.

    playplace history dev-alex          # all journeys with this name, newest first
    playplace history --journey ID --oldest-first
    playplace history --owner alex@example.com --since 30 --actor lead@example.com
    playplace history --limit 20 --json  # page plus next cursor and completeness

`--history file` (default) appends JSON lines to `~/.playplace/history.jsonl`
on the machine running the command, so nothing depends on AWS. `--history
cloudwatch` writes to a CloudWatch Logs group (`--history-log-group`, default
`/playplace/history`) so every operator, the web UI, and the worker can share one
record. Creation of a new group attempts to set one-year retention; existing
group retention is not reconciled. `--history none` turns it off. These modes
remain until the DynamoDB replacement is implemented, then CloudWatch/disabled
modes will be removed without a migration project.

CLI searches support owner, actor, event, journey/account IDs, and date ranges.
Web account pages support order reversal, expandable details, and next-page
links. The TUI detail view scrolls through loaded history and fetches older pages;
`r` reloads it after a failure or to pick up new events. Cursors bind their query
and order, and backend errors/damaged records are distinct from empty history.
The current file and CloudWatch readers scan available matching records; there
is no silent 5,000-event cap, but shared-store performance is not yet established.
The tool never reads history to decide account lifecycle state.


## The mark

The name comes from the indoor playgrounds fast-food restaurants used to
have: a ball pit, tube slides, primary colours. The mark is that idea moved
to the cloud: a soft white cloud whose lower half is a ball pit, with a red
tube slide coming out from under the cloud's top edge and opening over the pit. It borrows the feel, not the trademark;
there are no arches and it copies no existing logo.

| file | use |
|---|---|
| `internal/web/static/logo.svg` | the first mark, kept until the separate mark change. The web header now draws the line mark from the icon sprite in `internal/web/views.templ` |
| `internal/web/static/logo-wordmark.svg` | mark plus the word, for docs and slides |
| `internal/web/static/favicon.svg` | the mark on a sky tile, simplified so it reads at 16 px: bigger balls, a heavier slide without seams |
| `internal/web/static/favicon.ico`, `favicon-32.png`, `apple-touch-icon.png` | rasters of the favicon, generated with `rsvg-convert` and ImageMagick |
| `docs/assets/banner.svg`, `banner.png` | the hero: mark, wordmark, and tagline on a sky panel, for the README and the docs home page |
| `docs/assets/` | also copies of `logo.svg`, `logo-wordmark.svg`, and `favicon.svg` for the documentation site |

Colours: cloud outline `#8FB8E8`, balls `#E63946` red, `#FFC531` yellow,
`#2E86DE` blue, `#2DBE6C` green, and `#6C4DF2`, the TUI's primary purple.
The wordmark colours "play" purple and "place" near-black.

The TUI carries the same mark in block characters: a cloud of `█` with a
row of `●` in the pit colours and the slide as a red diagonal of `█` ending
in a `▓` mouth, in `internal/tui/banner.go`. It shows while
the first load runs, when there are no accounts, and at the top of the `?`
overlay, and the header starts with three balls. Glyphs alone carry the
picture, so NO_COLOR terminals still get a cloud and dots.

To regenerate the rasters after editing an SVG:

    cd internal/web/static
    rsvg-convert -w 32 -h 32 favicon.svg -o favicon-32.png
    rsvg-convert -w 180 -h 180 favicon.svg -o apple-touch-icon.png
    for s in 16 48; do rsvg-convert -w $s -h $s favicon.svg -o /tmp/f$s.png; done
    magick /tmp/f16.png favicon-32.png /tmp/f48.png favicon.ico
    cp logo.svg logo-wordmark.svg favicon.svg ../../../docs/assets/
    rsvg-convert -w 2400 -h 800 ../../../docs/assets/banner.svg -o ../../../docs/assets/banner.png

## TUI design notes

What to know before changing `internal/tui`. See [the README](README.md#cli-and-terminal-dashboard)
for usage and [Development](DEVELOPMENT.md#tui) for local testing. This section
records the Charm v2 facts and design rules behind the code.
Checked against bubbletea v2.0.9, lipgloss v2.0.6, bubbles v2.2.1 with `go doc`.

### Charm v2 facts that shape the code

- `View()` returns `tea.View`. Set `AltScreen`, `WindowTitle`, and `Cursor`
  on it. There is no layers field on `tea.View`; compose overlays in Lip Gloss
  and put the string in `Content`.
- Overlays: `lipgloss.NewCompositor(lipgloss.NewLayer(page), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()`.
  Layers draw opaque cells, so a dialog clears what is under it. See `overlay`
  in `render.go`.
- Light or dark: `Init` returns `tea.RequestBackgroundColor`; handle
  `tea.BackgroundColorMsg` and call `IsDark()`. Colors come from
  `lipgloss.LightDark(isDark)(light, dark)` in `theme.go`. Rebuild bubble
  styles on receipt: `help.DefaultStyles(isDark)` and
  `textinput.DefaultStyles(isDark)` take the flag.
- Keys: match `tea.KeyPressMsg` with `key.Matches(msg, binding)`. One key map
  in `keys.go` feeds both the hint bar (`ShortHelpView`) and the `?` overlay
  (`FullHelpView`).
- Text input cursor: `textinput.Model.Cursor()` is relative to the input.
  Offset it by the input's position on screen and assign to `View.Cursor`, or
  no cursor shows. See `View()` in `tui.go`.
- The bubbles table renders its body through a viewport whose width defaults
  to zero, so rows vanish while the header still draws. We render the table
  by hand (`tableView` in `render.go`) for that reason and for per-cell color.
- Width math uses `github.com/charmbracelet/x/ansi` (`StringWidth`,
  `Truncate`), never `len()`.
- Color output goes through `colorprofile`, which honors `NO_COLOR`. Status
  glyphs carry meaning without color for that reason.

### Toasts and notifications

Everything the TUI has to say arrives the same way: action results, and
the warnings and errors the service logs while it works (a budget AWS would
not create, a grant that failed, a sync error). Lifecycle notices log at
Warn, so they arrive there too. They are routine news, so the TUI shows
them as info with their subject and body, not as warnings. Each message
shows as a toast in the top right for six seconds, wrapped to a few lines,
then ages out. `N` opens the notifications window with the full text of
every message this session, newest first, scrollable; `c` there clears it.
The header shows how many arrived since the window was last open.

Nothing may write to the terminal while the TUI runs, because the
alternate screen has no scrollback and stray output draws over the frame.
`tui` therefore swaps the service logger, the log notifier, and the
CloudWatch history logger for `tui.Feed`, a `slog.Handler` that shows
records at Warn and above in the TUI and drops the rest.

### History

When a history sink is configured, the bottom pane's right column lists the
selected account's most recent events instead of daily spend, and the full
detail screen shows daily spend on the left and history on the right. Events
are fetched through a `tea.Cmd` and cached by journey identity. Actions, sync,
and manual reload clear the cache; timer inventory reloads do not. Full detail
supports up/down and page keys to browse every event, fetching older pages as
needed. Read failures are visible and retryable rather than left loading.

### Layout

Header, section tabs, table, and a detail pane under the table when the
terminal is 22 rows or taller (about 40 percent of the main area, capped at
11 rows). Below that, Enter opens the detail full screen. Columns drop as the
terminal narrows: ACCOUNT first, then OWNER, then the spend bar.

### Color and glyph semantics

| State | Glyph | Color role |
|---|---|---|
| pending (awaiting approval) | `◇` | warning |
| active | `●` | success |
| active, 7 days or less left | `●` plus `!` | warning |
| active, 1 day or less left | `●` plus `!` | error |
| expiring (*expiring* notice sent) | `●` plus `!` | warning or error by time left |
| creating, closing | `◌` | busy |
| creating with a placement note | `◌` plus `!` | warning |
| closing with an overdue warning, unavailable | `!` | error |
| failed | `×` | error |
| closed | `○` | muted |

Spend bar: success below 70 percent of budget, warning to 90, error above.
Unknown spend shows as `unavailable` with no bar, and the header total says
how many open accounts it leaves out. The TUI does not pull a closed
account, so a closed account with no spend shows a muted `— / $200`.
Two border treatments only: rounded on dialogs and forms, `─` rule for the
pane. Selection is a `▌` bar plus a subtle background on every cell.

### Rules

- Keep all IO in `tea.Cmd`s, never in `Update`.
- Dialogs default to the safe button and ignore keys for 150 milliseconds
  after opening so a queued keystroke cannot confirm them.
- Destructive dialogs say exactly what happens and to which account id.
- `a`, `d`, and `w` act only on pending rows; `x` only on real accounts. `e`
  edits the request on a pending row and extends on a real account. Each
  refuses with a flash rather than opening a dialog on the wrong row type.
- History is cached per journey and cleared after an action, a sync, or `r`,
  not on the timer, so the timer never re-reads the history sink.
- Empty, loading, and error states each have a line of text in the table body.
- Never print outside the frame. Warnings go through the notice feed, and
  action outcomes through `setFlash`, which is a notice too.
- Cost Explorer bills per call. Costs come from the service's hourly cache.
  Only `S` (sync) and `r` (reload) force a pull. After a failed pull, the
  timer waits five minutes before it pulls that account again.

### Checking a frame

`playplace tui --snapshot 120x30 --keys "x"` prints one styled frame with
live data after pressing the given keys (`enter`, `esc`, `tab`, or
characters). Pipe it through `freeze --language ansi --font.family Menlo` for
a PNG. Keys sent by the snapshot land inside the dialog grace window, so
forms render empty; that is expected.
