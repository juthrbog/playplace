# Web UI overhaul

**Status: proposed design, 2026-10-11.** This document records the research,
the chosen visual direction, and the plan for a full overhaul of the web UI
in `internal/web`. The mockups are on the design canvas "playplace web UI
overhaul" (a private claude.ai artifact). [PRODUCT.md](../../PRODUCT.md)
holds the product facts that this design serves. The working contract for
the direction is in `.impeccable/surfaces/internal-web-views-templ.md`.

[DESIGN.md](../../DESIGN.md) at the repository root is the normative visual
design system: the tokens, type, layout and components. This document keeps
the research, the reasons for each choice, and the plan. If a value in this
document is different from `DESIGN.md`, the value in `DESIGN.md` is correct.
The architecture document is [ARCHITECTURE.md](../../ARCHITECTURE.md).

## Why

A review of the current web UI on 2026-10-11 ran it with the fake provider
and 13 demonstration accounts. It found these problems:

- The UI has two pages and about 50 inline CSS rules. It has no search,
  filter, sort, tabs or summary. The TUI has all of them.
- The accounts table grew to 1791 px inside an 1100 px container, because
  one error string fills a row. Account ID, expiry, spend and the actions
  were off-screen at every window width.
- The 15-second poll replaces the whole table. Keyboard focus drops to the
  page, and the sideways scroll resets. The pending card never refreshes.
- The over-budget bar never turns red. templ writes two `class` attributes,
  and the browser keeps only the first.
- Request, approve, deny, withdraw, extend, close, edit and budget changes
  give no feedback. No button shows that it is busy.
- Every htmx error goes to one slot under the request form, away from the
  control that caused it. A 403 or 404 page is unstyled text.
- At 390 px the page is wider than the screen, and Approve, Deny and Close
  are off-screen.
- The account page has no actions, no purpose, no approver, and no way to
  reach the account in AWS. Daily spend is a 30-row table.
- Approve has no confirmation, so one click creates an AWS account. Deny
  uses `window.prompt`.
- Warning text, input borders, the error slot and table headers fail basic
  accessibility checks.

## Users and jobs

Engineers and approvers or admins use the web UI about equally. One
deployment holds over 100 accounts. People use it on a desktop, and on a
phone they approve from the Slack link and check one account.
[PRODUCT.md](../../PRODUCT.md) has the full record.

| Role | First job on the home page |
|---|---|
| Engineer | See their accounts, when each is due back, and its spend. Extend, return or request one. |
| Approver | Decide the waiting requests with the exact facts in view. |
| Admin | See what needs attention in the fleet, then decide requests and fix accounts. |

## Direction: Date Due

Every account is a loan from the playplace desk. It is stamped out, due
back, renewed and returned. The design refuses the grey cloud-console look
of summary tiles over a plain table. A due date and a decision are stamped
facts, not muted metadata.

The direction came from a round of the `impeccable` design skill. It listed
seven visual worlds that this audience knows by heart, and it rolled one
for the lead. It weighed six challengers from its catalog against that
lead. The maintainer chose Date Due, the library circulation desk, on
2026-10-11. The four declined challengers each gave the design one rule:

- State changes step in two frames and never fade.
- The focused queue row opens in place with every deciding fact.
- Every account, request and journey shows one ID in a fixed place.
- One typeface at five fixed sizes, and each size has one job.

### Tokens

These are the 14 core tokens. `DESIGN.md` has the full set of 22 tokens and
the night value of each one.

