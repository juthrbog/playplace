---
name: playplace
description: The web UI of playplace, the desk where engineers borrow AWS sandbox accounts and approvers stamp them out.
colors:
  shelf: "#2F5D50"
  shelf-deep: "#264D42"
  shelf-rule: "#A9BDB4"
  shelf-ink: "#2F5D50"
  card: "#EDE3CC"
  card-hover: "#E8DEC4"
  card-sel: "#E2D5B6"
  card-rule: "#CDBF9E"
  violet: "#5A3E9B"
  violet-deep: "#4A3184"
  red: "#C8323C"
  red-ink: "#A8262F"
  red-deep: "#8E1F27"
  ink: "#22201C"
  ink-2: "#55514A"
  control-rule: "#8E8A80"
  rule: "#D6D1C4"
  rule-soft: "#E7E3D9"
  ground: "#F2F1EC"
  paper: "#FFFFFF"
  paper-tint: "#F8F6F0"
  paper-warm: "#FBF8F1"
typography:
  headline:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "19px"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "-0.01em"
  title:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "17px"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "-0.01em"
  fact:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "16px"
    fontWeight: 700
    lineHeight: 1.3
    fontFeature: "'tnum' 1, 'lnum' 1"
  body:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.45
    fontFeature: "'tnum' 1, 'lnum' 1"
  body-medium:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 500
    lineHeight: 1.45
  control:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 600
    lineHeight: 1
  secondary:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 400
    lineHeight: 1.45
  secondary-medium:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "13.5px"
    fontWeight: 500
    lineHeight: 1.45
  label:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.35
    fontFeature: "'tnum' 1, 'lnum' 1"
  column-head:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1.35
  label-bold:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 700
    lineHeight: 1.5
    fontFeature: "'tnum' 1, 'lnum' 1"
  keycap:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1
  mono:
    fontFamily: "Courier Prime, monospace"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.45
  stamp-date:
    fontFamily: "Courier Prime, monospace"
    fontSize: "15px"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "0.03em"
  stamp-date-large:
    fontFamily: "Courier Prime, monospace"
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "0.03em"
  stamp-word:
    fontFamily: "Public Sans, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 700
    lineHeight: 1
    letterSpacing: "0.09em"
rounded:
  meter: "2px"
  stamp: "3px"
  control: "4px"
  group: "5px"
  note: "6px"
  panel: "8px"
  pill: "9px"
spacing:
  hair: "2px"
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  xxl: "32px"
components:
  shell:
    backgroundColor: "{colors.shelf}"
    textColor: "{colors.card}"
    padding: "10px 24px"
  nav-link:
    textColor: "{colors.card}"
    typography: "{typography.body-medium}"
    rounded: "{rounded.control}"
    padding: "6px 11px"
  nav-link-current:
    backgroundColor: "{colors.shelf-deep}"
    textColor: "{colors.paper}"
  count-pill:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink}"
    typography: "{typography.label-bold}"
    rounded: "{rounded.pill}"
    padding: "0 6px"
    height: "18px"
  search-on-shell:
    backgroundColor: "{colors.paper-warm}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "0 44px 0 35px"
    height: "34px"
  button-approve:
    backgroundColor: "{colors.violet}"
    textColor: "{colors.paper}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "34px"
  button-approve-hover:
    backgroundColor: "{colors.violet-deep}"
    textColor: "{colors.paper}"
  button-primary:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "34px"
  button-primary-hover:
    backgroundColor: "{colors.card-sel}"
    textColor: "{colors.ink}"
  button-plain:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "34px"
  button-plain-hover:
    backgroundColor: "{colors.paper-tint}"
    textColor: "{colors.ink}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.card}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "34px"
  button-ghost-hover:
    backgroundColor: "{colors.shelf-deep}"
    textColor: "{colors.paper}"
  button-deny:
    backgroundColor: "{colors.red-ink}"
    textColor: "{colors.paper}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "0 13px"
    height: "34px"
  button-deny-hover:
    backgroundColor: "{colors.red-deep}"
    textColor: "{colors.paper}"
  button-disabled:
    backgroundColor: "{colors.rule-soft}"
    textColor: "{colors.ink-2}"
  button-small:
    padding: "0 11px"
    height: "30px"
  button-touch:
    height: "44px"
  button-decide-phone:
    typography: "{typography.fact}"
    height: "52px"
  input-text:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "0 10px"
    height: "34px"
  segmented:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    typography: "{typography.secondary-medium}"
    rounded: "{rounded.group}"
    padding: "0 11px"
    height: "30px"
  segmented-current:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.paper}"
  panel:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
  panel-desk:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
  slip:
    padding: "11px 16px"
  slip-hover:
    backgroundColor: "{colors.card-hover}"
  slip-selected:
    backgroundColor: "{colors.card-sel}"
  table-head:
    backgroundColor: "{colors.paper-tint}"
    textColor: "{colors.ink-2}"
    typography: "{typography.column-head}"
    height: "34px"
  table-cell:
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    padding: "9px 14px"
  table-cell-compact:
    padding: "4px 14px"
  table-cell-due:
    backgroundColor: "{colors.card}"
  table-row-hover:
    backgroundColor: "{colors.paper-tint}"
  table-row-selected:
    backgroundColor: "{colors.card}"
  table-row-selected-due:
    backgroundColor: "{colors.card-sel}"
  stamp-date:
    textColor: "{colors.violet}"
    typography: "{typography.stamp-date}"
    rounded: "{rounded.stamp}"
    padding: "4px 7px 3px"
  stamp-date-soon:
    textColor: "{colors.red-ink}"
  stamp-date-large:
    textColor: "{colors.violet}"
    typography: "{typography.stamp-date-large}"
    rounded: "{rounded.stamp}"
    padding: "7px 10px 5px"
  date-unset:
    textColor: "{colors.ink-2}"
    typography: "{typography.label}"
    rounded: "{rounded.stamp}"
    padding: "4px 7px 3px"
  stamp-approved:
    textColor: "{colors.violet}"
    typography: "{typography.stamp-word}"
    rounded: "{rounded.stamp}"
    padding: "5px 7px 4px"
  stamp-denied:
    textColor: "{colors.ink}"
  stamp-withdrawn:
    textColor: "{colors.ink-2}"
  stamp-expired:
    textColor: "{colors.ink-2}"
  stamp-failed:
    textColor: "{colors.ink}"
  stamp-closed:
    textColor: "{colors.shelf-ink}"
  stamp-overdue:
    textColor: "{colors.red-ink}"
  status-label:
    textColor: "{colors.ink}"
    typography: "{typography.body-medium}"
  meter:
    backgroundColor: "{colors.rule-soft}"
    rounded: "{rounded.meter}"
    width: "96px"
    height: "4px"
  meter-full:
    backgroundColor: "{colors.rule-soft}"
    rounded: "{rounded.stamp}"
    width: "100%"
    height: "6px"
  meter-fill:
    backgroundColor: "{colors.shelf-ink}"
  meter-fill-high:
    backgroundColor: "{colors.ink}"
  meter-fill-over:
    backgroundColor: "{colors.red}"
  note:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.note}"
    padding: "10px 12px"
  note-desk:
    backgroundColor: "{colors.card}"
  kbd:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink-2}"
    typography: "{typography.keycap}"
    rounded: "{rounded.control}"
    padding: "2px 5px"
  badge-role:
    textColor: "{colors.card}"
    typography: "{typography.stamp-word}"
    rounded: "{rounded.stamp}"
    padding: "2px 6px"
  flag:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.card}"
    typography: "{typography.column-head}"
    rounded: "{rounded.stamp}"
    padding: "2px 7px"
  dialog:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.panel}"
    width: "min(640px, calc(100% - 32px))"
