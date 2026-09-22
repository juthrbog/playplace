# Budget protection

By default, budgets are **alerts-only**, not spending caps. Configure the optional
budget action to deny covered provisioning APIs in an individual account when
AWS reports spending above its monthly budget. Running resources, data-plane
usage, billing lag, and exempt AWS service-linked roles can still incur charges.

## Cost and decommissioning

[AWS pricing](https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/),
checked 2026-09-21: monitoring/notifications are free; the first two action-enabled
budgets per month are free, then each additional action-enabled budget costs
**$0.10 per day**. This is charged for enabled budgets, not only triggered actions.
Plan for this fleet-wide overhead before enabling one action per account.

### Automatic action retirement after closure

**AWS account closure alone does not delete the management-account action.**
Playplace retires its owned action once the member is confirmed `CLOSED`:

1. Recheck AWS `Account.State`, management-account exclusion, account ownership
   tags and direct Playground OU membership. `PENDING_CLOSURE`, `SUSPENDED`, old
   closure tags, and a successful `CloseAccount` response are not sufficient.
2. Discover the action across all pages and verify playplace ownership tags,
   its single-account target, policy ID and execution role. Ambiguous ownership,
   duplicate actions and drift require operator review rather than broad deletion.
3. Recheck `CLOSED` immediately before `DeleteBudgetAction`. Record `retiring`;
   only a later observation of absence records `retired` and clears recovery intent.
4. Retry during normal reconciliation, including after restarts, externally
   initiated closures and previously recorded closure confirmation. An error
   remains visible in budget health and reconciliation output without undoing
   closure or blocking other accounts' closure. State transitions are audited;
   errors and completion use the lifecycle notifier.

The budget and notification subscribers are retained to monitor residual bills.
No reverse/reset, whole-budget deletion, direct SCP detachment, trail deletion,
member-role assumption or member-resource cleanup is performed. Unrelated actions
are preserved; if any remain, that budget can still incur action-enabled fees.
`retired` means the owned action is absent, **not** that all account charges ended.

Keep reconciliation running and the original policy/role configuration available
until retirement completes. Deploy the updated `playplace-budget-actions-policy.json`:
its additional `budgets:DeleteBudgetAction` grant is restricted to `playplace-*`
action ARNs carrying `playplace:managed=true`. Permission failures or AWS resource
locks are retried, not worked around by reversing the action. Accounts moved out
of the OU or no longer discoverable require manual follow-up.

The DeleteBudgetAction API reference does not specify the effect on an already
applied SCP. Verify deletion of both standby and executed actions, resulting
attachments, IAM tag conditions, and billing in an authorized disposable sandbox.
Do not reopen a retired account casually: restore and verify protection before
returning it to use; an orphaned restriction attachment requires operator review.

## Enable per-account restrictions

Deploy infrastructure separately, using management-account credentials:

1. Enable service control policies in your organization and preserve normal
   allow policies, including `FullAWSAccess` where needed.
2. Review and create `deploy/aws/budget-provisioning-deny-scp.json` as a customer
   managed SCP. **Do not attach it to the Playground OU or root.** AWS Budgets
   attaches it to individual accounts only when their thresholds are crossed.
3. Create a management-account IAM role named `PlayplaceBudgetActions`, using
   `deploy/aws/budget-actions-trust.json` and
   `deploy/aws/budget-actions-execution-policy.json`. Replace
   `MANAGEMENT_ACCOUNT_ID`, `ORGANIZATION_ID`, and `BUDGET_POLICY_ID`. Adjust the
   role path if appropriate. The examples target the standard AWS partition;
   other partitions and endpoints have not been validated.
4. Give playplace's runtime role the existing `playplace-policy.json` plus
   `deploy/aws/playplace-budget-actions-policy.json`, replacing its management
   account/role placeholders. This permits scoped budget-action management and
   pass-role, **not direct SCP attachment, detachment, creation or editing**.
5. Configure every playplace process consistently:

   ```sh
   export PLAYPLACE_BUDGET_SCP_ID=p-xxxxxxxx
   export PLAYPLACE_BUDGET_ACTION_ROLE=arn:aws:iam::123456789012:role/PlayplaceBudgetActions
   export PLAYPLACE_ALERT_EMAIL=aws-operations@example.com
   ```

