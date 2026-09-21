<p align="center">
  <img src="docs/assets/banner.png" alt="playplace: short-lived AWS playground accounts, with guardrails" width="760">
</p>

# playplace

Self-service, short-lived AWS accounts for engineering teams. Engineers request
an isolated place to experiment; approvers decide what gets created; playplace
handles account provisioning, access, expiry warnings, and closure monitoring.

Every playground has an owner, a lifetime, a monthly budget, and an approval
trail. Interact through the web UI, a terminal dashboard, or the CLI, with
optional Slack approvals and GitLab workflows.

## What it provides

- **Request and approval workflows.** Request, edit, approve, deny, or withdraw
  accounts. Self-approval is refused by default.
- **Account provisioning and access.** Create accounts in a dedicated AWS
  Organizations OU and, when configured, grant owners access through IAM
  Identity Center.
- **Lifetime and budget guardrails.** Limit requested lifetimes, monthly budgets,
  and accounts per owner. Set up AWS Budgets alerts and view recent spend.
  Optionally deny covered provisioning APIs at 100% actual spend, with admin
  budget increases that lift and rearm the restriction.
- **Expiry management.** Warn owners before expiry, allow bounded extensions,
  request closure when time is up, and flag closures that remain unconfirmed.
  Retire owned budget actions after confirmed closure while retaining billing alerts.
- **Visibility and history.** See account status, spend, pending requests, and
  lifecycle events from the web UI, TUI, or CLI. History can be local or shared
  through CloudWatch Logs.

Defaults are a **14-day lifetime**, **$50 monthly budget**, and **one open account
or pending request per owner**. Requests are normally capped at 90 days and
$500/month; admins can override those ceilings. These are configurable policies,
not hard spending caps.

## How it works

1. **Request.** An engineer supplies a name, lifetime, budget, and purpose.
2. **Approve.** An approver reviews the request before AWS creates the account.
3. **Provision.** Playplace creates the account, places it in the Playground OU,
   applies lifecycle tags and a budget, and grants the configured Identity
   Center permission set.
4. **Use.** The owner accesses AWS through their AWS access portal, watches spend,
   and can extend the lifetime within policy or request early closure.
5. **Retire.** A refresh pass warns before expiry, requests closure at expiry,
   retries refused requests, and checks that AWS actually reports `CLOSED`.
   It then retires the owned budget action to avoid leaving obsolete paid controls.

Playplace runs with **management-account credentials**. AWS Organizations and
resource tags hold its inventory and request queue—there is no application
database to operate. It does not assume roles into playground accounts or
manage the resources engineers create inside them.

`playplace serve` runs the web UI and refreshes every minute by default.
`playplace reconcile` runs the same pass once for scheduled automation. An
expiry tag alone does not trigger AWS closure: keep reconciliation running and
monitor failed or missed runs.

## Using playplace

### Web UI

For engineers, the web UI is the usual entry point—no management-account
credentials are needed:

1. Open your organization's playplace URL and sign in with your work account.
2. Fill in the request form and wait for approval.
3. Once the account is active, open it from your AWS access portal (when
   Identity Center access is configured).
4. Use **+7d** to extend, **Close** to request early closure, or open the account
   page to see its history.

Approvers review the pending queue and approve or deny requests. Admins also
see the fleet, edit requests, change existing account budgets, override limits,
and trigger a sync with AWS.

See [Requesting an account](docs/requesting.md) and
[Approving requests](docs/approving.md) for the full workflows.

### CLI and terminal dashboard

The CLI and TUI are **operator tools**: both require management-account AWS
credentials. The examples below assume `playplace` is on your `PATH` and your
organization's configuration is set.

```sh
# Request an account; a different operator must approve it.
playplace request dev-alex --owner alex@example.com --ttl 5 --budget 20 \
  --purpose "try a new service"
playplace requests
playplace approve dev-alex

# Inspect and manage an account.
playplace list
playplace show dev-alex
playplace extend dev-alex --by 3
playplace budget dev-alex --amount 100 --reason "Approved additional testing"
playplace costs dev-alex --days 7
playplace history dev-alex
playplace close dev-alex

# Open the interactive terminal dashboard.
playplace tui
```

The operator is identified by `PLAYPLACE_OPERATOR`, or the local user and host.
Approval must come from someone other than the requester or intended owner.
In the TUI, `n` requests, `a` approves, `d` denies, `e` edits or extends, `S`
syncs, and `?` shows all keys.