| Token | Light | Night desk | Job |
|---|---|---|---|
| `--shelf` | `#2F5D50` | `#16302A` | The shell: header and navigation |
| `--shelf-deep` | `#264D42` | `#21443B` | Hover and current item in the shell |
| `--card` | `#EDE3CC` | `#2C2A22` | Desk surfaces: the approval queue and date-due fields |
| `--card-sel` | `#E2D5B6` | `#3A3729` | A selected row on card stock |
| `--card-rule` | `#CDBF9E` | `#4A4536` | Rules on card stock |
| `--ground` | `#F2F1EC` | `#121614` | Page background |
| `--paper` | `#FFFFFF` | `#1B201D` | Panels and tables |
| `--ink` | `#22201C` | `#ECE7DA` | Text |
| `--ink-2` | `#55514A` | `#B3AD9F` | Secondary text |
| `--rule` | `#D6D1C4` | `#353B37` | Panel borders |
| `--rule-soft` | `#E7E3D9` | `#272D29` | Row rules |
| `--violet` | `#5A3E9B` | `#A996E6` | Due-date stamps, Approve, focus, selection |
| `--red` | `#C8323C` | `#F07A80` | Overdue, over budget, failed |
| `--red-ink` | `#A8262F` | `#F59BA0` | Red text and red stamps |

Every night pair passes 4.5:1 for text and 3:1 for borders, icons and meter
fills. A Go test measures each pair in both palettes. The night desk stays off
until one small change after phase 4 turns it on (see Phases). Then the page
follows the operating system setting, and an Auto, Day and Night control in
the page footer can override it.

### Type

Public Sans is the only UI face. Courier Prime sets stamped dates, account
IDs and request versions, and nothing else. Both are under the SIL Open
Font License, and the server embeds them as `woff2` files.

| Size | Job |
|---|---|
| 12 px | Meta lines, column heads, hints |
| 13.5 px | Controls and secondary text |
| 14 px | Body text and table cells |
| 16 px | Facts that decide an action: lifetime and budget |
| 17 to 19 px | Headings and the brand |

Numbers use tabular figures. Letter spacing never goes below -0.03em.

### Shape, depth and motion

Corners are 2 to 9 px, and `DESIGN.md` has the full scale. Panels have a
1 px rule and no shadow. Stamps are rotated by one or two degrees and use
`mix-blend-mode: multiply` by day and normal blending at night. The one
motion is stamping: an approval or an extension lands as a stamp in two
frames over 90 ms, with no fade. The live-update dot is the only other
animated element. Under `prefers-reduced-motion`, the stamp appears at once.

### Brand mark

The maintainer chose to adapt the mark on 2026-10-12. The mark keeps the
cloud and the slide of the first logo as one line in one color, without the
ball-pit colors. The shell draws the line cloud with one ball at 28 px. The
favicon, the touch icon and the docs logo use the line cloud without the
ball, in card stock on a shelf-green tile. Only the banner of the README and
the docs home page puts the cloud inside a round stamp lettered PLAYPLACE.
The mark never turns and is never violet. `DESIGN.md` has the rules.

The favicon set, the wordmark and the banner come in one separate change
that blocks no phase. That change also updates "The mark" in
[ARCHITECTURE.md](../../ARCHITECTURE.md), which documents the current files.
Phase 1 updates only the `logo.svg` row there, because the header stops
using that file.

## Information architecture

| Route | Content | Who |
|---|---|---|
| `/` | Home. Engineers see their accounts and requests first. Approvers see the approval desk first, then their own accounts and requests. Admins see what needs attention, the approval desk and the fleet. The fleet and Needs attention are admin views. The `#pending` anchor stays, because notices and Slack link to it. | Everyone |
| `/accounts` | The fleet table with search, filters, sorting and pages. Parameters: `q`, `status`, `budget`, `owner`, `attention`, `sort`, `dir`, `page`. | Everyone, scoped by role |
| `/accounts/{id}` | The account page. | Owner, admin |
| `/requests/new` | The request form. | Everyone |
| `/requests/{name}` | One pending request, with its history. | Requester, owner, approvers |
| `/history` | The History hub from [the history design](lifecycle-history.md), with a view of each journey. | Everyone, scoped by role |
| `/notices` | The feed of lifecycle notices. | Everyone, scoped by role |

