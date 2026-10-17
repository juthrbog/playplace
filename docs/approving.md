# Approving requests

Approvers are named when playplace is set up. Admins are approvers too.

## The queue

Sign in to the web UI. The *Pending approvals* card lists every request
waiting: who asked, for whom, how long, how much, and why.

- **Approve** creates the account, and AWS takes a few minutes. playplace
  sends an *approved* [notice](#notices). You cannot undo an approval. The
  new account counts toward the account limit of the organization until 90
  days after it closes. If AWS cannot create the account, playplace sends a
  *creation failed* notice.
- **Deny** asks you for a reason, which the *denied* notice and the history
  show.
- **Edit** (admins only) changes the owner, days, budget, or purpose before
  approving. playplace saves only the fields you change. A lifetime that is
  not a whole number of days shows as part of a day, for example 1.5, with
  the hours next to it. The *edited* notice lists each change.
- **Withdraw** removes a request. Requesters and owners can withdraw their
  own; admins can withdraw any. The command line and the TUI can withdraw
  too.

You cannot approve a request you made or one that would give you the
account. Someone else must.

Each button acts on the request as your page showed it. Someone can edit the
request after your page loads. The request can also end, and someone can
submit a new request with the same name. In both cases, playplace refuses the
click and shows the current request. Read the request again, then click
again.

## Limits

Requests are capped at 90 days and $500 a month unless an admin ticks
**override limits** when requesting or editing. An overridden request carries
a *limits overridden* flag so you know before you approve it. Each person
may hold one open account or pending request at a time. The 90 day cap also
bounds extensions: an account's expiry cannot be pushed past 90 days from
its creation unless an admin does it.

Requests nobody acts on for seven days are dropped, and playplace sends an
*expired* notice.

## Notices

A notice is a short message that playplace sends when a request or an
account changes. playplace writes each notice to its log. If your
organization set `--slack-webhook`, playplace also posts the notice to that
Slack channel.

playplace does not send a notice to a person. The requester does not get
one, and the owner does not get one. A notice names the owner of the
account, or `approvers` for a new request. If the requester or the owner
must know the outcome, tell them, or make sure that they can read the
channel. See [Notices](configuration.md#notices).

## Slack

If Slack is connected, each request made in the web UI also posts to the
approvals channel with **Approve** and **Deny** buttons. Requests made with
the command line or the TUI do not post. A click acts on the same request as
the web UI, and the message then shows the outcome. Your Slack email must be
on the approver list.

The message shows the exact budget, the exact lifetime, and any limit
override. playplace does not update the message when someone edits,
withdraws, or decides the request in the web UI. If the request changed after
playplace posted the message, playplace refuses the click. Only you see the
reply. Open playplace to see the current request.

## Admin extras

Admins see every account, not only their own, and can act on all of them.
They can request an account for someone else by filling in the owner field,
and they can run **Sync with AWS**, which finishes creations that have
landed, sends *expiring* notices, closes expired accounts, and retries
closes AWS refused. The same pass runs on its own every minute while the
server is up. Closure is not instantaneous: `closing` stays visible until AWS
reports `CLOSED`. An unconfirmed closure older than `--close-alert-after` (default
24 hours) generates an overdue warning and makes reconciliation fail, so the
operator can investigate. `unavailable` is not proof of closure.
