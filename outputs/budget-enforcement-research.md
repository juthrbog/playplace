# Per-account budget restrictions: research and implementation proposal

Research date: 2026-09-21. Repository inspected: `8c99c11`.
This is the research-stage proposal, retained as a decision record. The user
subsequently selected the provisioning denylist and authorized implementation.
See [Budget protection](../docs/budgets.md) for the implemented behavior and
[validation provenance](budget-enforcement-research.provenance.md) for local
checks and remaining sandbox gates. Nothing has been deployed to AWS.

## Recommendation

Use one **automatic AWS Budgets SCP action per playground account**, attached to its existing monthly linked-account budget. Trigger on **100% ACTUAL spending**, using a percentage threshold so a changed budget changes the trigger amount. AWS runs enforcement even when playplace is offline. Keep playplace responsible for setup, repair, health reporting, and admin-requested budget changes. [1, 2]

Deploy the restriction SCP and the AWS Budgets execution role separately. Configure playplace with their identifiers; do not add general-purpose organization policy creation/editing to its runtime role. The existing purchase/auditing safeguards SCP remains independent and must never be detached by budget recovery.

The action definition would be:

```text
ActionType: APPLY_SCP_POLICY
ApprovalModel: AUTOMATIC
NotificationType: ACTUAL
ActionThreshold: PERCENTAGE, 100
Definition.ScpActionDefinition.PolicyId: configured restriction policy
Definition.ScpActionDefinition.TargetIds: [this member account ID]
ExecutionRoleArn: dedicated role in the management account
Subscribers: configured operations email (required in enforcement mode)
```

This is a design sketch, not a runnable deployment manifest. The API requires at least one action subscriber. Budget/action execution role and budget actions must be in the same account. [2]

## Important policy choice

An SCP can deny API operations, not a universal semantic category named “new resource creation.” Two reasonable approaches:

| Approach | Benefit | Cost / limitation |
| --- | --- | --- |
| Reviewed provisioning denylist | Closest match to stopping new creation without generally freezing existing workloads | Must enumerate supported creation, launch, restore, replication and deployment paths per service; unlisted paths/services remain allowed |
| Conservative write freeze with narrowly reviewed exceptions | Denies more unknown/future provisioning paths | Also prevents modifications and can break existing applications; read/cleanup/auditing exceptions need careful design |

Recommendation for the stated goal: a **reviewed provisioning denylist**, with its exact service/API coverage documented and tested, rather than claiming universal AWS-wide prevention. If comprehensive denial of ordinary-principal writes is more important than preserving workload behavior, choose the write freeze instead.

Do not rely on `Create*` alone: launching EC2 instances uses `RunInstances`, for example. Do not assume `Get*` or `List*` is a sufficient security classification, or broadly exempt runtime/deployment roles. Do not provide a member-editable principal-tag bypass. A member administrator could otherwise escape the restriction.

SCPs restrict member IAM identities, including root, but **not service-linked roles**, management-account identities, or all external-principal resource-policy access. Existing AWS-managed automation can therefore still create resources through exempt execution paths. Neither policy option is an absolute account-wide provisioning barrier. [5]

AWS's read-only SCP article also explicitly warns that broad restrictions can affect running EC2/Lambda/ECS workloads. Do not copy its role exceptions without separately protecting those roles. [10]

Keep central CloudTrail delivery intact. Do not stop/delete trails, move accounts to a different OU as a workaround, or add automated cleanup. Audit delivery and operator cleanup access require sandbox tests for the selected policy.

## Admin increase and unblocking

AWS explicitly documents raising a budget during the current period after a read-only action has run. The recovery sequence is **reverse, then reset**: a reversed action is otherwise no longer evaluated for the remainder of the period. [3, 4]

Proposed behavior:

