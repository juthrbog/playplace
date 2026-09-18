# Approving requests

Approvers are named when playplace is set up. Admins are approvers too.

## The queue

Sign in to the web UI. The *Pending approvals* card lists every request
waiting: who asked, for whom, how long, how much, and why.

- **Approve** creates the account. The requester is notified, and AWS takes a
  few minutes.
- **Deny** asks you for a reason and sends it to the requester.
- **Edit** (admins only) changes the owner, days, budget, or purpose before
  approving. The requester is told what changed.
- **Withdraw** removes a request. Requesters and owners can withdraw their
  own; admins can withdraw any. The command line and the TUI can withdraw
  too.

You cannot approve a request you made or one that would give you the
account. Someone else must.

## Limits

Requests are capped at 90 days and $500 a month unless an admin ticks
**override limits** when requesting or editing. An overridden request carries
a *limits overridden* badge so you know before you approve it. Each person
may hold one open account or pending request at a time. The 90 day cap also
bounds extensions: an account's expiry cannot be pushed past 90 days from
its creation unless an admin does it.

Requests nobody acts on for seven days are dropped and the requester is told.

## Slack

If Slack is connected, each request also posts to the approvals channel with
**Approve** and **Deny** buttons. Clicking acts on the same request as the web
UI, so it disappears from both. Your Slack email must be on the approver
list.

## Admin extras

Admins see every account, not only their own, and can act on all of them.
They can request an account for someone else by filling in the owner field,
and they can run **Sync with AWS**, which finishes creations that have
landed, warns owners nearing expiry, closes expired accounts, and retries
closes AWS refused. The same pass runs on its own every minute while the
server is up.