Engineers see Home, History and Notices in the navigation. Approvers also
see Queue with its count. Admins see Accounts instead of Home, because the
brand is their link home. Admins also see Sync with AWS. In NoAuth mode the
shell shows a band that says sign-in is off and every visitor is an admin.

## Screens

The canvas holds one frame for each screen. A frame shows the layout of its
screen until its phase ships. After that, the real page is the reference.

- **Home, admin.** A "Needs attention" list beside the approval desk, then
  the fleet sorted by due date. The date-due column sits on card stock.
- **Home, engineer.** The person's accounts as loan cards, each with its
  due-date stamp, spend, a link to the AWS access portal, Extend and Return.
  Then their pending requests and the per-owner limit.
- **Approval queue.** Bulk selection and a focused row with every deciding
  fact. A deny dialog with a required reason, a bulk approval summary, and
  a refused stale request with its new values.
- **Account page.** The loan card with every renewal, sign-in, facts, a
  30-day spend chart, the admin budget change, and the history timeline.
- **New request.** The form beside a live slip that shows what approvers
  will see, with the limits stated before the person submits.
- **History hub.** Search and filters over journeys, including denied,
  withdrawn and expired ones, with a journey timeline. An ended journey
  shows its outcome stamp. An open journey shows the status label of its
  current stage, because a stamp is a fact that does not change.
- **Lifecycle notices.** The same notices that go to the log and Slack,
  grouped by day, with unread state.
- **Phone.** Approving from the Slack link, and checking one account.
- **Design system.** Tokens, type, stamps, status words, controls, row
  states, feedback and the first drafts of the mark. Brand mark has the
  decision.

## Interaction model

- **Live updates.** The home page polls every 15 seconds. Each poll sends
  the hash of the fragment that the page shows. The server renders the
  fragment again and hashes it without the Updated time. When the hash is
  the same, the server answers 204 No Content, and htmx keeps the page as it
  is. When the hash is different, htmx merges the new fragment into the page
  with a morph swap, so focus, scroll and open forms stay. The approval desk
  refreshes too.
- **Live time and host.** After each successful poll, `app.js` resets the
  Updated time. The live dot blinks only when new data arrives. The response
  sends `Cache-Control: private, no-cache`. Polling works behind any host or
  proxy, so the design does not depend on the production host. The overhaul
  does not use server-sent events.
- **Keyboard.** `/` focuses search. `j` and `k` move the focused row, `x`
  selects it, `a` approves and `d` denies, and `Enter` opens it. `?` lists the
  keys. Shortcuts never fire inside a text field.
- **Bulk decisions.** The approval desk is one form. Each checkbox carries
  the request name and its version. The server decides each request on its
  own and reports a result for each row. One stale request does not stop the
  others.
- **Confirmation.** Approving two or more requests shows a summary first,
  because each new account counts toward the account limit of the
  organization until 90 days after it closes. Deny always asks for
  a reason in a native `<dialog>`. Return (close) shows its consequences
  first. A single Approve on a focused row does not ask again.
- **Feedback.** Each action answers next to its control and in one live
  region (`aria-live="polite"`). A refusal names the problem and the
  recovery. A stale request shows its new values in its row. 403 and 404 are
  styled pages with a way back.
- **Extend.** Engineers choose 3, 7 or 14 days or a date. The control shows
  the latest due date that the lifetime ceiling allows. Admins get an
  override checkbox instead of an implicit override.
- **Request form.** Requests are free-form. The form starts with the
  default lifetime and budget and states both ceilings. The form hides a
  choice above the lifetime ceiling, except for admins. Admins keep the
  owner field and the override checkbox. The admin edit row uses the same
  control. The web form then differs from the TUI form, which keeps typed
  days.