1. Provide an admin-only existing-account budget operation in the web UI and operator CLI. Pending-request edits are not enough. Record actor, reason, old/new amount, account ID, operation identity, and outcome.
2. Reject invalid/nonfinite values; preserve request-ceiling rules with an explicit audited admin override. Changes update the recurring monthly limit, not a separate lifetime cap or one-month-only allowance.
3. Read the current budget and action before mutation. Persist the desired change and necessary pre-update spend evidence so interrupted operations can resume. Serialize budget mutations; an in-process mutex is not multi-process coordination.
4. For an increase above the latest usable reported spend, apply the new budget limit. If the account has a completed, playplace-owned restriction action, request `REVERSE_BUDGET_ACTION`.
5. Observe `REVERSE_SUCCESS`, then request `RESET_BUDGET_ACTION`; confirm `STANDBY`. Reset is what rearms enforcement against the increased limit for the rest of the month.
6. Confirm the specific restriction SCP is no longer directly attached before reporting “restriction lifted”; rearming is a separate health condition. Other SCPs still apply and policy propagation is not instantaneous.
7. Resume/retry in-progress and failure states on reconciliation. Do not treat an accepted API call as completion, or restart reverse/reset on every pass. Missing/foreign/ambiguous actions require explicit repair, not unconditional policy detachment.

Example: an account restricted after $110 of reported spend against a $100 budget can be given a $200 monthly limit; the restriction is reversed and the action rearmed for $200. Raising it only to $105 should **not** unlock it based on the known $110 spend. Delayed charges may still cause another restriction after a seemingly sufficient increase.

A **critical AWS behavior**: `UpdateBudget` temporarily resets `CalculatedSpend` to zero until new usage data is available. Never use that post-update zero as evidence that spending fell below the threshold. Avoid rewriting unchanged budgets during reconciliation; preserve usable pre-update evidence and leave recovery pending when the decision cannot be justified. [6]

AWS documents policy actions resetting at the next budget period. This fits a recurring monthly budget, but the exact attachment/state transition and timing need live validation. Do not automatically reattach last month's restriction from a stale local flag. [9]

## Fixes to the current gaps

| Gap observed at `8c99c11` | Proposed fix |
| --- | --- |
| `place` warns on budget failure, still grants access and sends “ready” | In enforcement mode, withhold new owner access and readiness until budget, notifications and action are configured successfully. Persist setup-pending/error state and retry. This alone does not revoke existing sessions or already-provisioned credentials. |
| `Refresh` only ensures a budget during adoption, not every managed-account pass | Reconcile budget/action health for every eligible active managed account, including setup failures after placement. Continue expiry/closure handling even if budget APIs fail. |
| Existing-budget path unconditionally calls `UpdateBudget`, then returns | Diff owned budget fields; make no update when unchanged. Independently reconcile alerts/subscribers and action configuration. This avoids repeated calculated-spend resets. |
| Alert email optional | Require an operations recipient when enforcement is enabled; fail configuration validation rather than silently creating unenforced/no-notification accounts. Preserve an explicitly labeled alerts-only mode if backward compatibility is needed, with a clear warning when notifications are disabled. |
| Existing notification subscribers never change | List and reconcile playplace-owned 80% actual / 100% forecast notifications and subscribers through the notification/subscriber APIs. Handle empty-to-set, rotation, removal in alerts-only mode, missing notifications, pagination, duplicate-create races and partial failure. Do not delete unrelated operator configuration. [7, 8] |
| No existing-account budget mutation | Add admin UI + CLI flow described above; owners and approvers without admin rights must not gain this privilege. Web authorization must use the authenticated viewer, not a submitted actor/email field. CLI authorization remains the management IAM boundary. |
| No enforcement visibility | Report configured / setup pending / restricted / recovery pending / error distinctly, including action state, last error and checked time. Notify operators on execution, reverse and reset failures without flooding every refresh. |

Additional alert consideration: forecast alerts require approximately five weeks of usage history. Keep the actual-spend warning and actual-spend restriction; do not depend on forecasting for newly created accounts. Actual-spend alerts are normally once per threshold/budget period, so changing recipients does not imply an immediate replay of an earlier warning. [11]

