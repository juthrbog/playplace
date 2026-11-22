# Requesting an account

## Sign in

Open playplace in your browser and sign in with your work account. Your email
is the owner of everything you request, so there is nothing to fill in for
that.

## Ask for an account

Fill in the *New request* form. It is the last panel on the home page. The
**New request** button in the top bar takes you to it.

| field | what to enter |
|---|---|
| name | lowercase letters, digits, and dashes, such as `dev-alex` |
| days | how long you need it, up to the limit shown under the form |
| budget | dollars per month, up to the limit shown under the form |
| purpose | one line on what you will try |

Press **Request**. Your request appears under *Your requests* with a
**Withdraw** button in case you change your mind. If you are an approver,
your request appears in the *Approval queue* instead, and you have no
*Your requests* panel. Approvers are told through whatever channel your
organization connected, such as a Slack channel.

You can hold one account or pending request at a time by default. If you
already have one, close it or wait for it to expire before asking for another.
If you are not an approver, a line under the *Your requests* heading says
where you stand, for example "You have 1 of 1 open accounts and requests
allowed." An account counts until AWS confirms its closure. While its card
shows it as *closing* or *unavailable*, it still counts.

## After approval

playplace sends a notice when your request is approved, denied, edited, or
dropped. It also sends one when AWS cannot create the account, when the
account is ready, before it expires, and when it closes.
A notice is a short message that playplace writes to its log. If your
organization set up a Slack channel for notices, playplace also posts each
notice there. playplace does not message you directly. If you see no
notices, check the web UI or ask an operator. See
[Notices](configuration.md#notices).

The *approved* or *denied* notice gives the outcome, with the reason if it
was denied. The request then leaves *Your requests*. Once approved,
AWS creates the account, and it gets a loan card on the home page. The card
shows *creating*, then *active*, with the AWS account ID. After an hour as
*creating*, the card also shows a note
that the account waits outside the Playground OU for the next reconcile.
Until then it has no budget or guardrails. If the note is still there after a
day, tell an operator.

If AWS cannot create the account, the card shows *failed* for 7 days, and
playplace sends a *creation failed* notice. No account exists. The notice
gives the reason from AWS and the next step. Some reasons need a new request
with another name. Others need an admin first, for example when the
organization reached its account limit.

The account is in your AWS access portal under the playground permission
set. Sign in there to reach the console or to get CLI credentials.

An *active* account can still be getting ready. When budget protection is
on, the budget line of the card shows `setup-pending` until a later refresh sees the
protection in AWS. This usually takes a few minutes. The *ready* notice says
when you can sign in.

## While you have it

The home page shows each account you have as a loan card, the soonest due
first. A card shows the name, the AWS account ID, the status, the due date,
the spend and the buttons you can use. The due date is a stamp such as
"NOV 29 2026", in UTC. Point at the stamp to see the exact time. In the last
3 days the stamp turns red. After the due date passes, it stays red and says
"past due". If you have no account, the page says "You have no open
accounts." with a link to the form. The link **All your accounts** under the
cards opens the accounts page. There the Status filter also finds your closed
accounts. See
[Finding accounts](finding-accounts.md).

- **Extend.** Press **+7d** on the card to add a week. The card then shows
  the earlier date struck through above the new stamp, for example
  "extended from NOV 20". An account cannot live
  longer than the lifetime limit counted from its creation; past that, ask
  an admin. Three days before expiry, playplace sends an *expiring* notice.
- **Watch spend.** The card shows month-to-date spend against your budget,
  with a bar under it.
  Cost Explorer lags about a day. If the spend shows "unavailable",
  playplace could not get it yet. See
  [Finding accounts](finding-accounts.md#spend-shows-unavailable). If your organization set an alert address,
  AWS Budgets emails it at 80 percent of your budget and when the forecast
  passes it.
- **Close early.** Press **Close** when you are done. A dialog opens first.
  It names the account, its AWS account ID and its owner, and says what
  closure means. Closure is asynchronous: the account stays *closing* until
  AWS confirms it is closed. Charges can continue while closure is pending,
  and existing commitments or subscriptions can outlive closure. Recovery
  during the 90-day post-closure period requires AWS Support. The account
  counts toward the account limit of the organization until 90 days after it
  closes. **Cancel** has the focus. Press **Close account** to close it, or
  **Cancel** to keep it.
- **History.** The account page lists what happened to it and who did it.

## Live updates

The page updates by itself every 15 seconds. The line at the bottom of the
page shows a green dot and the time of the last update, for example
"Updated 12 seconds ago". If an update fails, the time does not reset, so
it tells you how old the data on the page is. The line shows only when
JavaScript is on.

Press **Pause updates** to stop the updates, for example while you read a
long list. The line then starts with "Paused". Press **Resume updates** to
get the latest data at once. The pause lasts for this browser tab, also
after a reload. When you close the tab, the pause ends. Your own actions, such as
**+7d** or **Close**, still update the page while it is paused.

A tab that is in the background does not update. When you come back to it,
it updates at once, unless you paused it.

## Other ways to ask

Depending on how your organization runs playplace, you may also be able to
request through the CLI or TUI with operator credentials. Slack can carry
request notifications. It carries approval buttons only for requests made in
the web UI. The rules are the same everywhere:
one owner, a lifetime, a budget, and an approver.
