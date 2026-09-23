# Lifecycle history redesign

**Status: accepted design; implementation in progress.** This document records
the agreed target for storing, searching, and displaying history. The checkpoint
below distinguishes delivered behavior from requirements still pending. See [the architecture decision](../adr/0001-dedicated-lifecycle-history.md)
and [the domain glossary](../../CONTEXT.md).

**Resuming implementation in a fresh session?** Start with the
[implementation handoff](lifecycle-history-implementation.md) for the ordered
checkpoints, dependencies, acceptance checks, worktree state, and validation notes.

## Implementation checkpoint: identity, readers, and shared-store adapter

Implemented:

- persisted request journey IDs, carried through edits, approval, placement, and
  service restarts; new submissions receive new IDs even when names/times match;
- account-origin identity for direct creation and adoption, and event IDs on new
  history writes;
- journey-scoped account timelines without name-based or legacy-record joins;
- structured CLI filters, query-bound pagination cursors, JSON output, and
  newest-first/oldest-first ordering;
- paginated web account timelines with escaped details and explicit read errors;
- journey-keyed TUI caches, scrollable paginated timelines, and retryable errors;
- request-expiry events emitted only after queue removal succeeds;
- a DynamoDB adapter with organization/environment namespaces, transactional
  search indexes, and duplicate-safe event writes, not yet enabled at runtime;
- durable journey origins, exact operation/request/account correlations,
  ownership observations, and terminal metadata, including archived journeys;
- local and loopback tests for complete filtered pagination, timestamp ties,
  missing/corrupt/unavailable records, and identity recovery after producer loss.

See the [store layout and contracts](lifecycle-history-store.md) for checkpoint 1.
The DynamoDB Local tests include a separate producer process whose working
directory is removed before a new client recovers a failed direct journey.
That recovery uses exact IDs, not names or live inventory.

Still pending: runtime wiring and deployment configuration for the shared store;
durable intent/delivery and unknown-outcome reconciliation; exposure-increasing
operation gates; ownership-period authorization; the History hub and web-wide
search; failure grouping; journey retention and scheduled maintenance; and
retirement of CloudWatch/disabled modes. Storage-level duplicate detection does
not establish the delivery contract. Current runtime writes remain best-effort.
In-flight or failed direct creations without a queue record still need runtime
integration with the new metadata adapter for identity recovery after restart.

The existing CloudWatch adapter remains in the runtime reader interface until
cutover. This is not a new compatibility-window commitment. Runtime readers
still scan matching records. The DynamoDB adapter uses transactional indexes,
but its two-second capacity target remains unmeasured.

## Purpose and boundaries

Lifecycle history is a shared operational record of a request's journey and any
resulting account lifecycle. Requests that are denied, withdrawn, or expire
without producing an account are first-class journeys, not missing history.

The record supports investigation, not compliance-grade claims of completeness,
immutability, or authenticated attribution for every producer. Known gaps and
uncertain outcomes must be visible. AWS provider facts and tags remain
authoritative for account operations; history is not an event-sourced replacement
for them and cannot restore lost lifecycle state.

There is one deliberate availability dependency: in shared-history mode,
exposure-increasing actions require durable history intent before mutation.
This does not authorize an action or override its ordinary lifecycle checks.
Exposure-reducing actions must not be blocked solely by a history outage.

## Pre-redesign baseline versus target

The table below describes the pre-redesign baseline, not the checkpoint above.
The inspected baseline was commit `65c694ba48494fc1611cc85cb6c0ee1599b9f163`.

| Area | Pre-redesign baseline | Accepted target |
| --- | --- | --- |
| Storage | Local JSONL, CloudWatch, or disabled history | DynamoDB for shared deployments; JSONL for local development |
| Delivery | Synchronous best-effort writes; failures only warn | Shared durable intent/delivery tracking, retry, deduplication, and outcome reconciliation |
| Identity | Timeline lookup primarily by account name | Stable journey identity across request and account stages; distinct submissions stay separate |
| Search | Account name, owner, since, limit | Name/ID, owner, actor, event type, date range, and pagination |
| Web | Account-page history, limited to 50 events | Searchable History hub, independently addressable journeys, and embedded timelines |
| Errors | Web query failure can look empty; TUI ignores query errors | Distinct empty, unavailable, incomplete, and pending-delivery states |
| Access | Current account authorization followed by name-only history lookup | Journey- and event-scoped historical access, including ownership periods |
| Retention | No file expiry; attempted fixed CloudWatch retention | Complete open journeys, then one year after confirmed termination |

