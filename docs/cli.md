# Command line

The CLI runs with management-account credentials from the usual AWS
credential chain. Lifecycle commands first run a refresh pass unless you pass
`--no-refresh`; read-only history searches do not refresh. Lifetimes are in days.

## Requests

    playplace request NAME --owner EMAIL [--ttl DAYS] [--budget USD] [--purpose TEXT]
    playplace requests
    playplace edit-request NAME [--owner EMAIL] [--ttl DAYS] [--budget USD] [--purpose TEXT | --clear-purpose]
    playplace approve NAME [-y] [--no-wait]
    playplace deny NAME [--reason TEXT]
    playplace withdraw NAME

`request` queues a request for an approver. `approve` prints the request:
owner, budget, lifetime, who asked, the purpose, and any limit override. Then
it asks you to confirm with `y`. `-y` skips the question. Scripts must pass
`-y`, because `approve` stops when it cannot read an answer. If you asked for
the request or are its owner, `approve` refuses before it asks. After you
confirm, `approve` creates the account and waits for AWS unless you pass
`--no-wait`. The approval applies to the values that `approve` printed. If
someone edits or replaces the request before you answer, `approve` refuses,
and you run `approve` again.

Approval applies the configuration of the process that approves. This
includes `--email-pattern`, `--permission-set`, `--max-ttl`, and
`--max-budget`. Run the CLI with the same `PLAYPLACE_*` environment as
`serve`. Otherwise `approve` can create an account with the default root
email pattern.

`deny` and `withdraw` print the request that they acted on. `withdraw` pulls a
request back without a notice to approvers. Add `--override-limits` to
`request` or `edit-request` to go past the lifetime and budget ceilings; an
edit that does not mention the flag leaves an existing override in place, and
`--override-limits=false` removes it.

The wait in `approve` and `create` ends when AWS places the account in the
Playground OU. The account can then still wait for its initial handoff. With
budget enforcement, AWS shows a new budget action only on the next pass, so
the command prints `NAME is not ready yet: budget protection is setup-pending`
and still succeeds. A later refresh continues readiness. The *ready* notice
follows when the owner is identified and, if `--permission-set` is set, has
access. The same line names a missing owner or a missing owner
grant when that is what the account waits for.

AWS can take a few more minutes to activate a new account after it creates
it. During that time AWS shows the account state as `PENDING_ACTIVATION`,
and playplace does not place the account. The wait prints
`NAME (account ID) waits for AWS to activate it` and keeps polling. For one
hour after AWS created the account, it shows as `creating`. After that hour
it shows as `unavailable`, and a wait that still runs stops with an error.
Investigate such an account with AWS.

A step after the creation can fail, for example the budget setup or the
owner grant. Then the wait stops with an error, and the command exits with
a non-zero status. The error says that AWS created the account and gives
the account ID. It also says whether playplace placed the account in the
Playground OU, and it ends with the cause:

    error: dev-one was created as account 123456789012 and placed in the Playground OU. The next refresh retries the step that failed: budget for dev-one: ...

