# Configuration

Every flag can also be set as an environment variable: `--email-pattern`
becomes `PLAYPLACE_EMAIL_PATTERN`, and so on. Secrets belong in the
environment.

Lifetimes take a bare number of days, `Nd`, or a Go duration such as `72h`.
Intervals and waits (`--every`, `--close-alert-after`, `create --timeout`) need a unit, such as
`30s` or `5m`.

## Accounts

| flag | default | meaning |
|---|---|---|
| `--ou-name` | `Playground` | name of the organizational unit that holds playground accounts. It must be a direct child of the organization root. For an OU nested in another OU, use `--ou-id`. Only `playplace init` creates the OU, and other commands fail when it is missing. Run `init` only when no Playground OU exists yet, because it creates a new, empty OU under the root. The OU must hold only accounts that playplace may close. See [the deployment guide](deploy.md#the-playground-ou) |
| `--ou-id` | | ID of the organizational unit that holds playground accounts, for example `ou-ab12-cd34ef56`. The OU can be at any depth, also nested in other OUs. Playplace reads it by ID and does not use `--ou-name`. Playplace refuses to start when you also set `--ou-name`. `init` never creates an OU for it. The OU must hold only accounts that you want playplace to manage. On the first refresh, playplace takes over every active account in the OU that does not have the `playplace:managed=true` tag. The account keeps each valid playplace owner, expires and budget tag, and gets the defaults for the rest. playplace closes the account when its lifetime ends. It starts the closure at once if the account has expired or has a `playplace:close-requested` tag. Move every other account out of the OU before you set `--ou-id`. Then run `init` before any other command, and check the OU name and the accounts that it lists. See [the deployment guide](deploy.md#the-playground-ou) |
| `--email-pattern` | `aws+pp-{name}@example.com` | root email for new accounts; `{name}` is replaced |
| `--default-ttl` | `14d` | lifetime when a request does not say; must be positive and at most `--max-ttl` |
| `--default-budget` | `50` | monthly budget in USD when a request does not say; at most `--max-budget` |
| `--warn-before` | `3d` | how far ahead of expiry to send the *expiring* [notice](#notices); must be shorter than `--default-ttl`; `0` turns the notice off |
| `--close-alert-after` | `24h` | positive duration before unconfirmed closure triggers an alert and reconciliation error; checked on each refresh |
| `--alert-email` | | single recipient for AWS Budgets alerts; required for enforcement |
| `--budget-scp-id` | | separately deployed provisioning-deny SCP; empty means alerts-only |
| `--budget-action-role` | | management-account role AWS Budgets assumes to apply/reverse the SCP |

Configure both enforcement flags together. See [Budget protection](budgets.md)
for deployment, policy coverage, admin increases and recovery. Without an alert
recipient or enforcement configuration, the CLI warns instead of implying that
budgets stop provisioning.

## Requests and limits

| flag | default | meaning |
|---|---|---|
| `--max-ttl` | `90d` | longest lifetime a request may ask for; admins can override; `0` removes the limit |
| `--max-budget` | `500` | largest monthly budget a request may ask for; admins can override; `0` removes the limit |
| `--max-per-owner` | `1` | open accounts plus pending requests one person may hold; `0` removes the limit |
| `--request-ttl` | `7d` | how long a request waits for approval before it is dropped; must be positive |
| `--allow-self-approval` | off | let requesters approve their own requests |

## Owner access

| flag | default | meaning |
|---|---|---|
| `--permission-set` | | IAM Identity Center permission set, by name or ARN, granted to owners. Empty disables owner validation and access grants |

Before you set `--permission-set`, enable IAM Identity Center in the
management account and create the permission set, for example
`PlaygroundOwner`. Its policies decide what owners can do in their accounts.
playplace does not create it.

With `--permission-set`, every owner must be an Identity Center user, given
by email or user name. playplace refuses a request for any other owner. In
the web UI, the signed-in email becomes the owner, so it must match an
Identity Center user. playplace grants the permission set to the owner when
it places the account, and it removes the grant when the account closes.

The Identity Center and identity store calls use the default AWS region, so
`--aws-region` or the profile must point at the Identity Center home region.

## Web UI and sign-in

These belong to `playplace serve`.

| flag | default | meaning |
|---|---|---|
| `--addr` | `:8080` | listen address |
| `--every` | `1m` | refresh interval, with a unit. Keep it at `30m` or less. Budget protection that is still settling after two hours fails the pass, and a budget recovery can take four passes. See [Budget protection](budgets.md) |
| `--no-worker` | off | serve the UI without the periodic refresh; run `reconcile` elsewhere |
| `--base-url` | | public URL, used in notifications and Slack messages |
| `--oidc-preset` | | `google` fills in the issuer |
| `--oidc-issuer` | | any OpenID Connect issuer URL |
| `--oidc-client-id`, `--oidc-client-secret` | | OAuth client; put the secret in `PLAYPLACE_OIDC_CLIENT_SECRET` |
| `--oidc-redirect-url` | `http://localhost:8080/auth/callback` | callback registered with the provider |
| `--allowed-domain` | | only allow sign-in from this email domain |
| `--admins` | | comma-separated emails with fleet-wide rights |
| `--approvers` | | comma-separated emails that may approve and deny |
| `--session-secret` | random | HMAC key for session cookies; set `PLAYPLACE_SESSION_SECRET` so sessions survive restarts |

Sign-in turns on when `--oidc-issuer` or `--oidc-preset` is set. Without
either, the UI has no sign-in and every visitor is an admin. Use that only on
a private network. If any other sign-in flag in this table is set without an
issuer, `serve` refuses to start, so a lost issuer setting cannot open the UI
to everyone.

Sign-in rejects an ID token whose `email_verified` claim is `false`. A token
without the claim is accepted, which suits Google and most workplace
providers; if your issuer can mint tokens for unverified addresses, check
that at the provider.

## Slack

| flag | meaning |
|---|---|
| `--slack-channel` | channel for approval messages |
| `--slack-signing-secret` | verifies clicks; prefer `PLAYPLACE_SLACK_SIGNING_SECRET` |
| `--slack-bot-token` | needs `chat:write` and `users:read.email`; prefer `PLAYPLACE_SLACK_BOT_TOKEN` |

Slack approvals are optional. If you set `--slack-bot-token` or
`--slack-signing-secret`, `serve` refuses to start unless all three flags are
set and OIDC sign-in is on. playplace uses sign-in to know the approvers. A
person can approve in Slack when their Slack email is in `--approvers` or
`--admins`.

Set `--base-url` to the public URL of the web UI:

```sh
playplace serve ... --base-url https://playplace.example.com \
  --slack-channel C0123456 --slack-signing-secret ... --slack-bot-token xoxb-...
```

Point the interactivity URL of the Slack app at `BASE_URL/slack/interact`,
here `https://playplace.example.com/slack/interact`. Only requests made in the
web UI post a message. playplace refuses a click on a button in a message
that an older playplace version posted. See [Slack](approving.md#slack) for
what approvers see.

## Notifications and history

| flag | default | meaning |
|---|---|---|
| `--slack-webhook` | | Slack incoming webhook. playplace posts every lifecycle notice to its channel. Without it, notices go only to the log. See [Notices](#notices) |
| `--history` | `file` | `file`, `cloudwatch`, or `none` |
| `--history-file` | `~/.playplace/history.jsonl` | for `--history file` |
| `--history-log-group` | `/playplace/history` | for `--history cloudwatch` |

`--history file` writes to a file on the machine that runs the command. A
`serve` process and a CLI on a laptop then keep separate records. To keep one
shared record, run every process with `--history cloudwatch` and the same
`--history-log-group`. When playplace creates the log group, it tries to set
a retention of one year. It does not change the retention of a group that
already exists.

### Notices

A lifecycle notice is a short message that playplace sends when a request or
an account changes. playplace sends one for a new, approved, edited, denied,
or expired request, for a ready, expiring, closed, or overdue account, and
for a change in budget protection. Every command that changes a request or
an account can send one, not only `serve`.

playplace writes each notice to its log at `warn`, so the default
`--log-level` shows it. With `--slack-webhook`, playplace also posts the
notice to the webhook's channel and keeps the log line. The log then records
what playplace sent. If the post fails, the log has the text of the notice
next to the `notify` error. The deployment guide alerts on error logs, so a
notice at `warn` does not raise an alert.

playplace does not send a notice to a person. A notice names the account
owner, or `approvers` for a new request. The requester is not a recipient.
The Slack app (see [Slack](#slack)) posts approval messages for web
requests only. It does not carry notices.

Without `--slack-webhook`, `serve` logs one warning at startup. Other
commands do not warn. They run on every call or, like `reconcile`, on a
schedule, so the same warning would repeat on every run. Their notices are in
their own log output. In the TUI, notices show in the notifications window.

## AWS

| flag | meaning |
|---|---|
| `--aws-profile` | named profile |
| `--aws-region` | region for Organizations calls |
| `--aws-endpoint` | endpoint override, for LocalStack |
| `--log-level` | `debug`, `info`, `warn` (the default), or `error`. [Notices](#notices) log at `warn`, so `error` hides them |