6. Run one controlled reconciliation and inspect protection health for all
   existing accounts before relying on enforcement. Newly configured AWS actions
   must be observed on a subsequent pass before new owner access is granted.
   Budget/action failure now withholds new grants, rather than silently allowing
   an account to proceed without its budget. Already-issued access is not revoked
   solely because a budget health check fails.

The initial **account ready** notification waits for observed protection, successful
managed owner access when enabled, and no active budget restriction. It can arrive
on a later reconciliation after setup or access retries. New creations and newly
adopted accounts receive this initial handoff once; already-managed accounts do not
receive retroactive notices on upgrade. Later budget recovery keeps its separate
health notifications. Delivery is best-effort: the handoff is recorded before sending,
so a crash or delivery failure can lose the notice rather than repeat it.

The execution policy permits only the designated SCP, but its account ARN
pattern covers members of the organization. Narrow that account list if your
infrastructure maintains one; playplace additionally validates direct Playground
OU membership before configuring an action. Do not give playground users access
to the management role, budget actions, or playplace's organization tags.

The purchase/trail safeguards SCP is **separate and remains attached**. Budget
recovery never detaches it. Do not reuse the budget SCP for manual quarantines:
its account attachments must be owned solely by budget actions.

### Exactly what the example denies

The JSON file is the authoritative action list. Its reviewed scope is:

| Service | Provisioning paths covered |
| --- | --- |
| EC2/EBS/networking | `Create*`, instance launches, spot requests, address allocation, imports, image/snapshot copies, image registration and restores |
| RDS | `Create*`, restores and starting automated-backup replication |
| S3 | New buckets, **not** object writes in existing buckets |
| Lambda | New functions and published versions |
| ECS/EKS | Creation, task launches, ECS service updates and EKS node-group configuration updates |
| Auto Scaling | Creation, desired-capacity/group updates and new scheduled updates |
| CloudFormation | Stack creation/update/change-set execution and StackSet/stack-instance provisioning |
| DynamoDB | Creation, restores and imports |
| Load balancing / ElastiCache | `Create*` |
| Redshift | `Create*` and restores |
| SageMaker | `Create*` and pipeline execution |

Some covered APIs can also modify existing resources, so this is not a promise
that every existing workload operation remains unaffected. Other services and
provisioning paths are **not comprehensively covered**. Extend and test the list
for your allowed services; deny access to unsupported services separately if
that is required by your organization. Service-linked roles are exempt from SCPs:
existing scaling/orchestration can still provision through those roles. Do not
add a member-editable principal-tag or administrator-role bypass.

The example does not deny log delivery, CloudTrail operations, KMS data-key
creation, resource inspection, or common stop/delete operations. It does not
perform cleanup. Verify actual cleanup dependencies and **central CloudTrail
delivery** in a disposable sandbox before deployment; offline policy tests only
check action-pattern coverage, not AWS policy evaluation.

## Increase an existing account's budget

Admins can use **Change monthly budget** on the account page. Owners and
non-admin approvers cannot use this operation. Operators can use the CLI:

```sh
playplace budget dev-alex --amount 100 --reason 'Approved additional testing'
# Explicitly exceed the configured request ceiling if authorized:
playplace budget dev-alex --amount 750 --override-limits --reason 'Approved exception'
```

This changes the **recurring monthly limit**, not a lifetime or single-month
allowance. Amounts must be positive USD values with at most two decimal places.
The actor, reason, old/new limits and override are recorded in history.

For an account restricted at $60 against a $50 budget, an increase to $100:

1. Saves the approved amount and recovery intent in organization tags.
2. Updates the AWS budget.
3. Requests reversal of the specific owned action; waits for `REVERSE_SUCCESS`.
4. Persists the reset phase, then requests reset; waits for `STANDBY` and observes
   the restriction SCP absent from the account's direct attachments.
5. Reports `ready`. AWS can restrict the account again when the new limit is
   exceeded. Other organization policies remain in effect.

An increase only to $55 does not authorize recovery from the known $60 spend.
Unknown or zero/recalculating spend on an executed action prevents automatic
recovery approval. AWS documents that changing a budget temporarily resets its
calculated spend to zero; playplace checks spending **before** changing the
budget and persists that evidence. It does not interpret the temporary zero as
new available budget.