- **Lifetime control.** Lifetime is the segmented control of `DESIGN.md`
  with 3, 7 and 14 days and an Other field for a number of days. The
  control preselects the default lifetime. When the default lifetime is not
  3, 7 or 14 days, it is its own choice, with a label in the format of
  `core.Lifetime`, for example "36 hours". It is not the Extend control,
  because a request has no date to choose.
- **Presets.** The overhaul adds no admin-defined presets. If presets come
  later, a preset only fills in the form. The request record keeps the
  exact lifetime and budget and never a preset name.
- **Unknown values.** A failed cost pull shows "unavailable", never $0.00. A
  damaged expiry shows "unknown". A history failure never reads as empty.

## Stack

Keep templ and htmx, and overhaul them. A React single-page app was
researched in the same depth, and it loses for this project:

- The release promises source builds with Go alone (the AUR recipe and
  `docs/releasing.md`). React needs Node in CI and about 1 MB of committed
  build output that changes on every build.
- Authorization is subtle and lives on the server: `CheckApprover`, the
  withdraw rule for stale versions, override rules and exact display values.
  Server rendering keeps it in one place.
- A few hundred rows filter and sort easily on the server.
- The server-rendered path can drop `style-src 'unsafe-inline'`. React with
  Radix and sonner cannot.
- The measured React toolkit is about 185 KB gzip in 169 packages, against
  16.5 KB for htmx.

The plan:

- Upgrade htmx to 2.0.11 with the idiomorph extension, or to 4.0 when it
  leaves the npm `next` tag. Both give morph swaps.
- One hand-written stylesheet in `internal/web/static`, with cascade layers
  and the tokens above. No CSS build step.
- Move every inline style into that file. Then the CSP drops
  `style-src 'unsafe-inline'`, and htmx runs with `includeIndicatorStyles`
  off.
- Self-host Public Sans and Courier Prime as `woff2` files.
- Draw icons as inline SVG from one stroke set (Lucide, ISC license, or
  authored icons).
- Render the spend chart as SVG on the server.
- Keep page behavior in `app.js`: keys, dialogs and the live region.
- Serve static files with `Cache-Control` and versioned URLs.

## Backend work for the new capabilities

| Capability | Work |
|---|---|
| AWS sign-in link | A new `--access-portal-url` setting. Show `AccessGrantedTo` and the readiness blocker that the CLI already prints. |
| Notice feed | Each lifecycle notice goes out at a place where playplace records a lifecycle history event. That event also stores the notice kind, subject and body, the same text that goes to the log and Slack. A new `ready` event records the ready notice. A `budget-protection` event stores a notice only when a notice went out. The feed reads these events through the role rules of the History hub. It needs the DynamoDB history store at runtime and the ownership-period rules of checkpoint 3 in [the history implementation plan](lifecycle-history-implementation.md). A cookie holds the time of the last notice that the person read, so unread state belongs to one browser. Approvers do not see the ready notice until the maintainer decides whether it is a minimal provisioning outcome. |
| History hub | The DynamoDB runtime wiring and the role rules in [the history design](lifecycle-history.md). |
| Bulk approve and deny | A handler that takes several name and version pairs and returns a result for each. |
| Fleet search and filters | Server-side filtering, sorting and pages over the cached inventory. |
| Extend by any span | The web handler accepts `by` today. Add a date and an explicit override. |
| Safe error text | Typed core errors, so pages stop showing raw AWS SDK errors. |
| Unknown spend | Return "unavailable" for a failed cost pull, as the CLI does. |
| Theme choice | A `POST /theme` handler takes Auto, Day or Night and redirects to the page that sent the form. It accepts only a local path as the target. Day and Night set the `theme` cookie. Auto deletes it. The cookie sets `Secure` behind TLS, as the session cookie does. When the cookie is set, the server writes `data-theme` on the `<html>` element. With Auto, the page has no `data-theme`. A page response that has an ETag must include the theme in it. |

## Accessibility

