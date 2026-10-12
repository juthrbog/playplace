# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Two groups use the web UI about equally. The owner confirmed this on
2026-10-11.

- Engineers request playground accounts for themselves. They watch the
  status, spend and expiry of their own accounts. They extend, close or
  withdraw their own requests. They do not hold AWS credentials, so the web
  UI is their usual entry point.
- Approvers work the approval queue. An approver sees every pending request
  but only their own accounts. Admins also watch the fleet. An admin sees
  every account, edits pending requests, overrides limits, changes budgets
  and runs "Sync with AWS".

Operators run the CLI and TUI with management-account credentials. They are
not web UI users, but the web UI must agree with what those tools show.

## Product Purpose

playplace hands out AWS sandbox accounts ("playground accounts") inside an
AWS Organization, and takes them back. Every account starts as a request
that an approver accepts. Each account has an owner, a lifetime and a
monthly budget, and playplace closes it when the lifetime ends. Success
means that an engineer gets a ready account in minutes, approvers decide
with the exact facts in front of them, and no account runs past its
lifetime or budget without someone seeing it.

## Positioning

playplace keeps no database. The Playground OU says which accounts exist,
and tags on each account say everything else. Every decision binds to a
request version, so an approver acts only on the values they saw. Approval,
not creation, is the human step, because a new account counts toward the
account limit of the organization until 90 days after it closes.

## Operating Context

- One deployment holds over 100 accounts. Owners need their own accounts
  first. Admins need search, filters by status and attention, and sorting.
- The web UI is served by `playplace serve`, the one process that writes.
  The CLI and TUI also exist, and Slack posts approval messages with
  Approve and Deny buttons for web requests.
- Lifecycle notices (*requested*, *approved*, *edited*, *denied*, *expired*,
  *ready*, *expiring*, *closed*, *overdue*, and budget notices) go to the log
  and to an optional Slack webhook. They never go to a person. Notices and
  Slack link to `BASE/#pending`.
- AWS takes a few minutes to create an account. Cost Explorer data lags
  about a day and is pulled at most once an hour per account. Closure is
  asynchronous, and charges can continue while an account is closing.

## Capabilities and Constraints

- Lifecycle statuses: pending, creating, failed, active, expiring, closing,
  closed, unavailable. Budget health is separate: alerts-only,
  alerts-disabled, setup-pending, ready, restricting, restricted,
  recovering, retiring, retired, error, or not checked. Readiness is
  separate again: an active account can still wait for its initial handoff.
- Defaults: lifetime 14 days (ceiling 90), budget $50 a month (ceiling
  $500), one open account or pending request per owner, requests dropped
  after 7 days, the *expiring* notice 3 days before expiry. A ceiling of 0
  means no limit.
- Every view where an approver decides shows the exact lifetime and budget:
  "36 hours", never "1 day", and "$12.50", never "$12".
- Approve, deny, withdraw and edit carry the request version the actor saw.
  A stale action is refused, and the UI shows the current request.
- Unknown values never look like values. A damaged expiry or budget shows
  as unknown, a failed cost pull shows as unavailable, and a history
  failure never reads as empty.
- Names are 3 to 50 characters (`a-z`, `0-9`, `-`). Purpose is at most 120
  characters. A budget has at most two decimals.
- Sign-in is OIDC with a 12-hour session. Without OIDC, every visitor is an
  admin named "operator", which is for development only.
- Stack: the web UI is templ with a vendored htmx 2.0.10 today, inside one
  Go binary, with a CSP that allows scripts only from the server. The owner
  said on 2026-10-11 that a JS framework is acceptable for the overhaul.
  The choice is open.
- Vocabulary follows CONTEXT.md. Say lifecycle notice, not notification.
  Say settling protection, not error, while it lasts under two hours. Say
  account readiness, not created or active, for "ready to use". Say
  lifecycle history, not audit trail.
- In scope for the overhaul (owner, 2026-10-11), each with backend work:
  a sign-in link to the AWS access portal with the access-grant state (a
  new URL setting), a feed of lifecycle notices in the web UI, the History
  hub from docs/design/lifecycle-history.md, and bulk approve and deny with
  keyboard shortcuts for the queue.
- Devices: desktop first. On a phone, people approve from the Slack link
  and check one account, so those tasks work well at phone width. The full
  fleet table stays desktop-first.
- Requests are free-form (owner, 2026-10-12). The web form starts with the
  default lifetime and budget and states the ceilings. The overhaul adds no
  admin-defined presets. A later preset will only fill in the form, so the
  request record keeps exact values and never a preset name.

## Brand Commitments

The existing mark is a cloud with a tube slide and ball-pit balls
(`internal/web/static/logo.svg`), and the wordmark sets "play" in purple and
"place" in near-black. The ball colors are #E63946, #FFC531, #2E86DE,
#2DBE6C and #6C4DF2, and the TUI uses #5B3FE6 as its primary color. None of
these is binding (owner, 2026-10-11): the current look is evidence only,
and the logo can be redrawn to fit a new direction.

Two results would feel wrong (owner, 2026-10-11): a toy-like look, because
the tool controls real AWS spend, and a generic grey admin dashboard that
could belong to any product.

## Evidence on Hand

- Screenshots of the current web UI with demonstration data, and a
  research report, in the session scratchpad. They are not committed.
- No real fleet data, usage numbers or user quotes. Do not invent them.

## Product Principles

- Show the facts that decide an action next to that action: exact
  lifetime, exact budget, the request version, and who asked.
- Never hide state. Every account shows its lifecycle status, its budget
  health and what it waits for, and unknown stays unknown.
- Serve every role without a mode switch. Engineers see their own accounts
  first. Approvers see the waiting requests first. Admins see what needs
  attention first.
- Agree with the CLI, TUI, Slack and the docs. One word means one thing in
  every interface.
