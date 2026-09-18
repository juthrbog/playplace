# Design

Architecture, lifecycle rules, and interface design decisions for playplace.
For an overview and usage, see [the README](README.md); for contributor setup
and local testing, see [Development](DEVELOPMENT.md).

## Principles

- **No state.** The Playground OU says which accounts exist; tags on each
  account say everything else. Anything not derivable from AWS is not kept,
  except an optional write-only history log. See [No state file](#no-state-file).
- **Management account only.** The tool holds management-account
  credentials and never assumes a role into a playground account. What runs
  inside an account is somebody else's concern.
- **Every account starts as an approved request.** Requests wait as tags on
  the OU. Approval, not creation, is the human step, because a created
  account cannot be closed for four days and counts against quota.
- **Guardrails are data, not code paths.** Owner, expiry, budget, and the
  approval trail are tags. Ceilings and limits are configuration.
- **Cost Explorer bills per call.** Nothing polls it. Views cache for an
  hour; `list` never touches it.

## No state file

There is no database and no state file. The playground OU decides which
accounts are playgrounds, and tags on each account hold the rest:

| tag | meaning |
|---|---|
| `playplace:managed` | `true` once the tool has tagged the account |
| `playplace:owner` | who is responsible |
| `playplace:expires` | RFC3339 expiry |
| `playplace:budget` | monthly budget in USD |
| `playplace:warned-at` | set once the owner was warned about expiry |
| `playplace:close-requested` | durable close intent and start of the closure-monitoring clock |
| `playplace:closed-at` | when playplace observed AWS `CLOSED` and recorded confirmation |
| `playplace:close-alerted-at` | one-shot overdue-closure warning marker |
| `playplace:access` | Identity Center principal the owner's access was granted to |
| `playplace:requested-by`, `playplace:approved-by`, `playplace:purpose` | audit trail from the request |

Pending requests are tags on the Playground OU itself, keyed `playplace:req:<name>`.
AWS allows 50 tags on a resource, so the queue holds at most 50 requests,
pending and approved-but-not-yet-placed together, less any other tags on the
OU. A request past that is refused with a message saying so.

A name whose account was closed cannot be requested again while the closed
account is still listed, because AWS never frees a root email and the email
is derived from the name. `create --email` with a fresh address is the way
around it.

Everything stored in a tag obeys AWS's rules: values up to 256 characters made
of letters, numbers, spaces, and `_ . : / = + - @`. Purpose is capped at 120
characters so the request record fits. Owners, requesters, and purposes are
checked on the way in and refused with a message that names the rule.

Everything else is read live: account status from Organizations, in-flight
and failed creations from `ListCreateAccountStatus`, spend from Cost Explorer.
Accounts that land in the OU without tags are adopted with defaults on the
next refresh. Cost Explorer bills per call, so `list` never touches it;
`show`, `costs`, the TUI, and the web UI cache results for an hour.


## Lifecycle

Statuses are derived, never stored:

    pending    a request waiting for an approver (a tag on the OU)
    creating   a creation request AWS has not finished
    failed     a creation request AWS rejected (kept 7 days)
    active     in the OU with no warning or close tag
    expiring   the warned-at tag is set
    closing    close intent is tagged or AWS reports PENDING_CLOSURE
    closed     AWS explicitly reports CLOSED
    unavailable AWS reports SUSPENDED, PENDING_ACTIVATION, or an unknown state

AWS lets you close 20% of member accounts or 250, whichever is higher, per
rolling 30 days (capped at 1000, at most 3 closures in flight), and a new
account cannot be closed for its first 4 days. A refused close leaves the
intent in the tag and each refresh retries while AWS still reports ACTIVE.
`CloseAccount` is asynchronous: accepting it is not confirmed closure. Pending
closures stay visible and consume the owner's account slot. Each refresh checks
AWS's `State` field; suspension alone is never treated as closure. Older API
emulators exposing only ambiguous `Status=SUSPENDED` show as unavailable.

After `--close-alert-after` (default `24h`) without confirmation, reconciliation
returns an error, logs the overdue account, records `closure-overdue` in history,
and attempts a one-shot lifecycle notification. Monitoring continues until AWS
reports `CLOSED`. Confirmation and notification markers are stored in account
tags; failures writing them are surfaced and retried. Notifications and history
remain best-effort, so alert on failed scheduled reconciliation as well.

Before granting playground access, deploy the purchase/trail guardrail SCP and
review existing accounts using [the deployment safeguards guide](deploy/README.md#billing-and-closure-safeguards).
The SCP blocks new commitments and Marketplace purchases and member-created
trails; organization audit trails are preserved. Nothing runs `aws-nuke` or
deletes account resources. Closure is not a guarantee of zero further bills:
prior usage, existing commitments, Marketplace subscriptions, and central
logging costs need separate operator review.


## Requests and approval

Every account starts as a request, and every request needs an approver who is
not the requester. Pending requests live as tags on the Playground OU, one tag
per request, so the CLI, TUI, web UI, and Slack all see the same queue with no
database. A request records owner, TTL, budget, purpose, who asked, and when.
Approving marks the record approved first and requests the account second,
so a second approver, in the same process or another, finds nothing pending.
If AWS refuses the creation the mark is removed and the request is pending
again. Within one process the refresh pass and every change to the queue are
serialized, so the worker, the web UI, and Slack never run the same pass or
act on the same request at once. The account carries `requested-by` and
`approved-by` tags for audit. Requests nobody acts on expire after
`--request-ttl` (7 days).

Guardrails applied at request time: the name must be free, the owner must be
an Identity Center user when `--permission-set` is set, one person may hold
at most `--max-per-owner` open accounts plus pending requests (default 1),
and a request may ask for at most `--max-ttl` (90d) and `--max-budget` ($500).
Only admins can exceed the two ceilings: the override checkbox in the web
form, or `--override-limits` on the CLI. The override is recorded on the
request, shown to approvers, and carried into the account when approved.

The lifetime ceiling holds after approval too. An extension may not push an
account's expiry past `--max-ttl` counted from when it was created, so
repeated extensions cannot walk around the limit. Admins may go past it: in
the web UI every admin extend is an override, on the CLI and in the TUI it
is `--override-limits` or the override field in the form. Each extension is
written to history with whether the ceiling was overridden.

Who may approve:

- in the web UI, emails in `--approvers` and `--admins`
- in Slack, the same people, identified by their Slack email
- on the CLI and in the TUI, whoever holds management credentials; their
  identity is recorded from `PLAYPLACE_OPERATOR` or the local user and host,
  and it is also the requester of anything they queue, even for someone else

Admins can edit a pending request before approving it: owner, days, budget,
and purpose, from the Edit button in the web UI or `playplace edit-request`.
The requester is told what changed. Self-approval is refused unless
`--allow-self-approval` is set: nobody may approve a request they asked for
or would own. A requester can withdraw their own request from the web UI,
with `playplace withdraw`, or with `w` in the TUI; admins and operators can
withdraw any. `create` on the CLI skips the queue for operators and records
them as approver; it and `approve` do not apply the per-owner limit, which
is checked when a request is queued.

### Slack approvals (optional)

With a Slack app configured, each request also posts a message with Approve
and Deny buttons to a channel:

    playplace serve ... --base-url https://playplace.example.com \
      --slack-channel C0123456 --slack-signing-secret ... --slack-bot-token xoxb-...

The app needs `chat:write` and `users:read.email`, and its interactivity URL
must point at `https://playplace.example.com/slack/interact`. A click is
verified with the signing secret, the clicker's Slack email is checked against
the approver list, and the message is rewritten with the outcome. Approving in
Slack and approving in the UI act on the same queue entry.


## Owner access

With `--permission-set NAME_OR_ARN` (or `PLAYPLACE_PERMISSION_SET`), owners
must be IAM Identity Center users, given by email or user name. When an
account is placed in the OU the owner is assigned that permission set on it,
so the account appears in their access portal. The grant is recorded in the
`playplace:access` tag and removed when the account closes. Accounts adopted
from the OU get the same treatment when their owner tag resolves to a user.
Without the flag, owners are free text and nothing is granted.

Identity Center must be enabled in the management account, and the
permission set must already exist; create one such as `PlaygroundOwner` with
the policies your sandboxes allow. The Identity Center calls go to the
default region, so `--aws-region` (or the profile's region) must be the
Identity Center home region.


## Web UI and sign-in

`playplace serve` runs the web UI and a refresh pass every minute. Sign-in is
OpenID Connect against any provider, with a preset for Google:

    export PLAYPLACE_OIDC_CLIENT_SECRET=...
    playplace serve --oidc-preset google --oidc-client-id ... \
      --oidc-redirect-url https://playplace.example.com/auth/callback \
      --allowed-domain example.com --admins admin@example.com \
      --approvers lead@example.com --base-url https://playplace.example.com

Any other provider works with `--oidc-issuer https://your-idp/...`. The
verified email from the ID token becomes the owner of everything the person
requests. Engineers see and manage only their own accounts and requests.
Emails in `--approvers` see the queue and can approve and deny. Emails in
`--admins` see the fleet, can request on someone's behalf, edit pending
requests, override the TTL and budget ceilings, and run the sync. With
`--permission-set` set, the signed-in email must exist in Identity Center,
which is what makes the access grant land on the right person.

Without any `--oidc-*` flag the UI has no sign-in and every visitor is an
admin. That mode is for local development only and prints a warning.

The UI ships its own copy of htmx and serves every page with a
Content-Security-Policy that allows scripts only from itself, so nothing is
loaded from a third-party host. Inline event handlers are not allowed;
page behaviour lives in `internal/web/static/app.js`.

The server needs management-account credentials, because AWS only allows
CreateAccount and CloseAccount from there. Run it in the management account
under a role limited to Organizations, Budgets, Cost Explorer, the two
Identity Center APIs, and, with `--history cloudwatch`, its log group.


## History

Every lifecycle event is written to a history sink: requested, edited,
approved, denied, withdrawn, placed, access granted, extended, warned,
expired, close requested, closed, adopted, failed. Each line records when,
who, through which channel, and a sentence saying what happened, with details
such as a deny reason or an edit diff.

    playplace history dev-alex          # one account's story, oldest first
    playplace history --owner alex@example.com --since 30
    playplace history --limit 20         # everything recent

`--history file` (default) appends JSON lines to `~/.playplace/history.jsonl`
on the machine running the command, so nothing depends on AWS. `--history
cloudwatch` writes to a CloudWatch Logs group (`--history-log-group`, default
`/playplace/history`, one-year retention) so every operator, the web UI, and
the worker share one record. `--history none` turns it off. The account page
in the web UI and the detail pane in the TUI show the same history. The tool never reads history to make a
decision; losing it loses the timeline and nothing else.


## The mark

The name comes from the indoor playgrounds fast-food restaurants used to
have: a ball pit, tube slides, primary colours. The mark is that idea moved
to the cloud: a soft white cloud whose lower half is a ball pit, with a red
tube slide coming out from under the cloud's top edge and opening over the pit. It borrows the feel, not the trademark;
there are no arches and it copies no existing logo.

| file | use |
|---|---|
| `internal/web/static/logo.svg` | the mark alone; the web header uses it |
| `internal/web/static/logo-wordmark.svg` | mark plus the word, for docs and slides |
| `internal/web/static/favicon.svg` | the mark on a sky tile, simplified so it reads at 16 px: bigger balls, a heavier slide without seams |
| `internal/web/static/favicon.ico`, `favicon-32.png`, `apple-touch-icon.png` | rasters of the favicon, generated with `rsvg-convert` and ImageMagick |
| `docs/assets/banner.svg`, `banner.png` | the hero: mark, wordmark, and tagline on a sky panel, for the README and the docs home page |
| `docs/assets/` | also copies of `logo.svg`, `logo-wordmark.svg`, and `favicon.svg` for the documentation site |

Colours: cloud outline `#8FB8E8`, balls `#E63946` red, `#FFC531` yellow,
`#2E86DE` blue, `#2DBE6C` green, and `#6C4DF2`, the TUI's primary purple.
The wordmark colours "play" purple and "place" near-black.

The TUI carries the same mark in block characters: a cloud of `█` with a
row of `●` in the pit colours and the slide as a red diagonal of `█` ending
in a `▓` mouth, in `internal/tui/banner.go`. It shows while
the first load runs, when there are no accounts, and at the top of the `?`
overlay, and the header starts with three balls. Glyphs alone carry the
picture, so NO_COLOR terminals still get a cloud and dots.

To regenerate the rasters after editing an SVG:

    cd internal/web/static
    rsvg-convert -w 32 -h 32 favicon.svg -o favicon-32.png
    rsvg-convert -w 180 -h 180 favicon.svg -o apple-touch-icon.png
    for s in 16 48; do rsvg-convert -w $s -h $s favicon.svg -o /tmp/f$s.png; done
    magick /tmp/f16.png favicon-32.png /tmp/f48.png favicon.ico
    cp logo.svg logo-wordmark.svg favicon.svg ../../../docs/assets/
    rsvg-convert -w 2400 -h 800 ../../../docs/assets/banner.svg -o ../../../docs/assets/banner.png

## TUI design notes

What to know before changing `internal/tui`. See [the README](README.md#cli-and-terminal-dashboard)
for usage and [Development](DEVELOPMENT.md#tui) for local testing. This section
records the Charm v2 facts and design rules behind the code.
Checked against bubbletea v2.0.9, lipgloss v2.0.6, bubbles v2.2.1 with `go doc`.

### Charm v2 facts that shape the code

- `View()` returns `tea.View`. Set `AltScreen`, `WindowTitle`, and `Cursor`
  on it. There is no layers field on `tea.View`; compose overlays in Lip Gloss
  and put the string in `Content`.
- Overlays: `lipgloss.NewCompositor(lipgloss.NewLayer(page), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()`.
  Layers draw opaque cells, so a dialog clears what is under it. See `overlay`
  in `render.go`.
- Light or dark: `Init` returns `tea.RequestBackgroundColor`; handle
  `tea.BackgroundColorMsg` and call `IsDark()`. Colors come from
  `lipgloss.LightDark(isDark)(light, dark)` in `theme.go`. Rebuild bubble
  styles on receipt: `help.DefaultStyles(isDark)` and
  `textinput.DefaultStyles(isDark)` take the flag.
- Keys: match `tea.KeyPressMsg` with `key.Matches(msg, binding)`. One key map
  in `keys.go` feeds both the hint bar (`ShortHelpView`) and the `?` overlay
  (`FullHelpView`).
- Text input cursor: `textinput.Model.Cursor()` is relative to the input.
  Offset it by the input's position on screen and assign to `View.Cursor`, or
  no cursor shows. See `View()` in `tui.go`.
- The bubbles table renders its body through a viewport whose width defaults
  to zero, so rows vanish while the header still draws. We render the table
  by hand (`tableView` in `render.go`) for that reason and for per-cell color.
- Width math uses `github.com/charmbracelet/x/ansi` (`StringWidth`,
  `Truncate`), never `len()`.
- Color output goes through `colorprofile`, which honors `NO_COLOR`. Status
  glyphs carry meaning without color for that reason.

### Notices

Everything the TUI has to say arrives the same way: action results, and
the warnings and errors the service logs while it works (a budget AWS would
not create, a grant that failed, a sync error). Each shows as a toast in
the top right for six seconds, wrapped to a few lines, then ages out. `N`
opens the notifications window with the full text of every notice this
session, newest first, scrollable; `c` there clears it. The header shows
how many arrived since the window was last open.

Nothing may write to the terminal while the TUI runs, because the
alternate screen has no scrollback and stray output draws over the frame.
`tui` therefore swaps the service logger for `tui.Feed`, a `slog.Handler`
that turns records at Warn and above into notices and drops the rest.

### History

When a history sink is configured, the bottom pane's right column lists the
selected account's most recent events instead of daily spend, and the full
detail screen shows daily spend on the left and history on the right. Events
are fetched per selected account through a `tea.Cmd`, cached by name, and the
cache is cleared on every reload so actions show up at once.

### Layout

Header, section tabs, table, and a detail pane under the table when the
terminal is 22 rows or taller (about 40 percent of the main area, capped at
11 rows). Below that, Enter opens the detail full screen. Columns drop as the
terminal narrows: ACCOUNT first, then OWNER, then the spend bar.

### Color and glyph semantics

| State | Glyph | Color role |
|---|---|---|
| pending (awaiting approval) | `◇` | warning |
| active | `●` | success |
| active, 7 days or less left | `●` plus `!` | warning |
| active, 1 day or less left | `●` plus `!` | error |
| expiring (owner warned) | `●` plus `!` | warning or error by time left |
| creating, closing | `◌` | busy |
| closing with an overdue warning, unavailable | `!` | error |
| failed | `×` | error |
| closed | `○` | muted |

Spend bar: success below 70 percent of budget, warning to 90, error above.
Two border treatments only: rounded on dialogs and forms, `─` rule for the
pane. Selection is a `▌` bar plus a subtle background on every cell.

### Rules

- Keep all IO in `tea.Cmd`s, never in `Update`.
- Dialogs default to the safe button and ignore keys for 150 milliseconds
  after opening so a queued keystroke cannot confirm them.
- Destructive dialogs say exactly what happens and to which account id.
- `a`, `d`, and `w` act only on pending rows; `x` only on real accounts. `e`
  edits the request on a pending row and extends on a real account. Each
  refuses with a flash rather than opening a dialog on the wrong row type.
- History is cached per account and cleared after an action, a sync, or `r`,
  not on the timer, so the timer never re-reads the history sink.
- Empty, loading, and error states each have a line of text in the table body.
- Never print outside the frame. Warnings go through the notice feed, and
  action outcomes through `setFlash`, which is a notice too.
- Cost Explorer bills per call. Costs come from the service's hourly cache;
  only `S` (sync) and `r` (reload) force a pull.

### Checking a frame

`playplace tui --snapshot 120x30 --keys "x"` prints one styled frame with
live data after pressing the given keys (`enter`, `esc`, `tab`, or
characters). Pipe it through `freeze --language ansi --font.family Menlo` for
a PNG. Keys sent by the snapshot land inside the dialog grace window, so
forms render empty; that is expected.
