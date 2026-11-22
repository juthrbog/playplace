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

## Sign in to AWS

The account is in your AWS access portal under the playground permission
set. Sign in there to reach the console or to get CLI credentials. If your
operator set the address of the portal, the card of your account has an
**Open AWS access portal** button. The button opens the portal start page in
a new tab. The account is listed there. The *ready* notice then also gives
the address. If no address is set, ask your operator where your organization
signs in to AWS.

The button shows only when all of these are true:

1. You own the account. An admin who is not the owner sees no button.
2. The account is *active* or *expiring*.
3. Your access is granted, or playplace does not manage AWS access for your
   organization.

An *active* account can still be getting ready. Then the loan card on the
home page shows "getting ready" under the status. The account page says
what the account waits for, for example "Getting ready: waiting for budget
protection to be ready." When budget protection is on, the budget line of
the card shows `setup-pending` until a later refresh sees the protection in
AWS. This usually takes a few minutes. The *ready* notice says when you can
sign in.

When the account is ready and playplace manages AWS access, the account
page shows "Access granted". If
playplace does not manage AWS access, the page says "playplace does not
manage AWS access for this account." Then someone else in your organization
gives you access. A closed or closing account shows no access line.

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
  **Cancel** to keep it. If the dialog cannot open, nothing is sent, and
  the page says "Reload the page to close this account."
- **Sign in to AWS.** Press **Open AWS access portal** on the card when it
  shows. See [Sign in to AWS](#sign-in-to-aws).
- **See it all.** Press the name of the account on its card to open its
  account page. See [The account page](#the-account-page).

## The account page

Each account has its own page at `/accounts/` and the ID of the account.
Open it from the name on a loan card, or from the name of a row in an
accounts table. You can also type the name of the account after
`/accounts/`. The page then moves to the address with the ID. A request
that waits for approval has the ID `req:` and its name. When an approver
approves it, the account gets the ID of its creation request. When AWS
gives it an AWS account ID, it gets that ID. Each time, an open page moves
to the new ID by itself. Only the owner
of the account and admins can open it. Anyone else gets "Not allowed".

The top of the page is the loan card of the account. The name of the
account is the heading of the page. The card shows the AWS account ID, the
status, the due date stamp and the spend:

- **Status.** If something is wrong with the account, a line under the
  status says what is wrong and what to do. See
  [Finding accounts](finding-accounts.md#a-line-under-the-status). For an
  *active* or *expiring* account, a line says where your AWS access stands:
  "Access granted", "Getting ready" and what the account waits for, or that
  playplace does not manage AWS access. See
  [Sign in to AWS](#sign-in-to-aws).
- **Spend.** The spend this month against the monthly budget, such as
  "$22.50 of $50.00 a month", with a bar under it. A line under the bar
  says what is left, such as "$27.50 left this month", or how far the spend
  is over the budget. The line "Cost data lags about a day." comes last. If
  the spend shows "unavailable", it has no bar. See
  [Finding accounts](finding-accounts.md#spend-shows-unavailable).
- **Buttons.** **Open AWS access portal**, **+7d** and **Close** work as on
  the home page, and Close opens the same dialog. After a close, you stay on the page, and the card
  shows *closing*. If playplace refuses an action, the reason shows above
  the card.

The *Details* panel under the card lists the facts of the account: the
owner, the AWS account ID, the purpose, who requested it, who approved it,
when it was created, when it is due, its lifetime, the monthly budget, the
state of budget protection and the root email. An older account can say
"not recorded" for the purpose, the requester or the approver, because
playplace did not keep them then. The lifetime counts from the creation to
the due date in whole days, such as "10 days". A lifetime shorter than one
day is in hours. When your organization sets a lifetime
limit, it follows, such as "(the limit is 90 days)". A small line under the
facts gives the creation request ID, for support.

To copy the AWS account ID, press **Copy ID** next to it. The page then
says "Copied 123456789012." If the browser cannot copy, the page says
"Could not copy the ID. Select it and copy it yourself." Copy ID needs
JavaScript, so it shows only when JavaScript is on.

Below the details come *Change monthly budget* (only for admins), the
*History* of the account and the *Daily spend*. History lists what happened
to the account and who did it.

The card and the details update by themselves every 15 seconds, as the home
page does. See [Live updates](#live-updates). The budget form, the history
and the daily spend do not update. Reload the page to see new history. When
you open the page and playplace does not know the spend, it gets the spend
from AWS at once. The updates every 15 seconds use the spend that playplace
already has. If the account is gone, or you can no longer see it, the page
shows "Account unavailable" in place of the card and the details, and stops
the updates. If playplace cannot read the account, for example because the
call to AWS failed, the page shows "The account is unavailable right now"
in place of the card and the details. It tries again every 15 seconds, and
the card comes back when the call works again.

Only the `?` key works on the account page. See [Keys](#keys).

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

## Keys

The web UI has single-key shortcuts. After you sign in, press `?` on any
page to see the keys that work on that page. **Key list** at the bottom of
the page shows the same list. On your home page, `j` and `k` move to the
next or the previous loan card or request, and Enter opens the account of
a card. Enter does nothing on a row of *Your requests*. Approvers have more keys. See [Approving requests](approving.md#keys) and
[Finding accounts](finding-accounts.md#keys).

The keys never act while you type in a field, while you hold Ctrl, Alt or
Command, or while a dialog is open.

The keys are on by default. To turn them off, press **Keys on** at the
bottom of the page. The button then says **Keys off**. Press it again to
turn them on. The setting lasts for this browser, in every tab and after a
reload. While the keys are off, no key works, `?` too. **Key list** still
shows the list. The keys need JavaScript, so the two buttons show only
when JavaScript is on.

## Other ways to ask

Depending on how your organization runs playplace, you may also be able to
request through the CLI or TUI with operator credentials. Slack can carry
request notifications. It carries approval buttons only for requests made in
the web UI. The rules are the same everywhere:
one owner, a lifetime, a budget, and an approver.