---

# Design System: playplace

This file is the visual system of the playplace web UI. The tokens in the
frontmatter are normative. The first values came from the mockups on the
design canvas "playplace web UI overhaul" and in `.impeccable/mocks/decision/`.
These mockups are now a record. They show the layout and the content of each
screen, but their values are older than this file. Where a mockup or a
design brief in `.impeccable/surfaces/` disagrees with this file, this file
is correct.
[docs/design/web-ui-overhaul.md](docs/design/web-ui-overhaul.md) keeps the
research, the reasons and the list of resolved disagreements.
[ARCHITECTURE.md](ARCHITECTURE.md) is the architecture document.

Phase 1 of the overhaul built the stylesheet `internal/web/static/app.css`
from this file, and the Go templates use its classes. A Go test keeps the
day and night colors of the stylesheet equal to the colors in this file. An
`impeccable document` scan of the real stylesheet updated this file in
phase 1. This file marks a component that a later phase builds as "not built
yet".

## Overview

**Creative North Star: "The Date Due Card"**

Every playground account is a loan from the playplace desk. It is stamped
out, due back, renewed and returned. The interface is the date-due card from
a library book: a shelf-green shell, card-stock desk surfaces, and violet
date stamps that a person can read from across the room. A due date and a
decision are stamped facts, not muted metadata.

The system is dense and calm, because people use it daily across more than
100 accounts. Most of the page is paper and ground with one-pixel rules.
Card stock appears only where a person decides or reads a due date. The one
expressive move is the stamp: a turned, inked mark that lands in two frames.
Everything else is square, flat and quiet.

The design rejects two things that the maintainer named: the generic grey cloud
dashboard with summary tiles over a plain table, and the toy-like look of
the old ball-pit colors. The tool controls real AWS spend.

**Key Characteristics:**
- A shelf-green shell, paper panels on a warm grey ground, and card stock for the desk and the date-due column.
- Violet Courier Prime stamps for every due date, turned 1 to 2 degrees.
- One sans-serif face at five fixed sizes, with tabular figures.
- Flat surfaces with 1 px rules. No shadows anywhere.
- One motion: the stamp lands in two frames over 90 ms.
- Status is always a word and an icon, never color alone.

## Colors

The palette is the circulation desk: a green shelf, buff card stock, and a
stamp pad with violet and red ink, set on paper and ground.

