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
| `--ou-name` | `Playground` | organizational unit that holds playground accounts |
| `--email-pattern` | `aws+pp-{name}@example.com` | root email for new accounts; `{name}` is replaced |
| `--default-ttl` | `14d` | lifetime when a request does not say |
| `--default-budget` | `50` | monthly budget in USD when a request does not say |
| `--warn-before` | `3d` | how far ahead of expiry to warn the owner |
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
| `--request-ttl` | `7d` | how long a request waits for approval before it is dropped |
| `--allow-self-approval` | off | let requesters approve their own requests |

## Owner access

| flag | default | meaning |
|---|---|---|
| `--permission-set` | | IAM Identity Center permission set, by name or ARN, granted to owners. Empty disables owner validation and access grants |

The Identity Center and identity store calls use the default AWS region, so
`--aws-region` or the profile must point at the Identity Center home region.

## Web UI and sign-in

These belong to `playplace serve`.

| flag | default | meaning |
|---|---|---|
| `--addr` | `:8080` | listen address |
| `--every` | `1m` | refresh interval, with a unit |
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

Without any `--oidc-*` flag the UI has no sign-in and every visitor is an
admin. Use that only on a private network.

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

Point the Slack app's interactivity URL at `BASE_URL/slack/interact`.

## Notifications and history

| flag | default | meaning |
|---|---|---|
| `--slack-webhook` | | incoming webhook for lifecycle notifications |
| `--history` | `file` | `file`, `cloudwatch`, or `none` |
| `--history-file` | `~/.playplace/history.jsonl` | for `--history file` |
| `--history-log-group` | `/playplace/history` | for `--history cloudwatch` |

## AWS

| flag | meaning |
|---|---|
| `--aws-profile` | named profile |
| `--aws-region` | region for Organizations calls |
| `--aws-endpoint` | endpoint override, for LocalStack |
| `--log-level` | `debug`, `info`, `warn`, or `error` |
