# Command line

The CLI runs with management-account credentials from the usual AWS
credential chain. Every command first runs a refresh pass unless you pass
`--no-refresh`. Lifetimes are in days.

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
    playplace close ACCOUNT [-y]
    playplace costs [ACCOUNT] [--days N]
    playplace history [ACCOUNT] [--owner EMAIL] [--since DAYS] [--limit N]

`ACCOUNT` is a name, an AWS account id, or a prefix of either. `list` hides
closed accounts unless `--all`. `costs` pulls Cost Explorer once an hour per
account and serves the cached numbers in between, because Cost Explorer
bills per call; a failed pull shows as `unavailable`, never as `$0.00`.
`extend` refuses an expiry that
would give the account a lifetime past `--max-ttl`; `--override-limits`
allows it and the override is recorded in history.

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
`reconcile` runs one refresh pass and prints what changed; schedule it daily
for the days nobody opens the UI. `serve` runs the web UI. `tui` opens the
terminal dashboard; press `?` there for its keys.

Who you are is taken from `PLAYPLACE_OPERATOR`, or your local user and host,
and recorded on everything you request, approve, or change. A request you
queue for someone else still names you as the requester, so you cannot
approve it yourself.
