# Use a dedicated shared store for lifecycle history

**Status: accepted; implementation pending.**

Playplace will use DynamoDB for shared lifecycle history, allowing durable
journey metadata and delivery tracking as a narrow exception to its no-database
architecture. This supports standalone AWS-authenticated CLI/CI clients,
request-to-account investigations, historical ownership permissions, and
journey-based retention without requiring a running playplace web server.
AWS tags and provider facts remain authoritative for account operations;
history is an operational record, not an event-sourced state model or an
authoritative audit trail.

## Trade-off

The existing optional JSONL/CloudWatch log is simpler, but best-effort delivery,
name-only timelines, and per-event retention do not meet the agreed requirements.
A CloudWatch-plus-metadata design would add two-store consistency concerns; a
relational store would add database operations and a secure client access path.
DynamoDB fits the AWS-only operating model, at the cost of explicit index design,
shared infrastructure, and scheduled reconciliation and retention cleanup.

## Consequences

- Shared-history mode requires durable intent before exposure-increasing actions.
  Exposure-reducing actions may proceed through a history outage with explicit
  recording-failure warnings. This is an availability prerequisite, not permission
  to use history to infer safe account state.
- Durable delivery does not make AWS mutations and history writes atomic.
  Unconfirmed outcomes remain unknown until reliable evidence resolves them;
  history repair must not replay mutations.
- Direct management-credential operators remain trusted administrators. Web
  visibility is scoped by role and historical ownership; new owners intentionally
  inherit the complete journey, while former owners retain only their periods.
- Local JSONL remains a development mode, not an automatic shared-store fallback.
  CloudWatch and disabled-history modes will be removed without a transition
  release or migration work because playplace has not been publicly released.
- The accepted target is detailed in [Lifecycle history redesign](../design/lifecycle-history.md).
  Its capacity and delivery requirements still require implementation evidence.
