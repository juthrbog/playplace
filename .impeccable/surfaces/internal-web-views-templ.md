---
version: 1
slug: "internal-web-views-templ"
primary_target: "internal/web/views.templ"
related_targets: ["internal/web/server.go"]
---

# Surface brief: playplace web UI

Scope: the whole web UI served by `playplace serve` (internal/web). Mode: Operate.

Audience and job: engineers request playground accounts and watch their own; approvers work the approval queue; admins also watch a fleet of over 100 accounts. Engineers and approvers use it about equally. Desktop first; on a phone, approving from the Slack link and checking one account must work well.

In scope: home for both roles, approval queue with bulk approve and deny and keyboard shortcuts, fleet table with search, filters and sorting, account page with the AWS access-portal link and access-grant state, new request form, History hub (docs/design/lifecycle-history.md), lifecycle notice feed, phone views for approving and checking an account.

Constraints: exact lifetime and budget wherever someone decides; request versions on every decision; unknown stays unknown; CONTEXT.md vocabulary; `#pending` stays a link target; strict CSP. Recommended stack: templ + htmx overhaul, no JS framework.

Unresolved: whether NoAuth mode gets a visible badge. Decided on 2026-10-12 (see docs/design/web-ui-overhaul.md, Decisions): requests stay free-form with no admin presets, and pages poll on every host.

## Direction contract

THESIS: Every account is a loan from the playplace desk: stamped out, due back, renewed, returned. The screen refuses the grey cloud-console arrangement of summary tiles over a generic table; due dates and decisions are stamped facts, not muted metadata.

OWN-WORLD: Shelf green #2F5D50 owns the shell; card stock #EDE3CC marks the desk (approval queue) and the date-due column; white paper surfaces on a cool ground #F2F1EC; ink #22201C; stamp violet #5A3E9B for due dates and approval; red ink #A8262F for every red word, and overdue red #C8323C only for fault icons and bars (overdue, over budget, failed, restricted). Public Sans for all UI; Courier Prime only for stamped dates, account IDs and versions. Square-ish 4-8px corners, 1px rules, no shadows.

STORY: An engineer sees their loans and when each is due, extends or returns one, and requests another. An approver sees the waiting requests first, stamps them with the exact facts in view, and trusts that a changed request is refused, not silently approved.

FIRST VIEWPORT: Shelf-green shell with brand, Queue (count), Accounts, History, Notices, search, Sync with AWS, New request, user and role. Row one: "Needs attention" ledger (left, 384px) beside the approval desk (card stock) with bulk select, Approve selected and Deny selected, and each request's lifetime, budget, purpose, requester and version. Below: the fleet table, sortable, filterable (All, Mine, Needs attention; status; budget health), with the date-due column on card stock holding violet stamps; renewals show the struck-through earlier date above the new stamp.

FORM: Date Due, library circulation desk; position 1 of 7 on the ordered list (taken as the pick card); seed key 80b72400. Signature interaction: stamping. Approve and extend land as a stamp in two frames, never a fade.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