Recovery is asynchronous. A CLI error saying **change saved; reconciliation
pending** means the intent was saved, not rolled back. Run reconciliation or let
the worker continue; inspect `show ACCOUNT` or the account page. Do not repeatedly
submit new increases while recovery is pending. A restart resumes the phase;
an action that re-executes after reset is not reversed again using the old
approval. Recovery approvals do not carry into a new monthly budget period.
AWS documents policy-action resets at the next period; playplace observes AWS
state rather than reattaching last month's restriction from local state.

## Reliable operation and repair

- Use **one control-plane writer**. Organization tags do not support atomic
  compare-and-swap across processes. Do not run multiple service replicas,
  CLI mutations, or scheduled reconciliations concurrently. Schedule an
  independent reconciler only with external serialization against the service.
- Every eligible active managed account is reconciled, including accounts whose
  initial budget setup failed. Expiry and closure continue even when budget APIs
  fail. Existing budgets are updated only when their limit actually differs.
- Budget reads and writes use `FilterExpression` and `Metrics`, not deprecated
  `CostFilters`/`CostTypes`. New budgets use exact `LINKED_ACCOUNT` matching and
  `UnblendedCost`. AWS supplies modern fields when describing legacy budgets;
  updates preserve the returned monetary metric, account scope, billing view and
  period, and never send both formats together. There is no representation-only
  update that would reset calculated spend. Missing modern fields, broader or
  narrower account filters, and unsupported metrics fail visibly; older API
  emulators must support the modern response. Existing custom charge-type or
  service subsets require operator review rather than silently changing scope.
- `playplace:budget` is the desired approved amount. Console changes to the AWS
  budget are repaired back to that value. Use the admin operation, not an
  out-of-band AWS budget edit, for an increase and automatic recovery.
- Budget health is separate from lifecycle status: `alerts-only`,
  `alerts-disabled`, `setup-pending`, `ready`, `restricting`, `restricted`,
  `recovering`, `retiring`, `retired`, or `error`. The displayed timestamp records a state/error change,
  **not a heartbeat**. Monitor missed/failed reconciliation independently.
- `--alert-email` is mandatory in enforcement mode. In alerts-only mode an empty
  value is explicitly reported as `alerts-disabled`, with a startup warning.
- Playplace owns **EMAIL subscribers on the exact 80% actual and 100% forecast
  greater-than percentage notifications** of its `playplace-*` budgets. Recipient
  changes reconcile existing budgets too, including legacy ones. SNS subscribers
  on those notifications and all other notification thresholds are preserved.
  Keep additional manually managed email alerts on separate thresholds.
- Action recipients are reconciled independently; AWS may lock updates during
  transitions. Errors remain visible and are retried. Forecast alerts require
  usage history and should not be relied on for newly created accounts.
- Missing, duplicate, unowned, or mismatched actions and unexpected attachments
  fail visibly. No general-purpose policy detach is used as a repair shortcut.
  Changing/removing the configured SCP requires a deliberate migration; it does
  not silently disable existing protection.
- Execution, reverse and reset failures remain errors. Reverse/reset operations
  with a matching durable intent are retried; an execution failure or manual
  reversal without such an intent requires operator review in AWS Budgets.
  After an out-of-band reversal, **Reset** is required to rearm the action for
  the current period. Do not delete ownership/recovery tags to silence errors.
- Budget state changes are audited. Error/restriction/recovery transitions also
  use the configured lifecycle notifier (for example `--slack-webhook`); AWS
  action notifications use `--alert-email`. Delivery is best-effort, not an
  exactly-once guarantee.

### Damaged account guardrail tags

Ownership is not lost when an owned account's expiry or approved budget is
missing or invalid. Such accounts remain owned, with actionable field diagnostics
and unknown values instead of a new default lifetime or budget. Nonempty malformed
`playplace:close-requested` is also a repair error, not permission to prepare the
account or evidence sufficient to close it.

Until the required facts are repaired, playplace withholds preparation: no new
access, initial handoff, budget update, reverse or reset. Existing grants and AWS
protection remain in place; the last budget-health observation is not a claim
that reconciliation is still succeeding. A valid expired lifetime or valid close
intent still authorizes closure despite unrelated damage. Explicit closure and
confirmed-CLOSED retirement also remain available. Missing/bad expiry or budget
on a CLOSED account does **not** require repair before safe action retirement.

