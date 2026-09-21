# Provenance: per-account budget restrictions

Date: 2026-09-21.
Artifact: `outputs/budget-enforcement-research.md`.
Research baseline: repository `playplace`, branch `feat/build-release-homebrew`, HEAD `8c99c11`. Subsequent implementation is on `feat/budget-enforcement`.

## Evidence and method

Direct parent research, without delegated agents. Used web search for discovery, fetched primary AWS documentation/blog pages, and inspected current source using LSP and symbol-body reads. A cached project graph was stale; decisive repository findings were checked against live source, not graph conclusions.

During the research phase, no application changes or tests were performed. The user subsequently selected the provisioning denylist and authorized implementation plus gap fixes. Application code, deployment examples, documentation and offline tests were then added. No live AWS account/API mutations, policy deployment, release tags, publication or tap updates were performed.

## Implementation validation

- `go test -race ./...`, `go vet ./...`, and `go build ./...` passed after the final code changes.
- Active LSP checks of changed Go files reported no errors; intermediate missing-symbol diagnostics were resolved, not suppressed in source.
- Version-pinned `templ` generation (`v0.3.1020`) was reproducible by SHA-256 comparison. The initial unversioned generator invocation lacked tool-only go.sum entries; no dependency files were changed to work around it.
- Built CLI `budget --help` smoke test passed without invoking lifecycle/AWS operations.
- `git diff --check` passed. The documentation site was not rendered (`uvx` unavailable).
- All 41 action patterns across 13 service prefixes matched AWS's public service authorization catalogs; summary and source URLs are in `budget-scp-action-validation.json`. These are action-name checks, not live IAM/SCP evaluation.
- Concrete budget action IAM permissions and resource ARNs were checked against https://docs.aws.amazon.com/service-authorization/latest/reference/list_budgets.html . Organization attachment resource types were checked against https://servicereference.us-east-1.amazonaws.com/v1/organizations/organizations.json .
- Raw retrieval of https://docs.aws.amazon.com/cost-management/latest/userguide/billing-example-policies.html resolved the examples omitted by readable extraction: the service-role example lists Organizations AttachPolicy/DetachPolicy and the Budgets trust policy uses SourceArn/SourceAccount. Runtime examples are still unverified in a live account.
- Tests cover no-op budget reconciliation, notification rotation/preservation, missing budget setup, action ownership/drift, single-account targeting, quota-independent setup validation, reverse/reset phases, restart recovery with post-update zero spend, repeated threshold enforcement, month-bound recovery intent, admin-only HTTP authorization, insufficient/unknown spend, budget validation, and closure despite budget failures.
- Operating instructions and remaining sandbox gates are in `docs/budgets.md`. Only one control-plane writer is supported; no distributed locking was added.
- https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/ was fetched directly: first two action-enabled budgets free per month, then $0.10 per additional budget per day. Documentation explicitly records this overhead. The subsequent user-authorized retirement implementation automatically removes the owned action after confirmed member closure; ambiguous ownership and accounts no longer discoverable still require manual follow-up.

## Closure-retirement follow-up (2026-09-21)

- User requested wiring retirement into confirmed closure. Added management-account `DeleteBudgetAction`, not whole-budget deletion, action reversal/reset, SCP detachment or member-resource cleanup.
- Primary reference: https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_DeleteBudgetAction.html . Fetched via `fetch_content`, response `mubg442svvc0iu`, URL index 0; full API page examined. It documents deletion by management account/budget/action ID and NotFound, ResourceLocked and permission errors, but does not specify the effect on already-applied SCP attachments.
- Re-fetched https://docs.aws.amazon.com/service-authorization/latest/reference/list_budgets.html . DeleteBudgetAction supports the budgetAction resource ARN and `aws:ResourceTag/${TagKey}` conditions. The runtime example adds a scoped, managed-tag-constrained deletion grant; no new execution-role permissions are needed.
- Safety design: fresh CLOSED checks before discovery and immediately before delete, current account ownership/OU checks, paginated action ownership/target/policy/role checks, subsequent absence confirmation, retained budget/alerts, and restart-safe retries independent of closure confirmation tags. Cleanup errors remain separate from the account lifecycle.
- Offline tests cover standby/executed deletion, pending/suspended/unknown/reopened account rejection, management-account rejection, ownership/configuration drift, duplicate and unrelated actions, pagination failures, missing budget/action, locked/denied deletion, lost delete response, post-delete tag-write failure, restart/idempotence, external/previously confirmed closure, and continuing other closures despite retirement failures.
- Re-ran `go test -race ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`: passed. Active full LSP checks on all nine changed Go files found no errors. An intermediate unused-method finding was stale; LSP references confirmed both closure call sites and the refreshed file check cleared it without source suppression.
- Live gate remains: confirm deletion of executed actions and resulting SCP attachments, closed-account Organizations metadata access, runtime IAM tag conditions, propagation, and action-fee cessation. No live AWS mutations or deployment were performed.

