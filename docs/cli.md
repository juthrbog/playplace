# Command line

The CLI runs with management-account credentials from the usual AWS
credential chain. Lifecycle commands first run a refresh pass unless you pass
`--no-refresh`; read-only history searches do not refresh. Lifetimes are in days.

## Requests

    playplace request NAME --owner EMAIL [--ttl DAYS] [--budget USD] [--purpose TEXT]
    playplace requests
    playplace edit-request NAME [--owner EMAIL] [--ttl DAYS] [--budget USD] [--purpose TEXT | --clear-purpose]
    playplace approve NAME [--no-wait]
    playplace deny NAME [--reason TEXT]
    playplace withdraw NAME

`request` queues a request for an approver. `approve` creates the account and
waits for AWS unless `--no-wait`. `withdraw` pulls a request back without a
notice to approvers. Add `--override-limits` to `request` or `edit-request`
to go past the lifetime and budget ceilings; an edit that does not mention
the flag leaves an existing override in place, and `--override-limits=false`
removes it.

## Accounts

    playplace list [--all] [--owner EMAIL]
    playplace show ACCOUNT
    playplace extend ACCOUNT --by DAYS | --until YYYY-MM-DD [--override-limits]
    playplace budget ACCOUNT --amount USD --reason TEXT [--override-limits]
    playplace close ACCOUNT [-y]
    playplace costs [ACCOUNT] [--days N]

`ACCOUNT` is a name, an AWS account id, or a prefix of either. `list` hides
closed accounts unless `--all`. `costs` pulls Cost Explorer once an hour per
account and serves the cached numbers in between, because Cost Explorer
bills per call; a failed pull shows as `unavailable`, never as `$0.00`.
`extend` refuses an expiry that
would give the account a lifetime past `--max-ttl`; `--override-limits`
allows it and the override is recorded in history.

`budget` changes an existing account's recurring monthly limit. In enforcement
mode, an increase above reported spend can reverse the specific restriction and
reset its action, rearming enforcement for the new amount. This is asynchronous:
“change saved; reconciliation pending” means the intent is durable. `list` and
`show` include protection health. See [Budget protection](budgets.md).

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
`create` is the escape hatch for operators; it records you as approver and,
like `approve`, does not apply the per-owner limit, which is checked when a
request is queued.
`reconcile` runs one refresh pass and prints what changed. Schedule it independently
with external serialization against other writers (for example, every 15 minutes
when no service worker is running) and alert on failed or missed runs.
`serve` runs the web UI and, by default, refreshes every minute. `tui` opens the
terminal dashboard; press `?` there for its keys.

Closure is asynchronous: `closing` remains visible until AWS explicitly reports
`CLOSED`. `unavailable` means suspension, pending activation, or an unknown state,
not confirmed closure. `show` includes the provider state, request time, and
closure observation time. After `--close-alert-after 24h` without confirmation,
`reconcile` returns nonzero and attempts a one-shot lifecycle notification. It
continues monitoring and retrying; operators must investigate rather than assume
that submitting a close request stopped charges. See the [deployment safeguards](deploy.md)
for the separately deployed purchase/trail SCP and existing-account checks.

Who you are is taken from `PLAYPLACE_OPERATOR`, or your local user and host,
and recorded on everything you request, approve, or change. A request you
queue for someone else still names you as the requester, so you cannot
approve it yourself.