Relevant baseline code: `internal/core/audit.go`, `internal/core/service.go`
(`audit`), `internal/audit/`, `internal/cli/commands.go` (`history`),
`internal/web/server.go` (`show`), and `internal/tui/tui.go`.

The baseline's name-only lookup motivated checkpoint 0: authorization for a
current account does not authorize unrelated older records sharing its name.

## Journeys and events

- Each request submission starts a distinct request journey. Edits preserve its
  identity. Approval connects the resulting account to that same journey.
- Resubmission after denial, withdrawal, or request expiry starts a new journey,
  even when its name is identical.
- Adoption or direct creation without a request starts an account-origin journey.
  Do not invent a request or approval that never occurred.
- Names are searchable labels, never sufficient evidence that records belong to
  one journey. Organization and environment boundaries must also prevent
  accidental cross-deployment joins.
- A journey remains discoverable after its request leaves the queue or its
  account disappears from inventory. Missing inventory alone does not establish
  closure or start retention expiry.
- Event identity must support retry deduplication. Distinct attempts and outcomes
  need enough correlation to explain an operation without presenting delivery
  retries as additional business actions.
- Owner means the beneficiary at the time of an event. Actor means who initiated
  it; requester, owner, and approver are not interchangeable.

Record decisions, meaningful state changes, failures, and recovery. Routine
polling belongs in diagnostic logs. Repeated identical failures should appear as
a counted group without hiding a different failure, recovery, or state change.
Structured details support investigation; the timeline must not depend solely on
parsing human-readable messages. Details exposed to readers must respect the
same authorization boundary as the event, not become a raw provider-response dump.

## Shared storage and delivery

DynamoDB is the selected primary shared store for events, journey metadata,
historical ownership, retention metadata, and durable delivery tracking. It is a
history-only exception to the existing no-database/no-state-file architecture.
Checkpoint 1 implements the [table layout, indexes, and conditional writes](lifecycle-history-store.md)
for events and journey metadata. Durable delivery tracking remains checkpoint 2 work.

Standalone CLI and CI continue to operate directly against AWS. They do not
require a running playplace web server. Disposable runners cannot satisfy shared
delivery guarantees merely by writing retry files to their own disks: pending
work must survive the runner's deletion in shared durable storage.

Delivery is retryable and duplicate delivery must not duplicate displayed events.
A deployment must arrange periodic history maintenance even when no web server
runs: delivery retry, safe outcome reconciliation, and retention cleanup. Local
buffers may be optimizations, not the sole durability mechanism for shared mode.

### Outage policy

When shared history is configured, require durable intent before:

- account creation or adoption;
- request approval;
- lifetime extension;
- approved budget increases;
- access grants or restoration, including removal of a budget restriction.

Allow exposure-reducing actions to proceed when history cannot be recorded:
closure, expiry enforcement, access revocation, restriction, and request
cancellation. Make the recording failure explicit. Do not claim an event has
been saved if no durable destination accepted it.

Other request decisions and mutations must be classified consistently during
implementation; they must not provide an alternate route around the gates above.
An action's successful AWS outcome must not be reported as failed solely because
saving its history outcome failed.

### Uncertain outcomes

Durable intent is not proof that AWS accepted an action. A process can fail after
AWS accepts a mutation but before its result is durably recorded. AWS mutation
and history storage are not an atomic transaction.

Display **outcome unknown** until reliable evidence resolves it. Reconciliation
may observe authoritative AWS facts and record what is established, but must not
replay an AWS mutation merely to fill a history gap. If evidence cannot establish
the outcome, leave the uncertainty visible for operator investigation. A current
state that happens to match an old intent is not automatically proof that that
particular operation succeeded.

These limits apply even with durable retry: the design does not promise a
complete, exactly-once record of every external effect.

## Authorization and attribution

| Reader | Permitted history |
| --- | --- |
| Current owner | The complete journey for the account/request they now own, including earlier ownership periods |
| Former owner | Only the portions of that journey during which they were owner |
| Approver, for another owner's journey | Request, edits, purpose, decisions/reasons, and minimal provisioning outcome; not ongoing account budget, access, or closure history |
| Admin | Full organizational history |
| Direct management-credential CLI/CI operator | Trusted administrative access, rather than engineer-scoped web access |

A new owner intentionally receives earlier purposes and decision reasons within
that same journey. Reused names do not confer access to other journeys. Apply
current role checks as well as historical ownership rules; retaining ownership
history does not preserve a revoked admin or approver role.