- Text meets 4.5:1, large text 3:1, in both palettes.
- Status is a word and an icon, never color alone.
- Every control is a real `<button>`, `<a>`, `<input>` or `<select>` with a
  label. Row actions name their row, for example "Approve priya-glue-etl".
- Tables use `<th scope="col">`, and sortable heads set `aria-sort`.
- Focus is visible on every surface, including the green shell.
- Phone touch targets are at least 44 px.

## Phases

1. **Foundation.** The stylesheet, tokens, fonts, shell, styled error pages,
   the live region, the CSP change and the htmx upgrade. Fix the over-budget
   bar and the focus loss on poll, with the 204 and hash rule of Live
   updates. The stylesheet takes its values from `DESIGN.md`.
   - The shell leaves out the History and Notices links and the Notices
     count pill until phase 5 builds them.
   - The shell draws the line mark of `DESIGN.md`. The shell task updates
     the `logo.svg` assertion in `internal/web/server_test.go`. It replaces
     the logo `<img>` in the header of `internal/web/views.templ` and its
     CSS rule. It also updates the `logo.svg` row in "The mark" of
     [ARCHITECTURE.md](../../ARCHITECTURE.md), because the header no longer
     uses that file.
   - The stylesheet holds the day and the night tokens and the role
     variables (one CSS variable for each job that changes at night, for
     example `--on-shell`). The night desk stays off.
   - A Go test measures every color pair in both palettes. A second test
     fails on a hex color outside the token layer (the rules that set the
     tokens and the role variables) of the stylesheet. It also fails on a
     fill or stroke color in a templ file.
   - The server draws its SVG with CSS classes, not with a color in the
     markup. The favicons are fixed files.
   - At the end of the phase, a new `impeccable document` scan of the real
     stylesheet updates `DESIGN.md` and `.impeccable/design.json`.
2. **Home, fleet and queue.** Role-based home, the fleet table with search
   and filters, the approval desk with bulk decisions and keys.
3. **Account page.** Sign-in link, extend control, spend chart, history.
4. **Request form.** The form page with its live slip and stated limits.
5. **Notices and History hub.** After their backend work.

The night desk turns on after phase 4, as its own small change. That change
adds the `prefers-color-scheme` rule, the theme cookie and the theme control
in the page footer. Phase 5 builds its screens in both palettes from the
start.

Each phase updates its tests and the user docs together. About 86 string
assertions in `internal/web/*_test.go` and the screens named in
`docs/requesting.md`, `docs/approving.md` and `docs/budgets.md` pin the
current markup and wording.

## Mockup drift resolved in DESIGN.md

The ten mockups share one base stylesheet, but each screen added its own
rules, and some of them disagree with the design system sheet. A review on
2026-10-11 compared every screen with the sheet. `DESIGN.md` picks one value
for each disagreement. Phase 1 builds the stylesheet from `DESIGN.md`, not
from the mockups.

- Type sizes snap to the five steps. Page titles at 20 and 26 px become
  19 px. Item names at 15 px become 14 px bold. Phone facts at 22 px become
  16 px. Small text at 11, 11.5, 12.5 and 13 px becomes 12 or 13.5 px by its
  job.
- Seven repeated raw colors became tokens: `control-rule`, `paper-tint`,
  `paper-warm`, `card-hover`, `shelf-rule`, `violet-deep` and `red-deep`.
  The table head color `#F7F6F2` and the unread color `#FBFAF6` merged into
  `paper-tint`. The one-off colors `#5D594F`, `#7A7262`, `#6E9083`,
  `#F5EFE0`, `#FDF6F5` and `#8A7C5E` were removed.
- The stamp lands in two frames over 90 ms, as the sheet says. The queue
  mockup used 200 ms from 130 percent and -5 degrees.
- Word stamps use the colors of the sheet. History and Queue drew DENIED in
  red ink, and History and Notices drew CLOSED in ink. The queue mockup set
  APPROVED and DENIED in Courier Prime. Words use Public Sans capitals.
