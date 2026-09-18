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

The same channel carries the outcome, with the reason if it was denied, and
the web UI shows it under your pending requests. Once approved, AWS creates
the account. In the table it shows as *creating*, then *active*, with
the account id.

The account is in your AWS access portal under the playground permission
set. Sign in there to reach the console or to get CLI credentials.

## While you have it

- **Extend.** Press **+7d** on the row to add a week. An account cannot live
  longer than the lifetime limit counted from its creation; past that, ask
  an admin. You will be warned three days before expiry.
- **Watch spend.** The table shows month-to-date spend against your budget.
  Cost Explorer lags about a day. If your organization set an alert address,
  AWS Budgets emails it at 80 percent of your budget and when the forecast
  passes it.
- **Close early.** Press **Close** when you are done. AWS suspends the
  account at once and deletes it after 90 days. This cannot be undone.
- **History.** The account page lists what happened to it and who did it.

## Other ways to ask

Depending on how your organization runs playplace, you may also be able to
request from Slack, from a GitLab pipeline, or from the command line. The
rules are the same everywhere: one owner, a lifetime, a budget, and an
approver.