The next refresh retries the step that failed. It also retries a placement
that did not finish, unless someone moved the account to another OU. If the
new account has no playplace creation tags, for example because an emulator
dropped them, the error says so, and no refresh places it. Move or tag the
account by hand. When AWS cannot create the account, the error gives the
reason from AWS. playplace records the failure in the history and sends the
*creation failed* notice. See [Notices](configuration.md#notices) for when
the notice can be lost or sent twice.

## Accounts

    playplace list [--all] [--owner EMAIL]
    playplace show ACCOUNT
    playplace extend ACCOUNT --by DAYS | --until YYYY-MM-DD [--override-limits]
    playplace budget ACCOUNT --amount USD --reason TEXT [--override-limits]
    playplace close ACCOUNT [-y]
    playplace costs [ACCOUNT] [--days N]

`ACCOUNT` is an AWS account id, the name of an open account, or a prefix of
an id. playplace tries them in that order, so an exact id always finds its own
account. `list` hides closed accounts unless `--all`. `costs` pulls Cost
Explorer once an hour per account and serves the cached numbers in between,
because Cost Explorer bills per call; a failed pull shows as `unavailable`,
never as `$0.00`.
`extend` refuses an expiry that
would give the account a lifetime past `--max-ttl`; `--override-limits`
allows it and the override is recorded in history.

`budget` changes an existing account's recurring monthly limit. In enforcement
mode, an increase above reported spend can reverse the specific restriction and
reset its action, rearming enforcement for the new amount. This is asynchronous.
The command prints the protection state, for example `recovering`, and later
passes finish it. An error that starts with “budget change saved” means that
the intent is durable but the protection update failed. Reconciliation retries
it. `list` and `show` include protection health. See
[Budget protection](budgets.md).

## History

    playplace history [NAME] --owner EMAIL --actor EMAIL --event approved
    playplace history --journey JOURNEY_ID --oldest-first
    playplace history --account-id ACCOUNT_ID --since 30d --limit 50 --json
    playplace history --since 2026-09-01T00:00:00Z --until 2026-10-01T00:00:00Z

History is newest-first by default. `--oldest-first` reverses the order. Name,
owner, and actor filters are case-insensitive exact matches; journey/account IDs
and event types are exact. Filters combine with AND. `--since` is inclusive and
`--until` exclusive; both accept an RFC3339 timestamp or a positive age such as
`30d`. Names are search labels: use `--journey` to isolate one request-to-account
journey when a name has been reused.

`--limit` selects a page of up to 500 events. When more results exist, text output
prints a next cursor on stderr; use `--cursor` with the same filters and order.
For relative-date searches, reuse the resolved absolute bounds printed with the
cursor rather than recalculating the age. JSON output includes `events`, `next`,
`incomplete`, and the resolved `since`/`until` bounds; event details remain
structured. A cursor is not a fixed snapshot: late-arriving older events can
appear on subsequent pages.

Damaged records produce an explicit incomplete-history warning. Backend failures
are errors, not empty results. Old records without journey identity remain
available to trusted CLI searches by name, but are not attached to an account's
web/TUI timeline merely because the name matches. These reads do not mutate or
refresh account lifecycle state.

## Operators

    playplace init
    playplace create NAME --owner EMAIL [...]   # skips the approval queue
    playplace reconcile
    playplace serve [--addr :8080] [--every 1m] [--oidc-* ...] [--slack-* ...]
    playplace tui

`init` creates the organization and the Playground OU if they are missing.
It is the only command that creates them. Every other command stops with an
error when the organization or the OU is missing. `--ou-name` finds only an
OU that is a direct child of the organization root. For an OU that is nested
in another OU, set `--ou-id` to the ID of the OU. Do not run `init` to fix
the error when your Playground OU is nested: without `--ou-id`, `init`
creates a second, empty OU under the root, without your SCPs. Run `init`
without `--ou-id` only when no Playground OU exists yet, and attach the
safeguards SCP to the new OU before you grant access.

With `--ou-id`, `init` creates nothing. It checks that the OU is in the
organization of the AWS profile and prints the name and the ID of the OU.
It also reminds you to attach the safeguards SCP to the OU. An SCP on a
parent OU also applies to every OU and account below it, so you can attach
the SCP to an OU above the Playground OU instead. The SCP then also applies
to every other OU and account below that parent. Attach it there only when
that is what you want.

`init` also lists the active accounts in the OU and the new accounts that
wait in the organization root, with what the next refresh does to each. For
each active account in the OU that playplace does not manage, it prints
what the next refresh does:

- It adopts the account and prints the owner, the budget, and the day when
  the closure starts. The account keeps each valid playplace owner,
  expires and budget tag. Without an owner tag, the owner is `unknown`.
  Without a valid expires or budget tag, the account gets the default
  lifetime or the default budget.
- It starts to close an account with a `playplace:close-requested` tag at
  once.
- It leaves an account with a damaged `playplace:close-requested` tag
  alone until someone repairs the tag.

Then `init` lists the active accounts in the OU that playplace manages, with
the owner and the day when the closure starts. The next refresh resumes
their management. It starts at once to close each one that expired or has
a `playplace:close-requested` tag. If you quarantined accounts in another
OU and named that OU by mistake, they show here. Last, `init` lists the new
accounts of playplace that wait in the organization root. The next refresh
moves each of them into this OU. playplace places an account only once, so
it never moves one to another OU later, even after you correct the OU. A new
account with damaged tags stays in the root until someone repairs the tags,
and `init` lists it apart. If the OU holds no active account and no new
account waits, `init` says so. The next refresh also tracks the closure of
the accounts in the OU that AWS is already closing. `init` does not list
them.

The dates assume that the next refresh runs on the same day. Most commands
start with a refresh, so run `init` before any other command. Check the
name of the OU and the list. If this is the wrong OU, correct `--ou-id` or
`--ou-name` before you run another command. Move every account that
playplace must not manage out of the OU.

`create` is the escape hatch for operators; it records you as approver and,
like `approve`, does not apply the per-owner limit, which is checked when a
request is queued. It refuses a name that a pending request holds. Deny or
withdraw that request first.
`reconcile` runs one refresh pass and prints what changed. Schedule it independently
with external serialization against other writers (for example, every 15 minutes
when no service worker is running) and alert on failed or missed runs.
Every command writes the lifecycle notices it sends to stderr at `warn`, and
`tui` shows them in its notifications window instead. Without `--slack-webhook`,
`serve` warns once at startup that notices go only to its log. `reconcile` does
not warn, because it runs on a schedule and the warning would repeat on every
run. See [Notices](configuration.md#notices).
`serve` runs the web UI and, by default, refreshes every minute. `tui` opens the
terminal dashboard; press `?` there for its keys. In the TUI, `r` (reload) and
`S` (sync) pull costs from Cost Explorer again for every open account, and
Cost Explorer bills each call. The automatic reloads use the hourly cache.

Closure is asynchronous: `closing` remains visible until AWS explicitly reports
`CLOSED`. `unavailable` means suspension, pending activation, or an unknown state,
not confirmed closure. `show` includes the provider state, request time, and
closure observation time. After `--close-alert-after 24h` without confirmation,
`reconcile` returns nonzero and sends the *overdue* notice once. It
continues monitoring and retrying; operators must investigate rather than assume
that submitting a close request stopped charges. See the [deployment safeguards](deploy.md)
for the separately deployed purchase/trail SCP and existing-account checks.

`reconcile` also returns nonzero when budget protection stays settling, that
is `setup-pending`, `restricting`, `recovering` or `retiring`, for more than
two hours. It returns nonzero at once when AWS reports a failed reverse or
reset. See [Budget protection](budgets.md).

Who you are is taken from `PLAYPLACE_OPERATOR`, or your local user and host,
and recorded on everything you request, approve, or change. A request you
queue for someone else still names you as the requester, so you cannot
approve it yourself.