For an already-active account, a failed health check does not prove either “protected” or “unprotected.” Preserve known restrictions and display uncertainty; do not detach an SCP just because a budget/action lookup failed. A stronger outage response (revoking existing user access) would be a separate operator policy.

## Permissions and deployment

- Keep all budget actions in the management account and target **individual member accounts**, not the Playground OU. Never target the management account.
- Separate role for AWS Budgets to attach/detach only the designated restriction policy to authorized playground targets. Restrict trust and `iam:PassRole` appropriately; verify the concrete policy against AWS service authorization documentation during implementation.
- Runtime needs scoped budget-action create/read/update/execute permissions in addition to current `budgets:ViewBudget` / `budgets:ModifyBudget`, plus narrowly scoped pass-role and organization policy/attachment inspection as required.
- Validate organization SCP support, the configured policy, role/account identity, target OU membership, and available attachment capacity. Current Organizations documentation lists **10 directly attached SCPs per account** and a **10,240-character SCP limit**; do not copy old five-policy/5,120-character assumptions. Preserve normal allow-policy inheritance. [12]
- Use action ownership markers and a stable per-account identity; action creation returns a generated UUID, so paginate/discover before creating and handle duplicate creation/concurrent processes deliberately.
- Do not reuse this policy for unrelated manual quarantines: reversing a budget action must not lift an independently imposed restriction. Keep other restrictions under separate policies/ownership.
- Budgets console edits are not automatically a safe alternative to the application flow: specify which configuration is authoritative. Recommended: playplace's persisted approved amount; out-of-band differences are visible drift, and admins use the explicit budget-change operation.

## Implementation and acceptance gates

1. Refactor budget setup into a read/diff/reconcile path and test no-op behavior before adding periodic budget repair.
2. Repair notifications and surface setup failures without blocking closure processing.
3. Add externally deployed policy/role examples, configuration validation and action reconciliation.
4. Add persistent admin budget-change/recovery workflow, permissions, audit events and UI/CLI state.
5. Add fake-provider state simulation and AWS HTTP/API tests for all action states, partial failures, pagination and no-op reconciliation.
6. Verify raising above spend unlocks and rearms; raising below known spend does not; delayed zero spend after `UpdateBudget` does not unlock; restart during update/reverse/reset resumes safely; unrelated SCPs remain untouched.
7. Verify owner and non-admin approver requests are rejected, active accounts are not erroneously marked ready, and budget failures never suppress expiry/closure.
8. Separately authorized disposable AWS sandbox test: attach/action/reverse/reset/reattach, selected denied APIs, allowed reads/cleanup, central CloudTrail delivery, service-linked-role limitations, actual budget-evaluation timing, and month rollover. Local mocks cannot establish these AWS behaviors.

No release tags, publication, tap updates, live policy deployment, or resource creation are part of this research.

## Sources

1. [Configuring budget actions](https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-controls.html)
2. [CreateBudgetAction API](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_CreateBudgetAction.html)
3. [Reviewing, reversing and resetting budget actions](https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-action-review.html)
4. [ExecuteBudgetAction API](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_ExecuteBudgetAction.html)
5. [Service control policy scope and exceptions](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html)
6. [UpdateBudget API and calculated-spend reset](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_UpdateBudget.html)
7. [UpdateNotification API](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_UpdateNotification.html)
8. [UpdateSubscriber API](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_UpdateSubscriber.html)
9. [AWS Budgets Actions launch: policy-action reset each budget period](https://aws.amazon.com/blogs/aws-cost-management/get-started-with-aws-budgets-actions/)
10. [AWS read-only SCP design and running-workload considerations](https://aws.amazon.com/blogs/mt/implement-read-only-service-control-policy-in-aws-organizations/)
11. [AWS Budgets best practices: forecasts and notification cadence](https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-best-practices.html)
12. [AWS Organizations quotas and limits](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_reference_limits.html)