## Modern budget fields and Go cleanup (2026-09-21)

- Source: https://aws.amazon.com/blogs/aws-cloud-financial-management/improving-accuracy-for-your-cloud-budgeting-with-new-features-in-aws-budgets/ . Direct retrieval confirms DescribeBudget supplies modern fields for legacy budgets, and writes must not mix legacy CostFilters/CostTypes with FilterExpression/Metrics. Current Budget and ExpressionDimensionValues API references were fetched as response `mubh0mwigk4mvw`; installed SDK v1.51.0 types/enums were also read. The API/SDK metric spelling is `UnblendedCost`, not the uppercase spelling in the blog's illustrative snippets.
- New budgets now use exact LINKED_ACCOUNT equality and UnblendedCost. Existing budgets are validated through modern fields and updated with an explicit writable-field payload preserving metric, expression, billing view and period. Unchanged budgets are not rewritten merely for migration, avoiding calculated-spend resets. No runtime references to deprecated CostFilters/CostTypes remain.
- Strict scope validation accepts default/explicit equality and bounded single-child boolean wrappers; rejects missing fields, multiple accounts, additional filters, unknown matches and non-monetary/multiple metrics. Legacy-only emulator responses fail visibly rather than falling back to deprecated fields or silently widening scope.
- Regression tests cover modern creation, translated legacy responses, no-op reconciliation, all five monetary metrics, modern-only update payloads, preserved metadata and filtering, and rejection of unsafe scope before any mutation.
- Applied maps.Copy, slices.Contains and integer-range modernization, removed a one-iteration test loop, and used strings.Builder for ready/request notification bodies. Added copy-isolation/grant-idempotence tests and exact notification formatting tests. Three alleged string-concatenation loops were numeric spend accumulation, not strings; these were marked false positives and left unchanged.
- Active LSP checks on nine changed Go files reported no primary findings. Two auxiliary no-fmt warnings are false positives for formatting into strings.Builder, not logging; no source suppressions were added. `go test -race ./...`, `go vet ./...`, `go build ./...`, and `git diff --check` passed. No live AWS calls, deployment or release occurred.

## Decisive source passages

- Reversal/rearming: https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-action-review.html
  - “Reversed - The action was undone, and AWS Budgets will no longer evaluate the action for the remaining budgeted period.”
  - The next paragraph explicitly describes Reset after a manager approves increasing the current-period budget.
  - Retrieved through `fetch_content`, response `mubd9suonjqeji`, URL index 0; full page examined.
- API shape and recipient requirement: https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_CreateBudgetAction.html
  - `APPLY_SCP_POLICY`, `AUTOMATIC`, `ACTUAL`, percentage threshold, SCP policy/targets and execution role are documented request fields.
  - Subscribers: “Minimum number of 1 item. Maximum number of 11 items.”
  - Same-account role/action requirement is documented under `ExecutionRoleArn`.
  - Response `mubd9suonjqeji`, URL index 2; full extracted page examined.
- Update hazard: https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_UpdateBudget.html
  - “When you modify a budget, the calculatedSpend drops to zero until AWS has new usage data to use for forecasting.”
  - Response `mubdapbojhgcud`, URL index 3; targeted excerpts examined.