See the [CLI reference](docs/cli.md) and [configuration reference](docs/configuration.md).

### Slack and GitLab

- **Slack:** configured channels receive requests with **Approve** and **Deny**
  buttons. Decisions use the same queue and approver rules as the web UI.
- **GitLab:** optional pipeline jobs let users request accounts and authorized
  approvers approve or deny them, without distributing management credentials.
  A scheduled job can run reconciliation.

See [deployment instructions](deploy/README.md) and the
[Slack setup notes](DESIGN.md#slack-approvals-optional).

## Running your own instance

### Release packages and Homebrew

Release automation is prepared; this setup does **not** publish a release or
install a formula in the tap. Until the first release, build from source below.
Once a stable release has published the formula, installation will be:

```sh
brew install juthrbog/tap/playplace
playplace --version
```

Releases will provide Linux, macOS, and Windows archives for amd64 and arm64,
SHA-256 checksums, and shell completions. Source-based AUR recipes are generated
as release assets; AUR submission remains manual. Installation does not start
any service or make AWS changes. See [Releasing](docs/releasing.md) for validation,
tap credentials, and the publication process.

### Build from source

[Go and Task](mise.toml) are configured through [mise](https://mise.jdx.dev/).
From the repository directory, with mise activated in your shell:

```sh
mise trust
mise install
task tools       # install the templ code generator
task build       # write bin/playplace
task test
```

Use `./bin/playplace` after building, or add `bin/` to your `PATH`. Without shell
activation, run Task through mise, for example `mise exec -- task build`.

### Deploy and configure

Follow the [deployment guide](deploy/README.md) to:

1. Give the service a management-account role with the supplied runtime IAM policy.
2. Configure the Playground OU, account email pattern, budget alert recipient,
   and optional Identity Center permission set. `playplace init` creates the
   organization and OU if missing; run it only in the intended management account.
3. Separately deploy the [purchase and trail SCP](deploy/aws/playground-safeguards-scp.json)
   and review existing account obligations **before granting playground access**.
4. Run `playplace serve` behind HTTPS with OIDC sign-in, approvers, and admins
   configured. **Without OIDC, every visitor is an admin**—that mode is for
   private local development only.
5. Optionally enable [per-account budget restrictions](docs/budgets.md) using a
   separately deployed provisioning SCP and AWS Budgets execution role.
6. Alert on failed or missed refreshes. Use one serialized control-plane writer;
   do not overlap scheduled reconciliation with service/CLI mutations. Configure
   shared history and lifecycle notifications as needed.

For a local walkthrough using LocalStack and Dex instead of real AWS, see
[Development](DEVELOPMENT.md#testing-the-flows-locally).

## Important boundaries

- **Budgets and TTLs are not hard spending caps.** Billing data is delayed, and
  AWS closure is asynchronous and subject to account-age and quota restrictions.
- **A close request is not confirmed closure.** Playplace keeps accounts visible
  while closing and flags unconfirmed closures after `--close-alert-after`
  (default `24h`). Suspension alone is not treated as closure.
- **Closure does not cancel every financial obligation.** Prior usage, existing
  commitments, Marketplace subscriptions, and central logging can still cost
  money. The separately deployed SCP prevents the listed new purchases; it does
  not cancel existing ones.
- **No resource deletion or `aws-nuke`.** Organization audit trails are preserved;
  existing account-owned trails need deliberate operator review.

Read the [billing and closure safeguards](deploy/README.md#billing-and-closure-safeguards)
before using playplace with real accounts.

## Documentation

| Guide | Covers |
|---|---|
| [Requesting an account](docs/requesting.md) | Engineer workflow |
| [Approving requests](docs/approving.md) | Approver and admin workflow |
| [CLI](docs/cli.md) · [Configuration](docs/configuration.md) | Commands, flags, and environment variables |
| [Deployment](deploy/README.md) | IAM, hosting, GitLab, and billing safeguards |
| [Design](DESIGN.md) | Architecture, lifecycle rules, and interface decisions |
| [Development](DEVELOPMENT.md) | Build setup, local testing, and contributor workflows |
| [Releasing](docs/releasing.md) | CI, release snapshots, Homebrew, and AUR packaging |

To browse the documentation site locally, install [uv](https://docs.astral.sh/uv/)
and run `task docs` (http://localhost:8000). `task docs:build` builds it into `site/`.

## License

[Apache License 2.0](LICENSE).
