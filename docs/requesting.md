# Requesting an account

## Sign in

Open playplace in your browser and sign in with your work account. Your email
is the owner of everything you request, so there is nothing to fill in for
that.

## Ask for an account

Fill in the form at the top of the page.

| field | what to enter |
|---|---|
| name | lowercase letters, digits, and dashes, such as `dev-alex` |
| days | how long you need it, up to the limit shown under the form |
| budget | dollars per month, up to the limit shown under the form |
| purpose | one line on what you will try |

Press **Request**. Your request appears under *Your pending requests* with a
**Withdraw** button in case you change your mind. Approvers are told through
whatever channel your organization connected, such as a Slack channel.

You can hold one account or pending request at a time by default. If you
already have one, close it or wait for it to expire before asking for another.

## After approval

playplace sends a notice when your request is approved, denied, edited, or
dropped, when the account is ready, before it expires, and when it closes.
A notice is a short message that playplace writes to its log. If your
organization set up a Slack channel for notices, playplace also posts each
notice there. playplace does not message you directly. If you see no
notices, check the web UI or ask an operator. See
[Notices](configuration.md#notices).

The *approved* or *denied* notice gives the outcome, with the reason if it
was denied. The request then leaves *Your pending requests*. Once approved,
AWS creates the account. In the table it shows as *creating*, then *active*,
with the account id. After an hour as *creating*, the row also shows a note
that the account waits outside the Playground OU for the next reconcile.
Until then it has no budget or guardrails. If the note is still there after a
day, tell an operator.

The account is in your AWS access portal under the playground permission
set. Sign in there to reach the console or to get CLI credentials.

An *active* account can still be getting ready. When budget protection is
on, the budget column shows `setup-pending` until a later refresh sees the
protection in AWS. This usually takes a few minutes. The *ready* notice says
when you can sign in.

## While you have it

- **Extend.** Press **+7d** on the row to add a week. An account cannot live
  longer than the lifetime limit counted from its creation; past that, ask
  an admin. Three days before expiry, playplace sends an *expiring* notice.
- **Watch spend.** The table shows month-to-date spend against your budget.
  Cost Explorer lags about a day. If your organization set an alert address,
  AWS Budgets emails it at 80 percent of your budget and when the forecast
  passes it.
- **Close early.** Press **Close** when you are done. Closure is asynchronous:
  the account stays *closing* until AWS confirms it is closed. Charges can
  continue while closure is pending, and existing commitments or subscriptions
  can outlive closure. Recovery during the 90-day post-closure period requires
  AWS Support.
- **History.** The account page lists what happened to it and who did it.

## Other ways to ask

Depending on how your organization runs playplace, you may also be able to
request through the CLI or TUI with operator credentials. Slack can carry
request notifications. It carries approval buttons only for requests made in
the web UI. The rules are the same everywhere:
one owner, a lifetime, a budget, and an approver.
