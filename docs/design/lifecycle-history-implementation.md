# Lifecycle history implementation handoff

Resume here when implementing the remaining history redesign. The
[accepted design](lifecycle-history.md) is the requirements source; this file
records execution order, completion checks, and where the previous session stopped.
Read [ADR 0001](../adr/0001-dedicated-lifecycle-history.md) for the architectural
trade-off and [CONTEXT.md](../../CONTEXT.md) for terminology.

## Resume procedure

1. Inspect `git status --short`, the current branch, and its diff before editing.
   Checkpoint 0 merged into `main` at `3041cba`. Checkpoint 1 was implemented on
   that base. Preserve local modifications and untracked checkpoint work.
   The checked items below describe local evidence, not commit or merge status.
2. Read the accepted design's implementation checkpoint, then inspect the first
   unchecked checkpoint below. The source and current Git state override this
   historical snapshot if work has continued since it was written.
3. Run the relevant tests before extending that checkpoint. Last verified on the
   checkpoint-0 worktree: `go test -race ./...`, `go vet ./...`, a Go build, and
   `git diff --check` passed. The web templates were regenerated with the pinned
   templ version. These are previous results, not proof for subsequent edits.
4. Complete the checkpoint's acceptance checks and update this handoff and the
   accepted design's progress section together. Record remaining limitations and
   fresh validation results. A checked item means locally implemented and verified,
   not necessarily committed or merged.

The running local app was **not restarted** during checkpoint 0. Browser behavior
may therefore come from the old binary. Verify processes before any runtime work
and confirm with the operator before restarting their services. Test against
fakes, loopback AWS APIs, or explicitly configured LocalStack; real AWS operations
require separate authorization.

## Ordered checkpoints

### [x] 0. Journey identity and paginated readers

Implemented and locally verified; included in the same change as this handoff.

Start with these files to understand the existing seams:

- `internal/core/audit.go`, `account.go`, `request.go`, `service.go`: event/journey
  IDs, v3 request encoding, approval propagation, and provider-account origins.
- `internal/audit/search.go`, `audit.go`, `file.go`, `cloudwatch.go`: query-bound
  cursors, structured filters, deterministic ordering, and completeness reporting.
- `internal/cli/history.go`, `internal/web/history.go`, `views.templ`, and
  `internal/tui/history.go`: the current consumers.
- Corresponding `history_test.go` files, `internal/core/journey_test.go`,
  `history_failure_test.go`, and `internal/audit/search_test.go` / `cloudwatch_test.go`:
  regressions and current interface contracts.

Important limits to carry forward:

- `Service.audit` still writes best-effort. Random event IDs alone do not provide
  retry deduplication or durable delivery.
- In-flight/failed direct creation without a queue record cannot recover its
  journey after restart. Correlate using exact operation/provider request IDs,
  never account names.
- `Search` currently scans matching stored records. Its cursors provide traversal,
  not a frozen snapshot; the two-second performance target is unverified.
- Web authorization still requires a live, currently visible account. The hub,
  archived journeys, former-owner periods, and approver history are not built.
- CloudWatch and `none` remain runtime modes only because the replacement is not
  implemented yet. Their eventual removal needs no migration project.

### [x] 1. Shared store and durable journey metadata

**Depends on:** 0. Implemented and locally verified. Runtime use remains disabled.

Design the DynamoDB keys/indexes and write/read seam around the existing
`internal/audit` interface. Persist journey identity, origin, authoritative
ownership observations, request/account correlation, and terminal-outcome metadata
separately from lifecycle authority. Namespace organization/environment data.
Review CLI configuration in `internal/cli/root.go` and `config.go` before wiring it.

**Done when:** local/loopback adapter tests prove restart-safe metadata and exact
identity joins, environment isolation, complete filtered pagination and tie
ordering, and explicit missing/corrupt/unavailable behavior. Test current and
archived journeys, not just successful active accounts. Recover direct-creation
identity after a process restart without matching names. Specify the indexing
strategy for the accepted workload. Keep the shared mode out of the supported
runtime path until checkpoint 2's delivery/gating contract is tested.

Implemented in `internal/audit/journey.go`, `dynamodb.go`, and
`dynamodb_search.go`. Before extending these files, read the
[store layout and contracts](lifecycle-history-store.md) for keys, indexes,
write conditions, limits, and test instructions.

Evidence and remaining limits:

- The adapter implements `Sink` plus versioned journey writes and exact identity
  lookups. Transactions preserve correlations, ownership observations, and
  terminal metadata independently of queue/account inventory.
- In-memory and DynamoDB Local 3.3.0 contracts passed with the race detector.
  Tests cover archived requests/accounts, namespace isolation, conditional
  writes, complete filtered pagination, timestamp ties, corruption, and outages.
  A separate producer process exits and loses its working directory before a new
  client recovers a failed direct journey through exact operation/request IDs.
- Event writes and search indexes commit together. Repeated delivery of the same
  event does not duplicate search results. This is storage-level behavior, not
  the durable delivery contract required in checkpoint 2.
- CLI configuration in `internal/cli/root.go` and `config.go` was reviewed and
  left unchanged. The adapter is not wired into `Service` or exposed as a runtime
  mode. Existing direct-creation runtime recovery limits still apply until
  checkpoint 2 connects producers and identity recovery to this store.
- Ownership observations record reliable source references, not inferred owner
  periods. Authorization, pending/unknown outcomes, retention, and maintenance
  remain pending. The capacity target is unmeasured.