- A focused row has a 2 px violet frame. The queue mockup used a 1 px grey
  outline.
- Controls are 34 px tall, 30 px in toolbars and 44 px on the phone. The
  mockups also used 32, 36 and 38 px.
- Return is a plain button. Only the final button of its confirmation is
  red. The account mockup drew Return as a red outline button.
- The AWS access portal link is a primary button on card stock. Two mockups
  drew it in shelf green, which breaks the rule that green belongs to the
  shell.
- Copy ID is one small plain button. The mockups drew it three ways.
- The live indicator is the 8 px shelf ink dot. The admin home and the queue drew a sync icon.
- The spend meter has two sizes: inline 96 by 4 px and full width at 6 px.
  The 8 px meter with a border on the engineer home is removed. The label
  sits beside the inline meter.
- The single-choice segmented control is one component. Five screens had
  five versions of it.
- The large date stamp is 22 px. The engineer home drew it at 23 px.
- The role badge is a 12 px word stamp with 0.09em tracking. The mockups
  drew it at 11 px with 0.07em tracking.
- At 640 px and below, the main area uses 16 px side gutters, the same as
  the phone frames. The base stylesheet used 12 px.
- On card stock, control borders are `ink-2`, because the control rule
  is only 2.7:1 on card stock.
- The night values of the sheet replace the earlier values in this
  document. The earlier red-ink pair was 4.43:1 and failed.
- Some class names had a different meaning on each screen, for example
  `.note`, `.warn`, `.fld` and `.kind`. Some icon names had a different
  shape on each screen, for example `i-back`, `i-copy` and `i-ext`. Phase 1
  gives each component one name and each icon one shape.

## Decisions

The maintainer decided these questions on 2026-10-12. `DESIGN.md` and the
sections above hold the details.

- **History hub stamps.** An ended journey shows the turned stamp of its
  recorded outcome event: DENIED, WITHDRAWN, EXPIRED or CLOSED. An open
  journey shows the status label of its current stage. No stamp exists for
  an open or pending journey. A reader who cannot see the current account
  never sees APPROVED in the State column.
- **Night desk.** `DESIGN.md` sets the night value of all 22 color tokens,
  with the new `shelf-ink` token for green as a foreground. Phase 1 writes
  both palettes, the role variables and the tests, but the night desk stays
  off. One small change after phase 4 turns it on, with an Auto, Day and
  Night control in the page footer.
- **Mark.** The mark adapts the first logo. The shell and the icons use the
  line cloud. Only the banner uses the round stamp lettered PLAYPLACE. See
  Brand mark.
- **Canvas.** The canvas and `.impeccable/mocks/decision/` are a record. A
  screen frame shows the layout until its phase ships. Where a mockup
  disagrees with `DESIGN.md`, `DESIGN.md` is correct.
- **Request presets.** Requests stay free-form. The form offers lifetime
  choices and starts with the defaults. The overhaul adds no admin-defined
  presets. See Interaction model.
- **Live updates.** Polling is the final design on every host. The server
  answers 204 No Content when the hash of the fragment is the same. The
  overhaul does not use server-sent events.
- **Notice feed.** The lifecycle history event of the same action stores
  each notice. The feed reads it through the role rules of the History hub.
  See Backend work for the new capabilities.
- **Approver fleet view.** Approvers get no fleet view. The fleet and Needs
  attention are admin views.

## Open questions

- Whether the ready notice counts as a minimal provisioning outcome that
  approvers can see. Until the maintainer decides, approvers do not see it.
- A failed account creation never gets a terminal outcome. Its journey
  stays open, so the retention rule never removes it (`Journey.validate` in
  `internal/audit/journey.go`, and Retention in
  [the history design](lifecycle-history.md)). This is a history design
  question, not a UI question.
- Night card stock is only 1.1:1 on paper (1.2:1 by day). The maintainer
  looks at it again in the screenshot round at the end of phase 1.
