# Development

Build, test, and work on playplace locally. For service usage, see
[the README](README.md); for implementation rationale, see [Design](DESIGN.md).

## Build

Go, [Task](https://taskfile.dev), and GoReleaser are configured in [mise.toml](mise.toml).
With [mise](https://mise.jdx.dev/) installed and activated in your shell:

    mise trust
    mise install
    task tools   # once, installs templ
    task build   # writes bin/playplace
    task test
    task --list  # everything else

## Release builds

CI builds, vets, and runs race tests on Linux, macOS, and Windows. A separate
Linux job builds and smoke-tests distribution snapshots. Shell completions and
`playplace --version` are safe to generate/check without AWS credentials.

    task completions        # write .generated/completions/
    task release:check      # generated views, tests, vet, GoReleaser checks
    task release:snapshot   # local archives and package recipes; never publish

Snapshot validation also needs bash, tar, and Ruby. GoReleaser's source archive
uses committed files; use the intended commit or a disposable local copy for
pre-commit source-package checks. Generated views are committed so source
packages do not need templ at build time; `scripts/check-generated.sh` checks
that they match the version pinned in `go.mod`.

See [Releasing](docs/releasing.md) for the workflow, Homebrew credentials, and
manual AUR steps. Do not push a version tag unless you intend to publish.

## Documentation site

    task docs        # http://localhost:8000 with live reload
    task docs:build  # writes ./site

Install [uv](https://docs.astral.sh/uv/) for `uvx`; it is not installed by this
repo's mise configuration. The site is built by [Zensical](https://zensical.org) from the
pages under `docs/`: an overview, guides for requesters and approvers, the
CLI and configuration references, and the deployment guide, which includes
[deploy/README.md](deploy/README.md) as it is. Architecture and interface design
notes live in [DESIGN.md](DESIGN.md).

## Layout


    cmd/playplace/          entry point
    internal/core/          domain model, Provider interface, lifecycle Service, request queue
    internal/provider/aws/  Organizations, Budgets, Cost Explorer, Identity Center
    internal/provider/fake/ in-memory provider for tests
    internal/worker/        periodic refresh for serve
    internal/notify/        log and Slack notifiers
    internal/cli/           cobra commands
    internal/tui/           Bubble Tea views
    internal/audit/         history sinks: local file and CloudWatch Logs
    internal/web/           templ views, htmx handlers, OIDC sign-in, Slack adapter
    deploy/                 IAM policy, GitLab pipeline, deployment notes
    scripts/                offline completions, generated-view and archive checks
    tools/package-release/  checksum-verified Homebrew and AUR recipe generation
    .github/workflows/      cross-platform CI and tag-triggered releases

## Offline budget recovery tests

    go test -race ./internal/core ./internal/provider/aws ./internal/provider/fake

The AWS recovery-flow tests enter through `SetBudget` and `Refresh`, using the real
AWS adapter against loopback HTTP with durable mock Organizations tags. They cover
approval-before-update, pre-update spend surviving recalculation, failed reset-phase
writes, restarts, reverse/reset failure retries, re-execution and monthly rollover.
The transition cases assert observable health, persisted intent and submitted AWS
mutations rather than calling the private recovery implementation. The fake adapter
keeps automatic remote progress for lightweight lifecycle tests, but does not decide
recovery eligibility or phases. No AWS credentials or live accounts are used.

## Testing the flows locally

The `task cli`, `task tui`, and `task serve*` workflows run against LocalStack Pro
(AWS) and, for sign-in, Dex (a local OpenID Connect provider). They use test AWS
credentials, not real AWS. Install Docker with Compose and provide a LocalStack
auth token before starting the walkthrough below. Slack and GitLab integration
checks also need their respective external services.

### One-time setup

    cp .env.example .env        # add LOCALSTACK_AUTH_TOKEN
    task localstack:up          # Organizations, Budgets, Cost Explorer, Identity Center
    task localstack:seed        # Identity Center instance, PlaygroundOwner, three users
    export PLAYPLACE_PERMISSION_SET=arn:aws:sso:::...   # printed by the seed step
    task cli -- init

Skip the `export` to run without Identity Center: owners become free text and
no access is granted. If your LocalStack version does not implement Budgets,
placement now reports a budget error and withholds new owner access; it no longer
silently ignores missing protection. You can inspect the account and exercise
closure, but a full ready/access flow requires working Budgets APIs. Use the
fake-provider and loopback AWS API tests for offline budget/action workflows;
real SCP enforcement and reverse/reset require a separately authorized sandbox.

`task cli -- <command>` runs the CLI against LocalStack with test credentials.
Set `PLAYPLACE_OPERATOR=you@example.com` so audit tags name you rather than
your local user and host. The operator is the requester of anything they
queue, so approving needs a different operator: the walk below switches
`PLAYPLACE_OPERATOR` for the approve step.

### CLI: request, approve, lifecycle

    task cli -- request dev-one --owner dev@example.com --ttl 5 --budget 20 --purpose "try bedrock"
    task cli -- requests                       # the queue, read from tags on the OU
    task cli -- request dev-two --owner dev@example.com   # refused: limit of 1 per owner
    task cli -- request big --owner lead@example.com --ttl 120      # refused: max 90d
    task cli -- request big --owner lead@example.com --ttl 120 --budget 900 --override-limits
    task cli -- deny dev-one --reason "smaller budget please"
    task cli -- request dev-one --owner dev@example.com --budget 10
    PLAYPLACE_OPERATOR=lead@example.com task cli -- approve dev-one   # another person; self-approval is refused
    task cli -- show dev-one                   # owner, approved-by, expires, spend
    task cli -- extend dev-one --by 3
    task cli -- extend dev-one --by 200        # refused: past the 90d lifetime ceiling
    task cli -- extend dev-one --by 200 --override-limits
    task cli -- close dev-one -y
    task cli -- reconcile                      # idempotent; prints only what changed

To see the queue and audit tags as AWS stores them:

    aws --endpoint-url http://localhost:4566 organizations list-tags-for-resource --resource-id <ou-or-account-id>

Expiry and warnings depend on time passing. Request with `--ttl 0.05`, approve,
then run `task cli -- reconcile` after at least 72 minutes; or shorten
`--warn-before` to test the warning window.

### History

    task cli -- history                      # everything, from ~/.playplace/history.jsonl
    task cli -- history dev-one              # one account
    task cli -- history --owner dev@example.com --since 7

Add `--history cloudwatch` to any command (or `PLAYPLACE_HISTORY=cloudwatch`)
to write to and read from LocalStack's CloudWatch Logs instead. `--history
none` disables it. Each account page in the web UI shows the same events.

### TUI

    task tui

`n` opens the request form, pending rows show a hollow diamond, `a` approves,
`d` denies, and `w` withdraws behind confirm dialogs, `e` edits a pending
request or extends an account, `S` runs the refresh pass, `N` opens the
notifications window, `?` lists every key. The TUI runs as the operator: requests queued with `n` name the
operator as requester, and it refuses to let that operator approve them,
like every other path. Costs come from the hourly cache; only `r` and `S`
pull from Cost Explorer.

### Web UI without sign-in

    task serve                  # http://localhost:8080

Every visitor is an admin and approver, and `--allow-self-approval` is on so
you can request and approve alone. Use this to check layout and the request,
approve, deny, and withdraw buttons quickly.

### Web UI with sign-in and roles

    task auth:up                # Dex on http://localhost:5556
    task serve:auth             # http://localhost:8080 with OIDC

Sign in as three people, password `password` for all:

| user | role | can |
|---|---|---|
| dev@example.com | engineer | request for themself, see own accounts, withdraw own requests |
| lead@example.com | approver | see the queue, approve and deny (not their own), no fleet view |
| admin@example.com | admin | everything, plus request on someone's behalf and sync |

A useful walk: sign in as dev and request `dev-box`; sign out; sign in as lead
and approve it; sign in as dev again and watch it turn active after the next
refresh. Try approving your own request as lead to see the refusal, and try
`/accounts/<someone-else's-id>` as dev to see the 403. Use a private browser
window per user, or sign out between them.

### Slack

Slack needs a real workspace and a public URL. Create a Slack app with
`chat:write` and `users:read.email`, point its interactivity URL at
`https://<tunnel>/slack/interact` (for example through `cloudflared tunnel`
or `ngrok http 8080`), and run:

    task serve:auth -- --slack-channel <channel-id> \
      --slack-signing-secret $SLACK_SIGNING_SECRET --slack-bot-token $SLACK_BOT_TOKEN

Request an account in the web UI; a message with buttons appears in the
channel. Click Approve as a Slack user whose email is in `--approvers`. The
request signature check and endpoint gating are covered by unit tests, so the
only thing this exercises is the real Slack round trip.

### GitLab

Push the repo, add the CI variables from [the deployment guide](deploy/README.md), protect an
environment named `approvals`, and run the pipeline from the web with
`JOB=request`. The job scripts are the same CLI commands as above, so they can
also be rehearsed locally by exporting `NAME`, `OWNER`, and friends and
running the script lines by hand.

### Unit tests

    task test

Core, web, and TUI tests run against an in-memory fake provider and need no
LocalStack. They cover the state derivation from tags, the approval rules,
limits, access grants, web authorization, cookie signing, and the Slack
signature check.