- SCP limitations: https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html
  - “SCPs do not affect any service-linked role.”
  - Member-account/root coverage, management-account exemption and external resource-policy principal example also examined.
  - Response `mubd9suonjqeji`, URL index 4; relevant passages examined.
- Monthly reset: https://aws.amazon.com/blogs/aws-cost-management/get-started-with-aws-budgets-actions/
  - “Budget actions that are focused on applying policies (IAM or SCP) will be reset at the beginning of each budget period”.
  - Response `mubddvz1bsuyf7`, URL index 2; reset passage examined. This is an older AWS launch article fetched on the research date, not live validation of present transition timing.
- Read-only policy impact: https://aws.amazon.com/blogs/mt/implement-read-only-service-control-policy-in-aws-organizations/
  - AWS warns the policy impacts IAM principals and “might therefore impact your running services.”
  - Response `mubddvz1bsuyf7`, URL index 0; examples/considerations examined. Role exceptions in that article are not endorsed unchanged for this threat model.
- Alert behavior: https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-best-practices.html
  - Approximately five weeks of usage data required for forecasts; actual alerts sent once per budget period when the threshold is first reached.
  - Response `mubd9suonjqeji`, URL index 5; relevant passages examined.
- Current quotas: https://docs.aws.amazon.com/organizations/latest/userguide/orgs_reference_limits.html
  - Retrieved table lists 10 SCPs directly attached per account and 10,240 characters per SCP. Inherited policies do not count against direct-attachment limits.
  - Response `mubdb9cxqe94j1`, URL index 2; tables examined. Recheck before deployment rather than hard-code historical quotas.
- Action state enumeration: https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_Action.html
  - Response `mubdapbojhgcud`, URL index 1; full extracted page examined, including execution/reverse/reset states.

Additional fetched primary references: ExecuteBudgetAction, UpdateBudgetAction, UpdateNotification, UpdateSubscriber, DescribeBudgetActionsForBudget, budgets-controls, budgets-action-role. Source URLs are in the main research artifact.

## Baseline-code evidence (before implementation)

- `internal/provider/aws/aws.go:409–475`, `EnsureBudget`: builds management-account linked-account monthly budget; describes then unconditionally updates existing budget; returns before notification reconciliation; new budget notification recipients are optional.
- `internal/core/service.go:482–511`, `place`: budget error is warning-only; access grant and ready notification follow.
- `internal/core/service.go:739–900`, `Refresh`: adoption-only budget setup; no normal managed-account budget reconciliation; lifecycle reconciliation must remain independent of budget errors.
- `internal/core/service.go:1144–1234`, `UpdateRequest`: pending-request editing, not an existing-account budget-change operation.
- `internal/cli/commands.go`: LSP outline and approve/extend bodies confirm operator command conventions; no existing live-account budget setter in that outline.
- `internal/web/auth.go:21–50`: authenticated Viewer distinguishes Admin from Approver; NoAuth is an operator mode, not an end-user authorization boundary.
- `deploy/aws/playplace-policy.json`: existing budget permissions are ViewBudget and ModifyBudget only; implementation needs explicit action/pass-role review.

## Limitations and decisions

- Search provider availability was intermittent (authentication/provider-unavailable and HTTP 503 failures). Direct AWS fetches supplied decisive evidence. Failed searches are not treated as negative evidence.
- AWS Budgets FAQ extraction was sparse, and the AWS Organizations service-authorization page failed readable extraction. Concrete least-privilege IAM resources/conditions still require implementation-time verification; no fully validated IAM policy is claimed here.
- An AWS re:Post implementation example was inspected for context but is not treated as a secure template: its member-editable emergency principal-tag exceptions and broad permissions are not copied.
- Unverified by execution: actual action trigger/attachment propagation, monthly reset timing, changed-budget re-evaluation timing, notification replay on recipient rotation, and central audit delivery under the chosen restriction policy.
- The user selected selective provisioning denial, not a broad write freeze. The exact shipped coverage is documented in `docs/budgets.md`; unlisted paths/services and service-linked roles remain outside its guarantees.
- Source-backed facts are distinguished from recommended application state-machine, authorization, ownership, and failure-handling design in the main document.