Repair is an operator task, using management-account credentials:

1. Serialize with the sole writer: stop the worker/scheduled reconciliation and
   prevent concurrent CLI mutations. Record the account ID, observed AWS state,
   raw tags, reviewed approval, operator and repair reason in your incident record.
2. Inspect without an automatic refresh:

   ```sh
   playplace --no-refresh list --all
   aws organizations describe-account --account-id "$ACCOUNT_ID"
   aws organizations list-tags-for-resource --resource-id "$ACCOUNT_ID"
   ```

3. Establish the **reviewed approved fact**, not a guessed default. Do not extend
   an unknown lifetime or raise an unknown budget merely to clear the error.
   Logs can help investigation; they are not automatically authoritative state.
   If you cannot establish the approved facts, leave preparation paused or use
   explicit closure (`playplace --no-refresh close ACCOUNT -y`). Normal `extend`
   and `budget` commands, including overrides, do not repair missing baselines.
4. Write only the damaged fields you have reviewed. For example, after setting
   `REVIEWED_EXPIRY` to the approved RFC3339 timestamp, or `REVIEWED_BUDGET` to the
   approved positive USD amount with at most two decimal places:

   ```sh
   aws organizations tag-resource --resource-id "$ACCOUNT_ID" \
     --tags "Key=playplace:expires,Value=$REVIEWED_EXPIRY"
   # Only if the budget tag also requires repair:
   aws organizations tag-resource --resource-id "$ACCOUNT_ID" \
     --tags "Key=playplace:budget,Value=$REVIEWED_BUDGET"
   ```

   Inspect malformed close intent separately. Restore a verified timestamp if
   closure was intended; remove that tag only after establishing that no close
   intent should remain. An explicit `close` command is preferable when requesting
   closure now. AWS `PENDING_CLOSURE` still drives closure monitoring regardless of
   the tag; playplace may establish a new observation clock, not the lost original
   request time. Never delete ownership, policy, recovery or handoff markers to
   make an account look unowned or to bypass a safety check. Restoring a budget
   tag is not a new recovery approval.
5. Reread the tags, then run `playplace reconcile` as the sole writer. Inspect
   diagnostics and budget health before resuming the worker. A repaired account
   resumes eligible work, but a legacy account does not receive a retroactive
   initial-handoff notice. Raw AWS repairs do not produce a playplace mutation
   history event; retain the incident record and AWS audit evidence.

Enabling actions does not validate the execution role's effective IAM permissions
by actually executing a restriction. An accepted action configuration is not proof
that it will succeed later. Test the role, attachment quota, selected denies,
central audit delivery, reverse/reset, action retirement, and monthly rollover in a disposable AWS
account. LocalStack and HTTP mocks cannot establish those behaviors.

## Sources and limits

- [AWS budget actions](https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-controls.html)
- [Reversing and resetting after a budget increase](https://docs.aws.amazon.com/cost-management/latest/userguide/budgets-action-review.html)
- [Deleting a budget action](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_DeleteBudgetAction.html)
- [Modern budget fields and legacy-budget compatibility](https://aws.amazon.com/blogs/aws-cloud-financial-management/improving-accuracy-for-your-cloud-budgeting-with-new-features-in-aws-budgets/)
- [UpdateBudget resets calculated spend](https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_budgets_UpdateBudget.html)
- [SCP effects and exceptions](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html)
- [Budget-action IAM permissions](https://docs.aws.amazon.com/service-authorization/latest/reference/list_budgets.html)
- [Confused-deputy protection](https://docs.aws.amazon.com/cost-management/latest/userguide/cross-service-confused-deputy-prevention.html)
- [Organizations quotas](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_reference_limits.html): checked 2026-09-21; ten directly attached SCPs per account and 10,240 characters per SCP. Inherited SCPs do not count against direct-attachment slots.

Action-name patterns in the supplied policy were checked against AWS's public
service authorization JSON catalog on 2026-09-21. This is **not live enforcement
validation**. No SCP can turn delayed billing into a hard dollar cap.