### Primary
- **Shelf Green** (#2F5D50): the shell bar and its navigation.
- **Shelf Ink** (#2F5D50): green as a foreground, for the healthy state. The check icon, the spend meter below 75%, the CLOSED stamp and the live-update dot use it. By day it is the same as shelf green. At night it is #7AB8A6, because the night shelf is only 1.1:1 on the night paper.
- **Deep Shelf** (#264D42): hover and the current page in the shell, and the hover of the ghost button.
- **Shelf Rule** (#A9BDB4): the border of the ghost button and the role badge on the shell.

### Secondary
- **Card Stock** (#EDE3CC): the approval desk, the date-due column, the loan card, and the primary button. The loan card is the card-stock block that shows one account's due date on the engineer home and the account page. On the shell, card stock is also the text color and the focus ring.
- **Selected Card** (#E2D5B6): a selected slip (one request on the approval desk) or a selected row on card stock, the due cell of a selected row, and the primary button hover.
- **Card Hover** (#E8DEC4): a slip under the pointer on the desk.
- **Card Rule** (#CDBF9E): every rule on card stock. Slips are separated by dashed card rules.

### Tertiary
- **Stamp-Pad Violet** (#5A3E9B): due-date stamps, the APPROVED stamp, the Approve and Extend buttons, the focus ring, text selection, the caret, and settling icons (work in progress, not a failure).
- **Pressed Violet** (#4A3184): the Approve button hover.
- **Overdue Red** (#C8323C): fault icons and bars only. The spend meter from 95%, the failed and restricted icons.
- **Red Ink** (#A8262F): every red word. The OVERDUE stamp, a due date in its last 3 days, error text, the invalid field border, the stale row frame, and the Deny button. The refusal note border uses it too.
- **Deep Red Ink** (#8E1F27): the Deny button hover.

### Neutral
- **Desk Ink** (#22201C): text, strong fills, the current segment, the flag chip, the DENIED and FAILED stamps, and the spend meter from 75%.
- **Pencil Grey** (#55514A): secondary text, column heads, hints, placeholders, disabled text, and the WITHDRAWN and EXPIRED stamps.
- **Control Rule** (#8E8A80): the border of plain buttons, text fields, selects and segmented controls on paper and ground. On card stock these borders are pencil grey.
- **Panel Rule** (#D6D1C4): panel borders, table head rules, and the keycap border.
- **Soft Rule** (#E7E3D9): row lines, the meter track, and the disabled fill.
- **Reading-Room Ground** (#F2F1EC): the page behind the panels.
- **Paper** (#FFFFFF): panels, tables, text fields, and the text on violet, pressed violet, red-ink and deep red-ink fills in both palettes. By day it is also the text on deep shelf.
- **Paper Tint** (#F8F6F0): table heads, day-group heads, row hover, plain button hover, unread notices, and the dialog footer.
- **Warm Paper** (#FBF8F1): the search field on the shell, the "Sign-in is off" note under the shell, and the incomplete-history banner.

### Night desk

The night desk is the dark palette. It is a desk lamp over a dark desk.
Card stock keeps its warm hue, violet stays violet, and red ink stays red.
Every text pair passes 4.5:1. Every field border, icon, meter fill,
stamp border and focus ring passes 3:1. The ratios in the table are cut
down to one decimal, never rounded up.

Phase 1 writes the day and the night tokens, but the night desk stays off.
After phase 4, one small change turns it on: the `prefers-color-scheme`
rule, the theme cookie and the theme control (see Theme control).

| Token | Day | Night | Check at night |
|---|---|---|---|
| shelf | #2F5D50 | #16302A | ink on it 11.4:1 |
| shelf-deep | #264D42 | #21443B | ink on it 8.7:1 |
| shelf-rule | #A9BDB4 | #6A8C80 | 3.8:1 on shelf |
| shelf-ink | #2F5D50 | #7AB8A6 | 5.2:1 on card-sel |
| card | #EDE3CC | #2C2A22 | ink on it 11.6:1 |
| card-hover | #E8DEC4 | #332F27 | ink-2 on it 5.9:1 |
| card-sel | #E2D5B6 | #3A3729 | ink on it 9.6:1 |
| card-rule | #CDBF9E | #4A4536 | rule, no text |
| violet | #5A3E9B | #A996E6 | 4.6:1 on card-sel |
| violet-deep | #4A3184 | #9682D9 | paper on it 5.1:1 |
| red | #C8323C | #F07A80 | 4.4:1 on card-sel, icons only |
| red-ink | #A8262F | #F59BA0 | 5.7:1 on card-sel |
| red-deep | #8E1F27 | #E3868C | paper on it 6.3:1 |
| ink | #22201C | #ECE7DA | 13.3:1 on paper |
| ink-2 | #55514A | #B3AD9F | 5.3:1 on card-sel |
| control-rule | #8E8A80 | #7A756A | 3.6:1 on paper |
| rule | #D6D1C4 | #353B37 | rule, no text |
| rule-soft | #E7E3D9 | #272D29 | rule, no text |
| ground | #F2F1EC | #121614 | ink on it 14.7:1 |
| paper | #FFFFFF | #1B201D | ink on it 13.3:1 |
| paper-tint | #F8F6F0 | #212622 | ink-2 on it 6.8:1 |
| paper-warm | #FBF8F1 | #25231D | ink-2 on it 7.0:1 |

Most components use the same tokens at night. These roles change:

- Text, icons and the focus ring on the shell use ink, at 11.4:1 on the night shelf. Night card stock on the night shelf is only 1.0:1. At night both parts of the brand word, "play" and "place", are ink.
- Text on deep shelf uses ink. Night paper on night deep shelf is only 1.5:1.
- Card stock on the shell (the count pill and New request) is an ink fill with paper text. The hover fill of New request is pencil grey.
- The search field on the shell gets a 1 px shelf-rule border, because no dark fill reaches 3:1 on the night shelf.
- Stamps use normal blending, not multiply (see Elevation & Depth).
- The dialog scrim is black, not ink.
- The root sets `color-scheme: dark`, so native checkboxes, selects and scroll bars are dark.

The text on violet, pressed violet, red-ink and deep red-ink fills is paper
in both palettes. Night paper is dark, so it passes 6.4:1 on night violet
and 7.9:1 on night red ink. Green as a foreground is shelf ink in both
palettes, so it needs no night rule.

The stylesheet keeps the night differences in the token layer, as role
variables for each palette, for example `--on-shell`, `--shell-chip`,
`--search-edge`, `--stamp-blend` and `--scrim`. Components read the role
variables, so no component has its own night rule. The web UI supports
evergreen Chrome, Edge, Firefox and Safari from 2024 on (see PRODUCT.md).
All of them have `light-dark()`, so the stylesheet uses it. Each color token
and each color role is one `light-dark(day, night)` value, so every value
has one copy. The root sets `color-scheme: light`, which keeps the night
desk off. `[data-theme=night]` sets `color-scheme: dark`, and nothing sets
that attribute in phase 1. `--stamp-blend` and `--scrim` do not fit in one
`light-dark()` value. They have their day value on the root and their night
value under `[data-theme=night]`. When the night desk turns on, the
`prefers-color-scheme` rule sets the dark scheme and these two values for
`:root:not([data-theme=day])`. A test reads the `light-dark()` pairs and
checks the contrast of both palettes.

Violet on selected card is the tightest text pair at 4.6:1. If a turned
stamp on a selected row reads weak at night, the first fallback is the
screen blend for stamps at night. It gives 5.8:1 with no token change. The
second fallback is night violet #AE9CE8. Night card stock is only 1.1:1 on
paper (1.2:1 by day), so the desk and the date-due column stand out less at
night. On 2026-10-19 the maintainer kept the night card stock as it is.
Phase 2 looks at it again when it builds the desk and the date-due column.

### Named Rules

**The Shelf Owns the Shell Rule.** Shelf green fills the shell and nothing else. In the content area, green appears only as shelf ink: a healthy icon, the meter fill, the CLOSED stamp and the live dot. No content button is green.

**The Card Stock Is the Desk Rule.** Card stock marks the places where someone decides or reads a due date: the approval desk, the date-due column and the loan card. A plain list or a form sits on paper.

**The Red Ink Rule.** Red text is always red ink (#A8262F). Overdue red (#C8323C) is only for icons and bars, because red on card stock is 4.1:1 and fails for text.

## Typography

**Display Font:** none. The system has no display face.
**Body Font:** Public Sans (with system-ui, sans-serif)
**Label/Mono Font:** Courier Prime (with monospace), for stamped dates, account IDs and request versions only

**Character:** Public Sans is a plain, public-service grotesque that stays
legible at 12 px in a dense table. Courier Prime is the typewriter of the
date stamp. It appears only where a value is stamped or copied by hand.

Both faces are under the SIL Open Font License. The server embeds them as
`woff2` files, because the CSP allows no third-party host.

### Hierarchy
- **Headline** (700, 19px, 1.3, -0.01em): page headings and the brand word.
- **Title** (700, 17px, 1.3, -0.01em): panel headings.
- **Fact** (700, 16px, 1.3, tabular): the facts that decide an action, which are lifetime and budget. Also the decision buttons on a phone.
- **Body** (400, 14px, 1.45, tabular): body text, table cells and item names (at 600 or 700). Nav links and status words use the medium weight (500).
- **Control and Secondary** (600, 500 or 400, 13.5px): button and field labels at 600, segmented items at 500, secondary text at 400.
- **Label** (400, 12px, 1.35): meta lines, captions, help text and key hints. Column heads and flags use 600. Keycaps use 600 at line height 1. Counts use 700.
- **Stamps** (Courier Prime 700 at 15px or 22px, 0.03em, and Public Sans 700 capitals at 12px, 0.09em): date stamps and word stamps. Stamps are marks, not running text, so they are the one exception to the five sizes.

### Named Rules

**The Five Sizes Rule.** Text uses 19 or 17, 16, 14, 13.5 and 12 pixels, and nothing between. A mockup value of 11, 11.5, 12.5, 13, 15, 18, 20 or 26 pixels snaps to the step whose job it does. The phone uses the same steps.

**The Stamp Face Rule.** Courier Prime sets stamped dates, account IDs and request versions, and nothing else. Monospace is never a "technical" costume for labels, numbers or headings.

**The Tabular Rule.** The page root sets tabular lining figures, so every number lines up in columns, in every type role. Letter spacing never goes below -0.03em.

## Layout

The desktop page is a shell bar over a left-aligned grid of panels. The
shell wraps when it runs out of room. It holds, in order, the brand, the
navigation, the search field pushed to the right (at most 360 px), Sync with
AWS, New request, the signed-in person, and Sign out. The search field is
not built yet, so Sync with AWS starts the right side, or New request when
there is no Sync. Shell controls are 34 px tall, with 10 px top and bottom
padding and a 24 px side gutter.

The main area has 16 px top padding, 24 px side gutters, 32 px bottom
padding, a 16 px gap between panels, and a maximum width of 1600 px. On the
admin home, the "Needs attention" ledger (300 to 384 px) sits beside the
approval desk, and the fleet table runs full width below them. The ledger
is not built yet. Until then, the New request panel, the approval desk and
the fleet table stack in one column.

Panels have a 48 px head with 10 px by 16 px padding, and a 16 px inset for
rows. The fleet table has a minimum width of 1120 px inside a horizontal
scroll container, because the fleet stays a desktop task. Cells use 9 px by
14 px padding in the comfortable density and 4 px top and bottom in the
compact density. The compact density is not built yet. Rows do not wrap,
and cells center their content vertically. Only a cell that holds free
text, such as a purpose, an email in the queue or an error, wraps.

The spacing scale is 2, 4, 8, 12, 16, 24 and 32 px. Gaps inside a row use
6 to 10 px.

There are two breakpoints:
- At 1000 px and below, the top row stacks into one column, and the shell hides the email address. The desk slips move their facts under the purpose as one 16 px line.
- At 640 px and below, the shell and the main area use 16 px side gutters, the same as the phone frames. The main area has 12 px top padding and 24 px bottom padding. The search field takes its own row, and the Sync button shows only its icon.

The phone frames are 390 px wide. The phone serves two quick tasks: approving
from the Slack link and checking one account. The shell is 56 px tall, with
16 px on the left and 6 px on the right. Navigation, New request and Sign out
move behind a 44 px menu button. An approver keeps the Queue link and its
count in the bar. Content uses 16 px gutters and a 12 px gap between blocks.
Every touch target is at least 44 px tall. Actions sit inline at the end of
their card and are not pinned to the screen.

### Named Rules

**The Desktop Fleet Rule.** The full fleet table is a desktop surface and scrolls sideways before it collapses. A phone shows one request or one account, never the fleet.

## Elevation & Depth

The system is flat. Depth comes from three tonal layers: the ground, the
paper panels on it, and the card stock desk inside the content. Each layer
is closed by a 1 px rule. No surface has a shadow at rest or on hover. The
sticky bulk bar (the bar for decisions on many requests at once) is
separated by its card-rule top border, not a shadow.

The dialog is the only raised surface. A 1 px ink border and a scrim raise
it. By day the scrim is ink at 45 percent opacity (rgba(34,32,28,.45)). At
night it is black at 55 percent (rgba(0,0,0,.55)), because night ink is
light. Stamps use `mix-blend-mode: multiply` by day, so the card stock shows
through the ink. At night they use normal blending, because multiply on a
dark card hides the stamp. One role variable, `--stamp-blend`, holds the
blend mode of each palette.

### Named Rules

**The Flat Desk Rule.** No `box-shadow` on any surface. If something needs to stand out, it gets a rule, card stock or a stamp.

## Shapes

Corners are small and square to the grid. The inline meter is 2 px and the
full-width meter is 3 px. Stamps,
badges and flags are 3 px. Buttons, fields, nav links and keycaps are 4 px.
The segmented control group is 5 px, so its 4 px items nest inside it.
Inline notes are 6 px. Panels, the loan card and the dialog are 8 px. The
count pill is 9 px, which makes it a full pill at 18 px. The avatar and the
live dot are circles. The avatar and the live dot are not built yet.

Borders are 1 px. Stamps use a 1.5 px border in their own ink, and the large
stamp uses 2 px. Dashed lines carry meaning: separators on card stock, a
disabled control, the WITHDRAWN stamp, the "Your request" chip, an unset due
date, and a stale row.

### Named Rules

**The Turned Stamp Rule.** Only stamps rotate. A stamp turns between 1 and 2 degrees (-1.8, 1.3 or -1 degrees, varied from row to row). Nothing else on the page is rotated. The server picks the turn from the request name or the account ID, so a stamp keeps its turn when rows move.

## Components

The controls are standard web controls in the desk's colors. Each control
is a real `<button>`, `<a>`, `<input>`, `<select>` or `<dialog>`.

### Buttons
- **Shape:** gently squared (4px), 34 px tall, 13 px side padding, an icon and a word with a 7 px gap.
- **Approve:** violet with paper text. The only violet button. Extend uses it too, with the stamp icon.
- **Primary:** card stock with ink text. Inside a panel it gets a card-rule border. New request, Request, Save, Save budget, the way back on an error page and the AWS access portal link (not built yet) use it.
- **Plain:** paper with ink text and a control-rule border. Deny on a row, Return, Cancel and pager buttons use it. Return and the pager buttons are not built yet.
- **Ghost:** transparent on the shell, with card text and a shelf-rule border. Only Sync with AWS uses it.
- **Deny (not built yet):** red ink with paper text. It appears only as the final button in a dialog that denies a request or returns an account. Red marks the step that cannot be undone, not the button that opens the dialog.
- **Hover / Focus:** a hover changes the fill (pressed violet, selected card, paper tint, deep shelf or deep red ink). The plain button also turns its border to ink. Focus is a 2 px violet ring with a 2 px offset. On the shell the ring is card stock.
- **Disabled and busy:** a disabled button has a soft-rule fill, pencil-grey text and a dashed control-rule border. A busy button keeps its size, sets `aria-busy`, and changes its word to a verb in progress: Approving, Sending, Denying, Syncing, Withdrawing or Saving. The word and its verb share one grid cell, and the verb has no width, so the button keeps the width of its word. A busy button keeps its colors and is never disabled. The +7d, Close, Edit and Cancel buttons set only `aria-busy` and keep their word.
- **Sizes:** 30 px with 11 px padding in toolbars and pagers (not built yet). 44 px on the phone. The phone Approve and Deny buttons are 52 px with a 16 px label.

### Chips
- **Count pill:** card stock with ink text, 12 px bold, beside Queue and Notices in the shell. A visually hidden word says what the number counts.
- **Role badge:** the word "admin" in card text with a shelf-rule border, set as a 12 px word stamp (0.09em tracking) but not turned.
- **Flag:** ink with card text, 12 px semibold, for "limits overridden". A request that changed after the page loaded gets a red-ink flag with paper text: "changed after you loaded the page".
- **Your request:** a dashed control-rule chip in pencil grey that replaces the Approve and Deny buttons on a person's own request. On card stock its border is pencil grey.

### Cards / Containers
- **Corner Style:** 8px for panels and the loan card, 6px for inline notes.
- **Background:** paper for panels. Card stock for the approval desk and the loan card. The loan card is not built yet.
- **Shadow Strategy:** none. See Elevation & Depth.
- **Border:** 1 px panel rule on paper, 1 px card rule on card stock.
- **Internal Padding:** 16 px side inset. The panel head is 48 px tall.

### Inputs / Fields
- **Style:** paper, a 1 px control-rule border, 4px corners, 34 px tall, 10 px side padding. The label sits above in 13.5 px semibold. Help text sits below in 12 px pencil grey. Placeholders are pencil grey.
- **Focus:** a violet border and a 2 px violet ring with a 1 px offset. The caret is violet.
- **Error / Disabled:** an invalid field gets a red-ink border and a 12 px semibold red-ink message with an alert icon below it, tied with `aria-describedby`. The message below the field is not built yet. Until then, a field that the server refused gets `aria-invalid`, and `aria-describedby` ties it to the refusal note (see Feedback). A disabled field gets the soft-rule fill and a dashed border.
- **On card stock:** plain buttons, fields, selects and segmented controls use a pencil-grey border, because the control rule is only 2.6:1 on card stock.
- **Checkboxes and radios:** native, 16 px, with a violet `accent-color`.
- **Selects (not built yet):** native, 30 px in toolbars and 34 px in forms.
- **Segmented control (not built yet):** a single choice in one 5 px group with a control-rule border and dividers. Items are 30 px tall (44 px on the phone) in 13.5 px. The current item is ink with paper text. A filter that changes the URL uses links with `aria-current`. A choice inside a form uses radio inputs in a `<fieldset>`.

### Navigation
- **Style:** the shell holds the brand (the mark and "playplace" at 19 px, "play" bold in card stock and "place" regular in paper), then nav links in 14 px medium card text with 6 px by 11 px padding.
- **States:** hover and the current page get the deep shelf fill with paper text. The current page sets `aria-current="page"`. Focus is the card-stock ring.
- **By role:** engineers see Home, History and Notices. Approvers also see Queue with its count. Admins see Queue, Accounts, History and Notices, and the brand is their link home. Admins also see Sync with AWS and the role badge. A page with no signed-in viewer, such as a failed sign-in, shows only the brand.
- **Until phase 5:** the shell that phase 1 builds leaves out the History and Notices links and the Notices count pill. Phase 5 adds them when it builds those pages.
- **Mobile:** a 44 px menu button opens the navigation, New request and Sign out.

### Theme control
- **Place:** a footer on every page. On a page that polls, the footer also holds the live-update line.
- **Style:** a segmented control with three items: Auto, Day and Night. Auto is the default and follows the operating system setting.
- **Behavior:** radio inputs in a `<fieldset>`, inside one form that posts to `/theme`, with a Save button. `app.js` can submit the form on change. Day and Night set the `theme` cookie. Auto deletes the cookie. When the cookie is set, the server writes `data-theme` on the `<html>` element, so the first paint has the right palette with no script. With Auto, the page has no `data-theme`, and the operating system setting decides.
- **Timing:** the control comes with the change after phase 4 that turns on the night desk.

### Date stamp

The signature component. It is not built yet. A due date is a violet rubber
stamp in Courier Prime 700 capitals, with a 1.5 px border in its own ink,
3 px corners, and a turn of 1 to 2 degrees. The format is "OCT 27 2026": the
month in capitals, the day with no leading zero, and the year.

- **Sizes:** 15 px in tables, slips and lists. 22 px for the one due date at the top of an account page or a loan card.
- **Last 3 days:** the stamp changes to red ink.
- **Renewal:** after an extension, the old date stays above the new stamp as "extended from OCT 20", with the old date in Courier Prime at 13.5 px and struck through with a 1 px ink line.
- **No date yet:** a pending request shows a dashed card-rule box that says "Stamped at approval" in 12 px pencil grey. An account that AWS is still creating and that has no approved record says "Set when it is ready". An approved account that AWS is still creating already has a due date from the approved record, so it shows the date stamp. The box stays upright, because it is not a stamp. Its token is `date-unset`.
- **Motion:** when someone approves or extends, the stamp lands in two frames over 90 ms with no fade. Frame 1 (0 ms) shows it at 112 percent size and upright. Frame 2 (45 ms) shows it at full size, turned 1.8 degrees. Under `prefers-reduced-motion`, frame 1 is skipped.

### Word stamps

Decisions and outcomes are word stamps: Public Sans 700 capitals at 12 px
with 0.09em tracking, the same border, corners and turn as a date stamp. The
markup holds the word in lowercase and CSS sets the capitals, so a screen
reader does not spell the word out. APPROVED and DENIED are built: a slip
that the viewer decided shows one, and it lands with the motion of the date
stamp. The other word stamps are not built yet.

- **APPROVED:** violet, on the request and in lifecycle history.
- **DENIED:** ink, with the required reason beside it.
- **WITHDRAWN:** pencil grey with a dashed border. The owner withdrew a pending request.
- **EXPIRED:** pencil grey with a solid border. No decision came in 7 days.
- **FAILED:** ink with a solid border. AWS did not create the account, so the journey ended with no account. The fleet row keeps the red failed label for the 7 days that the fleet shows a failed creation, because the owner must make a new request to get an account. The stamp records the end of the journey, so it is ink.
- **CLOSED:** shelf ink. AWS confirmed the closure, so the account is back on the shelf.
- **OVERDUE:** red ink, followed by "by 2 hours" in 12 px semibold red ink. The closure is late.

In the notice feed, the approved, denied, request expired, closed and
closure overdue notices use the matching word stamp and its color. The
creation failed notice uses the FAILED stamp only when history recorded the
creation-failed outcome for its creation request. Otherwise it shows the
failed status label. The requested, edited, ready, expiring and budget
notices show a status label, not a stamp.

A word stamp is a fact that does not change. There is no OPEN, PENDING, ON
LOAN or OUT stamp. A journey is the history of one request and the account
that it became, or of an account that started without a request. A journey
ends when history records one of five outcomes: request-denied,
request-withdrawn, request-expired, creation-failed or account-closed. The
stamp follows that recorded
outcome: DENIED, WITHDRAWN, EXPIRED, FAILED or CLOSED. The same recorded
outcome drives retention and the Open/Ended filter of the History hub. A
reader can tell an ended journey from an open one by its shape, without color.

- **History hub row:** one mark in the State column. An ended journey shows its turned outcome stamp. An open journey shows the upright status label (icon and word) of its current stage. The word and the icon are the same as in the queue and the fleet table. A turned stamp in the State column always means an ended journey.
- **Reader who cannot see the current account:** for example, an approver on the journey of another owner. The State cell never shows APPROVED. It shows the provisioning outcome that the reader can see. That is the creating or failed label of an open journey, or the turned FAILED stamp when a confirmed creation failure ended the journey. If there is none, it shows only the qualifier "later history not shown" in 12 px pencil grey. The row never tells this reader whether the account closed.
- **Failed creation:** a confirmed creation failure (see Retention in [the history design](docs/design/lifecycle-history.md)) ends the journey, and the row shows the turned FAILED stamp. The stamp comes from history, so it stays after the failed row leaves the inventory. If history has a failed event but the journey cannot end, the row shows the "failed" label with the qualifier "another attempt not resolved". If history has no failed event and the outcome is not known, the row shows the "unavailable" label with the qualifier "outcome unknown".
- **State not known:** the "unavailable" label with a qualifier that says why, for example "not in the playground OU" or "outcome unknown". There is no "unknown" status word. Core already uses "unavailable" for an unknown provider state, and the TUI, the CLI and PRODUCT.md use the same word.
- **Journey view:** the journey view in `/history` has a head that shows the same mark as the row. For owners and admins, the head links to the account page. It has no 22 px date stamp and no card stock.
- **Timeline:** a stamp marks a decision, an outcome or a late closure: APPROVED, DENIED, WITHDRAWN, EXPIRED, FAILED, CLOSED and OVERDUE. APPROVED and OVERDUE are not outcomes, so in the History hub they appear only on the timeline, never in the State column. The failed event gets the FAILED stamp only when history recorded the creation-failed outcome for its creation request. Any other failed event shows the failed status label. The account event "expired" means that the due date passed and closure started. It never gets the EXPIRED stamp. EXPIRED is only for a request that no one decided in 7 days.

### Status label

A status is an icon and a word, never color alone. Icons are 16 px. A
qualifier such as "getting ready" or "overdue" follows in 12 px pencil grey.

- **Normal** (no one needs to act): pending (slip, ink), active (check, shelf ink), closed (shut door, pencil grey). Budget: ready (check, shelf ink), alerts-only (bell), alerts-disabled (bell off), retired (minus), and not checked (dashed circle), all pencil grey.
- **Settling** (progress, not failure): creating (clock arc, violet), closing (box, ink), and active while getting ready (check, shelf ink, not built yet). Budget: setup-pending (clock), restricting (lock), recovering (open lock) and retiring (minus), all violet, with the words "settling protection".
- **Needs attention** (someone must act): failed (crossed circle, red), unavailable (ban, ink), closing and overdue (box, red), expiring (hourglass, violet). Budget: restricted (lock, red), error (alert, red), and settling for over 2 hours (clock, red, not built yet).

### Spend meter

Spend sits above a thin bar. The inline meter is 96 by 4 px with 2 px
corners on a soft-rule track. The account page, the loan card and the phone
use a full-width 6 px meter. The full-width meter is not built yet.

- **Below 75 percent:** shelf ink fill and no label.
- **From 75 percent:** ink fill and an "NN% used" label in 12 px semibold ink.
- **From 95 percent:** red fill, an alert icon and the "NN% used" label in red ink.
- **Over budget:** a full red bar and the words "over budget" with an alert icon.
- **Unknown (not built yet):** a failed cost pull shows the word "unavailable" and no bar, never $0.00. Until then, a cost that did not load shows as $0.00.

The label sits 8 px to the right of the inline meter, so a table row stays
one line. Under a full-width meter, the label sits below it. The server
draws the bar as inline SVG and sets the fill with a `width` attribute,
because the CSP does not allow inline `style` attributes.

### Approval desk

The desk is a card-stock panel. Each request is a slip, separated from the
next by a dashed card rule. A slip shows a checkbox, the request name in
14 px bold, its purpose and requester, and the actions. It also shows the
lifetime and the budget as 16 px facts, and the request version. The
checkbox comes with bulk decisions and is not built yet.

A slip under the pointer is card hover, and a selected slip is selected
card. The slip that holds the focus gets a 2 px violet frame. Enter or a
click on the name opens the slip in place with every deciding fact. On a
phone the deciding facts come before Approve and Deny. The server renders
every slip closed, except the slip of a link and the slip of an edit that
just ended. A live update keeps the state that the viewer chose.

A slip that the viewer decided shows its stamp, the decided version and what
happens next. It stays until the next answer that redraws the queue. A
refused slip has a 1 px dashed red-ink frame and names the reason in a note
under the row. A request that left the queue keeps its slip in place until
that redraw, with the same frame and the reason.

The bulk bar sits at the top of the desk: card stock with a card-rule top
border, and selected card when anything is selected. Above 640 px it is
sticky. At 640 px and below it scrolls with the desk, so nothing is pinned
to a phone screen. The page keeps the height of the bar and 8 px clear above
a focused control.

The bar holds the page's one `aria-live="polite"` region and the refusal
slot of the queue. A result that has no slip shows in the refusal slot, as
one line that names its request. A link to a request that no longer waits
gets such a line too. With the bulk controls (not built yet) the bar is
48 px tall. Until then, it takes no room while it shows nothing.

On the home page of a person who is not an approver, the region sits in
Your requests, under its heading. Every other page holds it at the top of
the main area. Every action on the home page that changes a request or an
account writes a short result message into it, and so does Sync. The message
shows as a note with a check in shelf ink. A Sync result has the sync icon in
pencil grey. A refusal stays visible in its slot, and the region gets a
visually hidden copy of the same text. The region is visually hidden while it
is empty or holds only that copy. Off the home page, a refused Sync has no
slot, so the region shows the refusal.

### Tables
- **Head:** paper tint, 12 px semibold pencil-grey labels, 34 px tall, with panel rules above and below. A sortable head is a button. The sorted column turns ink and shows an arrow for its direction, and the `<th>` sets `aria-sort`. Sortable heads are not built yet.
- **Rows:** a soft-rule line under each row. Hover is paper tint. A selected row is card stock with its due cell in selected card. A focused row has a 2 px violet frame. A stale or refused row has a 1 px dashed red-ink frame and shows the new values in place. Row selection and both frames are not built yet on table rows. The desk slips have both frames, and slip selection comes with bulk decisions.
- **Date due column (not built yet):** always on card stock with a card-rule line under each cell, in every row state.
- **Account links:** 14 px semibold ink, underlined in the panel rule color with a 3 px offset, violet on hover.
- **Mono values:** account IDs and versions in Courier Prime at the size of the text around them.

### Feedback
- **Inline note:** paper with a panel-rule border, 6px corners, a 16 px icon, a semibold first line and a 13.5 px body. A refusal uses a red-ink border and an alert icon and names the recovery. In a slot under a form, in the bulk bar or in a slip, a refusal has only the semibold first line. On an error page and in an unavailable accounts panel, the problem in semibold sits above a body with the recovery. An empty desk uses card stock.
- **Error page:** a styled page with the title "Action refused" (400), "Not allowed" (403), "Page not found" or "Account not found" (404), or "Sign-in failed". The panel head holds the title, then the status code in pencil grey. Under it, a refusal note names the problem and the recovery, and one primary button leads back. A refused action on a page gets no error page, and its refusal shows on that page.
- **Dialog (not built yet):** a native `<dialog>` on paper with a 1 px ink border, 8px corners and a maximum width of 640 px. The head and the footer are separated by panel rules, and the footer is paper tint. Only three tasks use it: the deny reason, the bulk approval summary, and the consequences of Return.
- **Live update (not built yet):** an 8 px shelf ink dot and "Updated 12 seconds ago" in the footer. `app.js` resets the Updated time after each successful poll, also when nothing changed. The dot hides for one 90 ms step only when new data arrives, with no fade. Under `prefers-reduced-motion` it does not blink. The home page already polls every 15 seconds, and the server answers 204 when nothing changed.
- **Keycap (not built yet):** 12 px semibold pencil grey on paper, with a panel-rule border that is 2 px at the bottom.

### Icons

Line icons on a 24 px grid, drawn at 16 px with a 1.75 stroke, round caps
and joins, in `currentColor`. They are inline SVG from one sprite, and every
icon next to a word is `aria-hidden`. Icons are 13 px inside 12 px text lines
and 22 px in phone icon buttons. One icon name has one shape on every page.

### Mark

The mark is the cloud and the slide of the first playplace logo, drawn as
one line. It uses the icon style: a 24 px grid, round caps and joins, and
`currentColor`. It has no ball-pit colors.

- **Shell:** the line cloud with one ball, at 28 px with a 1.5 stroke. It is card stock by day and ink at night.
- **Favicon, touch icon and docs logo:** the line cloud without the ball, in card stock on a shelf-green tile, with a stroke of 2 on the 24 px grid. It reads at 16 px.
- **Banner:** the line cloud inside a round stamp lettered PLAYPLACE in Public Sans 700 capitals. Only the banner of the README and the documentation home page uses it, because smaller lettering cannot be read.

The mark is not one of the page stamps. It never turns, and it is never
violet. Violet stays the ink of due dates and decisions.

### Named Rules

**The Word and Icon Rule.** Every status, health state and outcome shows a word. Color and icon only support the word.

**The No Toast Rule.** Feedback appears beside the thing it is about and in the one live region. Nothing floats in from a corner.

**The One Motion Rule.** The stamp is the only animation, and the live dot is the only other thing that changes on its own. State changes step in two frames and never fade.

## Do's and Don'ts

### Do:
- **Do** put card stock (#EDE3CC) only under the approval desk, the date-due column and the loan card.
- **Do** show lifetime and budget as exact 16 px bold facts beside Approve, for example "36 hours" and "$12.50", with the request version.
- **Do** keep the old due date, struck through, above a renewed stamp.
- **Do** use red ink (#A8262F) for every red word, and overdue red (#C8323C) only for icons and bars.
- **Do** theme the browser surfaces: a violet selection with paper text, a violet caret and `accent-color`, and a 2 px violet focus ring that turns card stock on the shell.
- **Do** keep every text pair at 4.5:1 or more, and the border of every field, select, checkbox and segmented control at 3:1 or more, in both palettes. A button is identified by its label.
- **Do** name the row in every row action for screen readers, for example "Approve priya-glue-etl".
- **Do** make every touch target on the phone at least 44 px.
- **Do** show "unavailable" or "unknown" when a value failed to load.

### Don't:
- **Don't** add a shadow to any surface.
- **Don't** fade anything, or animate anything except the stamp and the live dot.
- **Don't** set anything in Courier Prime except stamped dates, account IDs and request versions.
- **Don't** add a font size between the five steps.
- **Don't** build summary tiles over a plain table, or any generic grey admin dashboard.
- **Don't** bring back the ball-pit colors. The tool must not look toy-like.
- **Don't** use a toast, or a modal for a task that needs no interruption.
- **Don't** signal a status with color alone.
- **Don't** round a deciding fact to "1 day" or "$12".
- **Don't** put a colored stripe wider than 1 px on the side of a card, row or note, or a small label above a heading.
- **Don't** ship inline `style` attributes or a font from a third-party host. The CSP allows neither.