Enforce visibility before returning events, details, search results, counts, or
pagination metadata. Hidden events must not leak through search or grouping.
When ownership or authorization cannot be established, fail closed rather than
assuming that a matching name or email grants access. Historical authorization
must remain possible for retained journeys without requiring a live account
object; this is one reason for durable journey metadata.

Distinguish self-reported operator labels from authenticated identities. Today's
CLI label is not proof of an AWS principal. An operational record must not
present it as equivalent to a verified web sign-in identity.

## Search and presentation

### Search contract

Support name or stable ID, owner, actor, event type, and a bounded date range,
with pagination. Owner and actor are separate filters: responsibility and action
attribution answer different questions. Full-text search across messages and
details is out of scope initially.

Pagination must let a reader reach the full authorized result set within
retention. Do not silently return a capped early subset as the latest results.
Use deterministic ordering, including for timestamp ties, and make partial or
unavailable results explicit. Precise cursor/index mechanics and handling of
late-arriving records need validation during implementation.

### Web

Provide a role-scoped **History** destination with search and links to individual
journeys, including pending, denied, withdrawn, expired, and closed journeys.
Embed the same timeline on relevant request/account pages rather than providing
conflicting versions of the story.

Default to newest-first, with an oldest-first option for reconstructing a story.
Show a concise event description, actor, and unambiguous timestamp; provide
expandable structured details and repeated-failure groups. Distinguish:

- genuinely empty history;
- unavailable history;
- more pages or an incomplete historical record;
- pending delivery or unresolved outcomes.

Do not turn a storage/query failure into “nothing recorded yet.” Pending delivery
indicators describe known pending work, not proof that no other gaps exist.

### CLI and TUI

The CLI receives the same structured search filters and pagination plus
structured JSON output. The TUI receives complete, paginated journey browsing;
a second fleet-search interface is not required initially. All surfaces share
event meanings and completeness/error semantics, even where layout differs.

## Retention

Keep a complete journey while it is open, then retain it for one year after:

- confirmed account closure; or
- denial, withdrawal, or expiry of an unfulfilled request.

Account expiry, a closure request, missing inventory, or an unresolved
provisioning/closure failure is not a terminal outcome. Such journeys must not
expire merely because their last event is old.

This is journey-based retention, not a rolling one-year lifetime for individual
events. Cleanup must preserve the opening request and approval while their
journey remains retained. Retention/index cleanup is an operational responsibility
of the shared backend; selecting DynamoDB alone does not implement it.

## Modes and pre-release cutover

Only two history modes are intended:

- **DynamoDB:** shared deployments with the guarantees and prerequisites above.
- **Local JSONL:** local development, not a substitute for shared durability.

There must be no silent fallback from configured shared history to local files.
Local mode does not make local history shared or durable beyond that machine.

The tool has not been publicly released for use. Remove CloudWatch and disabled
history configurations directly when this redesign is implemented: **no
transition release, compatibility window, or migration tooling is required**.
This supersedes the interview's earlier suggestion of a transition release.
There is no migration work in this design's implementation scope.

Removal of a configuration option does not authorize deleting an existing log
group or rewriting local history. Do not automatically assign old records to new
journeys by name. If legacy records are exposed, only reliably attributable
records may join a journey; ambiguous records remain admin-only. Show missing
pre-redesign history as incomplete rather than fabricating it.

## Capacity and implementation evidence

Initial target, per organization: **1,000 journeys and 100,000 events per year**,
with ordinary first-page searches targeting **two seconds**. These are design
targets, not measured performance or a guarantee already satisfied by DynamoDB.

Implementation must demonstrate at least:

- journey separation under reused names and across environments;
- full request-to-account linkage and history without live inventory;
- owner-transfer, former-owner, approver, and admin visibility, including details
  and search metadata;
- safe handling of uncorrelated legacy records;
- durable delivery after disposable-runner loss and duplicate delivery;
- correct gating of exposure-increasing actions and safe outage exceptions;
- unknown outcomes that are neither falsely failed nor blindly replayed;
- complete pagination, stable ordering, filters, and explicit read errors;
- retention anchored to confirmed journey termination, not event age;
- measured search behavior at the chosen capacity target.

The checkpoint above tracks implementation evidence after the design interview.
The store document specifies the schema and query implementation tested in
checkpoint 1. Maintenance scheduling, IAM resources, delivery/outcome encoding,
and production capacity still require implementation or validation.