- Fresh validation passed: `go test -race ./...`, `go vet ./...`,
  `go build -o .generated/playplace-checkpoint1 ./cmd/playplace`, active LSP
  diagnostics, and `git diff --check`. No web templates changed.
  The isolated loopback suite also passed with
  `PLAYPLACE_TEST_DYNAMODB_ENDPOINT=http://127.0.0.1:32768 go test -race ./internal/audit -count=1`.
  The test table was deleted and the isolated test container was stopped.
  Existing application services were not restarted.

### [ ] 2. Durable delivery, operation gates, and uncertain outcomes

**Depends on:** 1. **Next work starts here.**

Replace the shared mode's best-effort-only write path with durable intent and
outcome tracking. Reuse an event/operation identity across delivery retries.
Inspect every exposure-increasing mutation in `internal/core/service.go`,
`readiness.go`, `budget.go`, and `budget_recovery.go`; cover direct creation,
approval, adoption, extension, grants, budget increases, and restriction removal.
Provide reconciliation that records reliable observations without replaying AWS
mutations merely to repair history. Classify the remaining request mutations
explicitly so another entry point cannot bypass a gate.

**Done when:** tests lose the producer process/disk, retry delivery, duplicate
responses, interrupt execution before/after AWS acceptance, and fail history
storage. They prove shared durability, no duplicate displayed events, refusal of
exposure-increasing mutations before durable intent, and continued safety actions
with explicit gap warnings. Successful AWS outcomes are not relabeled failed;
unprovable outcomes remain unknown. Core continues deriving lifecycle decisions
from provider facts, not history. Record how unresolved work is surfaced and retried.

### [ ] 3. Historical authorization and archived journey queries

**Depends on:** 1 and the recording semantics from 2.

Build one history-read policy used by the web hub and embedded timelines. Use
current roles plus reliable historical ownership metadata, including journeys
whose queue/account objects no longer exist. Apply visibility before filtering,
counts, grouping, and pagination metadata. Preserve the distinction between
self-reported CLI operator labels and authenticated web identities.

**Done when:** tests cover current owners seeing their full journey, former owners
seeing only their periods, approvers seeing decision context/minimal provisioning
outcomes, and admins seeing all. Cover owner transfers, role revocation, missing
ownership evidence, archived requests, reused names, cross-environment data,
legacy uncorrelated records, and direct URL/cursor/filter manipulation. Hidden
events and details cannot leak through results, counts, cursors, or caches.

### [ ] 4. History hub and complete presentation states

**Depends on:** 2 and 3.

Add searchable History and independently addressable journey pages, then link
request/account screens to those journeys. Reuse the existing pagination and
reader semantics in web/CLI/TUI. Add failure grouping and visible known pending
or unknown-outcome states; retain expandable, escaped structured details.

**Done when:** route/render tests exercise all roles, every terminal request path,
accounts absent from inventory, full pagination in both supported orders, and
query errors versus empty/incomplete/pending history. Repeated failures remain
investigable without hiding changes or recovery. CLI JSON and TUI browsing work
with the new shared backend. Regenerated templ output matches source. Full-text
search and a TUI fleet-search interface remain outside the accepted scope.

### [ ] 5. Retention and independently scheduled maintenance

**Depends on:** 1 and 2; use 3's access rules for retained metadata.

Provide a documented maintenance invocation/scheduling path that works without a
web server. It must handle delivery/outcome reconciliation and journey-based
retention. Define behavior for retries, partial cleanup, and late event delivery.

**Done when:** controlled-clock tests preserve entire open/unresolved journeys,
start the one-year clock only on accepted terminal outcomes, and remove the
expired journey's events/index/authorization metadata consistently. Exercise
interrupted cleanup, concurrent or late delivery, and repeated maintenance;
expired data must not be silently resurrected. An expired account, closure
request, or missing inventory object must not alone authorize history deletion.
Document deployment scheduling and failure monitoring.

### [ ] 6. Runtime cutover, deployment, and final evidence

**Depends on:** 1–5.

Expose the supported DynamoDB/local-file modes and remove CloudWatch/disabled
configuration paths, dependencies where unused, and obsolete documentation.
Update deployment IAM/provisioning and LocalStack development tooling. Local JSONL
remains explicitly local; configured shared history never silently falls back.
Removing a mode does not authorize deleting existing log groups or local records.

**Done when:** configuration tests reject retired modes; deployment/dev docs
reproduce setup; all applicable store/consumer contracts pass; and the full suite,
`go vet`, generated-view check, and build pass. Validate structured search at
1,000 journeys / 100,000 events per organization per year against the two-second
first-page target. Record dataset, environment, query mix, and latency evidence;
emulator results are not production AWS evidence. Report any unverified target
rather than marking it complete. Update the design's progress status only when
all requirements and residual limitations are accounted for.

## Validation notes

Generate views with the version pinned in `go.mod`, using `generate -path
internal/web` (or generating from inside `internal/web`). The positional command
`templ generate ./internal/web` used by the current Taskfile can change generated
source-path strings unnecessarily.

`scripts/check-generated.sh` also requires a clean, committed `internal/web`
working tree; its diff check will fail on intentional uncommitted changes. While
working, regenerate and inspect generated diffs; use the release check after the
relevant changes are committed. Keep running-service binaries separate from
verification builds, for example under `.generated/`.
