# Approving requests

Approvers are named when playplace is set up. Admins are approvers too.

## The queue

Sign in to the web UI. The *Approval queue* is the first panel on your home
page. Below it, your own accounts show as loan cards, and the *New request*
form comes last. See [Requesting an account](requesting.md#while-you-have-it).
Your own requests stay in the queue. Unless self-approval is allowed, they
show the "Your request" chip in place of Approve and Deny, because another
approver must decide them.

The queue lists every request that waits, oldest first. A slip shows the name, the purpose, who asked and for whom,
the exact lifetime and budget, and the request version. Click the name, or
press Enter on it, to open the slip. The open slip shows every fact that
decides it: when it arrived, the ceilings, the per-owner limit, the whole
purpose, and when playplace drops it. The slip stays open while the page
updates. The page updates every 15 seconds. To stop the updates for a
while, press **Pause updates** at the bottom of the page. See
[Live updates](requesting.md#live-updates).

- **Approve** creates the account, and AWS takes a few minutes. playplace
  sends an *approved* [notice](#notices). You cannot undo an approval. The
  new account counts toward the account limit of the organization until 90
  days after it closes. If AWS cannot create the account, playplace sends a
  *creation failed* notice.
- **Deny** opens a dialog that asks for a reason. The web UI does not deny
  a request without one, and the reason can have at most 500 characters.
  Press **Deny** in the dialog to deny, or **Cancel** to go back. If
  playplace refuses the reason, the dialog opens again with your text and
  says why. The *denied* notice and the history show the reason on one
  line.
- **Edit** (admins only), in the open slip, changes the owner, days, budget,
  or purpose before approving. playplace saves only the fields you change. A
  lifetime that is not a whole number of days shows as part of a day, for
  example 1.5, with the hours next to it. The *edited* notice lists each
  change.
- **Withdraw**, in the open slip, removes a request. Requesters and owners
  can withdraw their own, and admins can withdraw any. The command line and
  the TUI can withdraw too.

After a decision, the slip shows a stamp, APPROVED or DENIED, with the
version that you decided. An approved slip says that AWS is creating the
account. A denied slip shows the reason. The stamped slip stays until the
page updates.

You cannot approve a request you made or one that would give you the
account. Someone else must. The web UI does not let you deny such a request
either, but you can withdraw it. The slip of such a request shows *Your
request* in place of the buttons, and the open slip says why.

Each button acts on the request as your page showed it. Someone can edit the
request after your page loads. The request can also end, and someone can
submit a new request with the same name. In both cases, playplace refuses the
click. The slip then shows the current request, marks it *changed after you
loaded the page*, and names what changed, for example budget $40 to $60.
Read the new values, then click again. If the request no longer waits, its
slip says so until the page updates. A refused edit or Withdraw shows its
reason at the top of the queue instead, and the queue shows what waits now.

## Deciding several requests

Each slip that you can approve has a checkbox. Select the slips that you
want to decide. The bar at the top of the queue shows how many you
selected. The box in that bar selects or clears every slip at once. A
selected slip stays selected while the page updates. If someone changes the
request, its box clears, so you never decide values that you did not see.
If a box clears while a dialog is open, the dialog opens again with the
new selection when you confirm. A summary opens again only if two or more
requests are still selected. If one is left, it is approved at once,
because you saw it in the summary. If none are left, nothing is decided.

- **Approve selected** approves the selected requests. If you selected two
  or more, a summary opens first. It lists each request with its owner,
  lifetime, budget and the per-owner limit. It also says that each new
  account counts toward the account limit of the organization until 90 days
  after it closes, and that you cannot undo an approval. **Cancel** has the
  focus. Press **Approve 3**, with the number that you selected, to
  approve them all. If you selected one, it is approved at once.
- **Deny selected** opens the deny dialog. The dialog lists the requests,
  and one reason goes with all of them.

You can approve at most 10 requests at once and deny at most 50. If you
select more, playplace says so and decides nothing.

playplace decides each request on its own, oldest first. The bar says how
many went through. Each slip shows its own result: a stamp, or the reason
that playplace refused it. A refused request does not stop the others. Two
things stop the rest: a failed call to AWS, and requests that together
take longer than 45 seconds. AWS needs time for each new account. The slip
of a request that playplace did not try says *playplace stopped before this
request*. A request that was not decided keeps its box, so you can try it
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

The message links to the request in playplace, and the link opens its slip.
If you are not signed in, playplace signs you in first and then opens the
slip. If the request no longer waits, playplace says so at the top of the
queue.

The message shows the exact budget, the exact lifetime, and any limit
override. playplace does not update the message when someone edits,
withdraws, or decides the request in the web UI. If the request changed after
playplace posted the message, playplace refuses the click. Only you see the
reply. Open playplace to see the current request.

## Admin extras

Admins see every account, not only their own, and can act on all of them.
On the home page of an admin, the *Needs attention* list sits beside the
queue. Below them, a table shows the first 50 open accounts, the soonest due
first, with a link to all of them. Admins get no loan cards. See
[Finding accounts](finding-accounts.md#needs-attention-on-the-home-page).
They can request an account for someone else by filling in the owner field,
and they can run **Sync with AWS**, which finishes creations that have
landed, sends *expiring* notices, closes expired accounts, and retries
closes AWS refused. The same pass runs on its own every minute while the
server is up. Closure is not instantaneous: `closing` stays visible until AWS
reports `CLOSED`. An unconfirmed closure older than `--close-alert-after` (default
24 hours) generates an overdue warning and makes reconciliation fail, so the
operator can investigate. `unavailable` is not proof of closure.
