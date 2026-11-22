# Finding accounts

The accounts page at `/accounts` lists accounts with a search, filters, a
sort and pages. Admins see every account. Everyone else sees only the
accounts they own. Admins open the page from the Accounts link in the top
bar, or from the link such as "All 112 accounts" under the table on the home
page. Anyone else opens it from the link **All your accounts** under their
loan cards, or types `/accounts` after the address of playplace.

The home page is shorter:

- Admins see the *Needs attention* list beside the approval queue. Below them, a table shows the first 50 open accounts, the soonest due first, with no search, filters or sort. The link under the table, such as "All 112 accounts", gives the count on the accounts page. That count includes the pending requests, which the home table leaves out.
- Everyone else sees their open accounts as loan cards. See [Requesting an account](requesting.md#while-you-have-it).
- The home page shows no closed accounts and has no **Show closed** box. Use the Status filter on the accounts page to find closed accounts.

## Needs attention on the home page

Only admins get this list. It has one row for each reason that a person
must act, such as "2 budgets restricted" or "6 expiring soon". Under the
count, the row names up to three accounts. Press a row to open the accounts
page with the matching "Needs attention" filter. If the list is empty, it
says "Nothing needs attention." If playplace cannot load the accounts,
the list says so. It does not say that nothing needs attention.

## Search

Type in the search field at the top of the list. playplace looks for the
text in the account name, the owner email and the AWS account ID. Upper and
lower case do not matter. The list updates when you stop typing for a
moment, or at once when you press Enter. playplace uses at most the first
100 characters. If you press a column head, **Previous**, **Next** or one of
the admin buttons before the list updates, playplace uses the text that is
in the search field at that moment.

## Filters

- **Status.** "Open" is the default. It shows every account except closed
  ones. Pick one status, such as *active* or *closing*, or pick "Any status"
  to include closed accounts too. The Status filter replaces the old
  **Show closed** box.
- **Budget health.** Shows only accounts whose budget protection is in one
  state, such as *restricted* or *error*. "Not checked" shows accounts whose
  budget protection playplace did not check yet.
- **All, Mine and Needs attention.** Only admins get these three buttons.
  "Mine" shows the accounts that you own. "Needs attention" shows the
  accounts where a person must act: a closure that AWS did not confirm in
  time, a failed creation, a budget restriction or error, damaged tags,
  budget protection that takes too long, an *unavailable* account, an
  account close to its expiry, or spend over the budget.

The search and the filters work together. When nothing matches, the list
says "No accounts match these filters." Press **Clear filters** to go back
to the plain list.

## Sorting

Press a column head to sort by that column. Press it again to sort in the
other direction. The sorted head shows an arrow. By default the list sorts
by the Due column, soonest first. An account with no due date, such as a
closed or failed one, sorts last. An account whose spend is unavailable
sorts last when you sort by spend.

## Pages

A page holds 50 accounts. When there are more, the bottom of the list shows
a line such as "Showing 51–100 of 112", with **Previous** and **Next**. The
number next to the heading counts every account that passes the filters,
not only the accounts on this page.

A new search, filter or sort starts again at page 1. **+7d**, **Close** and
**Sync with AWS** (for admins) keep the page that you are on. If the list
gets shorter, for example after a Close, and your page is past the end, you
see the last page.

## Share or keep a view

The address in the browser follows the search, the filters, the sort and the
page. So you can reload the page, press Back, or copy the address to share
the view. If your session ends, you come back to the same view after you sign
in again. The live updates every 15 seconds keep the view too.

A very long address is the one exception. This can happen with a long search
in a script such as Chinese, or with emoji. Then you come back to the
accounts page after you sign in, but with the default view.

## The Due column

The Due column shows each due date as a stamp, such as "NOV 29 2026", in
UTC. Point at the stamp to see the exact time. In the last 3 days the stamp
is red. After the due date passes, it stays red and says "past due". After
an extension, the earlier date shows struck through above the stamp. A
pending request says "Stamped at approval". An account that AWS still
creates without an approved request says "Set when it is ready". A damaged
expiry says "unknown", and a closed or failed account shows a dash.

## Spend shows "unavailable"

The Spend column shows "unavailable" when playplace could not get the spend
of the account from AWS Cost Explorer, or did not get it yet. The row then
has no spend bar. This is not $0.00. The budget still shows after it. An
account without an AWS account ID yet, such as one that AWS still creates,
shows $0.00, because it cannot have spend.

The home page shows "unavailable" in the same way. playplace tries again
later by itself. The account page tries at once, and so does **Sync with
AWS**, which only admins have.
