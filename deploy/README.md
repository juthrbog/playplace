# Deploying playplace

playplace must run with management-account credentials, because AWS allows
`CreateAccount` and `CloseAccount` only from there. Run `playplace serve` as
one process. It gives the web UI, the Slack endpoint, and a refresh every
minute. Run it on one small host in the management account (ECS, App Runner,
or a VM) behind HTTPS, with an instance or task role that carries
`aws/playplace-policy.json`.

AWS tags have no compare-and-swap across processes, so playplace supports one
writer. The `serve` process is that writer: its worker refreshes the fleet,
and its web UI and Slack actions change requests and accounts. While it runs:

- Make changes in the web UI or in Slack, not with the CLI or the TUI. Those
  write from a second process, and the TUI `S` key runs a full refresh.
- Give read-only commands (`list`, `show`, `requests`, `costs`) the
  `--no-refresh` flag. Without it, each one runs its own refresh pass first.
- Do not run a second `serve` or a scheduled `reconcile`.

`serve --no-worker` turns the worker off, for example to run `reconcile` from
a scheduler instead. The web UI still writes when people act in it, and
nothing locks those writes against `reconcile`. Read
[Reliable operation and repair](../docs/budgets.md#reliable-operation-and-repair)
before you choose this. If you do, stop runs from overlapping, for example
with `flock -n` in cron or with a systemd timer. Every 15 minutes is a
reasonable start. A daily schedule can leave an expired account active for
almost a day before closure starts. Keep the interval at 30 minutes or less.
Budget protection is settling while a later pass must finish its setup,
restriction, recovery or retirement. Protection that is still settling after
two hours fails the pass, and a budget recovery can take four passes.

Alert when `serve` stops and on its error logs. A pass that fails or panics
logs `refresh failed` or `refresh panicked` at error level. A closure that
stays unconfirmed past `--close-alert-after` fails the pass. Budget
protection that is still settling after two hours also fails the pass. Each
pass stops after five minutes, and the worker runs as long as `serve` does,
so a stopped process is the only cause of missed passes.

## The Playground OU

Playplace keeps playground accounts in one OU, the Playground OU. It finds
the OU in one of two ways:

- `--ou-name` (default `Playground`) finds the OU by name. The OU must be a
  direct child of the organization root. `playplace init` creates it there
  when it is missing.
- `--ou-id` finds the OU by its ID, for example `ou-ab12-cd34ef56`. The OU
  can be at any depth, for example `Root / Sandbox / Playground`. Use it for
  an OU that already exists inside another OU. `playplace init` never
  creates an OU for it.

Set only one of them. Playplace refuses to start when you set both, because
it would ignore `--ou-name`.

Playplace treats every account in the Playground OU as its own. Every
refresh, not only the first, adopts each active account in the OU that does
not have the `playplace:managed=true` tag. This is true for `--ou-name` and
for `--ou-id`, and also for an account that someone moves into the OU after
setup. An adopted account keeps each valid playplace owner, expires and
budget tag. It gets the defaults for the rest, and playplace closes it when
its lifetime ends. Do not move an account into the OU unless playplace may
close it. To bring in an account on purpose, set its `playplace:owner` tag
to the email of the owner before you move it. After the next refresh, look
at it with `playplace show ACCOUNT`. If the default lifetime or budget does
not fit, use `playplace extend` or `playplace budget`.

Pending requests are tags on the Playground OU itself, one
`playplace:req:NAME` tag for each request. AWS allows 50 tags on a resource,
so the queue holds at most 50 requests, less the other tags on the OU. The
count includes approved requests whose account is not placed yet. Playplace
refuses a new request when the queue is full. Do not let other tools manage
the tags of the OU, for example infrastructure as code that sets the full
tag list. Such a tool deletes the pending requests when it applies its list.

To use an existing nested OU:

1. Find the ID of the OU in the AWS Organizations console, or with
   `aws organizations list-organizational-units-for-parent --parent-id PARENT_ID`.
2. Make sure that the OU holds only accounts that you want playplace to
   manage. On the first refresh, playplace takes over every active account
   in the OU that does not have the `playplace:managed=true` tag. The
   account keeps each valid playplace owner, expires and budget tag, and
   gets the defaults for the rest. playplace closes the account when its
   lifetime ends. It starts the closure at once if the account has expired
   or has a `playplace:close-requested` tag. Move every other account out of
   the OU before you set `--ou-id`. A valid ID of the wrong OU passes every
   check, for example the ID of the parent OU.
3. Set `--ou-id` (`PLAYPLACE_OU_ID`) to the ID. Do not set `--ou-name`.
4. Run `playplace init` before any other command. Most commands start with a
   refresh, and the refresh adopts accounts. `init` reads the OU with
   `organizations:DescribeOrganizationalUnit`, which the runtime policy
   allows. It checks that the OU is in the organization of the AWS profile
   and prints the name and the ID of the OU. Then it lists the active
   accounts in the OU and the new accounts of playplace that wait in the
   organization root, with what the next refresh does to each. Check the
   name of the OU and the list. If the list names an account that playplace
   must not manage, move that account out of the OU. If it names accounts
   that you quarantined, the ID is wrong. Correct `--ou-id` before you run
   another command. The refresh moves each waiting account into the OU, and
   playplace never moves it to another OU later. `init` creates nothing, so
   it does not need the bootstrap policy.
5. Make sure that the safeguards SCP applies to the OU before you grant
   access. See
   [Deploy the purchase and trail SCP](#deploy-the-purchase-and-trail-scp-before-granting-access).

An SCP on the root or on a parent OU also applies to every OU and account
below it. A nested Playground OU thus gets the SCPs of every OU above it, in
addition to its own. A deny in any of them applies to playground accounts.
An allow must be present at every level: the root, each parent OU, the
Playground OU, and the account itself must each allow an action. By
default, AWS attaches the `FullAWSAccess` policy, which allows every action,
to the root, to every OU, and to every account, also to new ones. Check the
SCPs of the parent OUs before you put playgrounds below them.

New accounts still wait in the organization root until playplace places
them. Placement moves an account from the root straight into the nested OU.
Playplace never moves an account out of a parent OU.

## IAM

`aws/playplace-policy.json` is the least privilege the tool needs: read and
lifecycle actions on Organizations, budgets named `playplace-*`, one Cost
Explorer read, and the Identity Center assignment calls. Identity Center
provisions its own role into member accounts through its service role, so
the tool needs no IAM actions for that. `HistoryLogGroupOptional` is needed
only with `--history cloudwatch`. With the default file sink, drop it.

The runtime policy cannot create an organization or an OU. Only `playplace
init` creates them, and every other command fails when they are missing.
`aws/playplace-bootstrap-policy.json` allows `CreateOrganization`,
`CreateOrganizationalUnit`, and the Organizations service-linked role that
`CreateOrganization` needs. Attach it next to the runtime policy only while
you run `init`, then detach it. Do not attach it to the role that runs
`serve` or `reconcile`. If the organization and the Playground OU already
exist, `init` does not need it. It only reports them and lists the accounts
in the OU and the root that the next refresh acts on. With `--ou-id`, `init`
never creates anything and never needs it. If you apply the runtime policy
in a new organization, run `init` with the bootstrap policy first.

New accounts wait in the organization root until playplace places them, so
playplace lists the root with `organizations:ListAccountsForParent`. If you
scope that action to the Playground OU, every reconcile pass fails with
`list root accounts`.

Optional hardening: playplace moves an account only from the root into the
Playground OU, and it refuses every other move. IAM can enforce the same
rule. Remove `organizations:MoveAccount` from the `OrganizationsLifecycle`
statement and add this statement with your IDs:

```json
{
  "Sid": "MoveOnlyFromRootToPlayground",
  "Effect": "Allow",
  "Action": "organizations:MoveAccount",
  "Resource": [
    "arn:aws:organizations::MANAGEMENT_ACCOUNT_ID:root/o-ORG_ID/r-ROOT_ID",
    "arn:aws:organizations::MANAGEMENT_ACCOUNT_ID:ou/o-ORG_ID/ou-PLAYGROUND_OU_ID",
    "arn:aws:organizations::MANAGEMENT_ACCOUNT_ID:account/o-ORG_ID/*"
  ]
}
```

The statement allows a move only when the source and the destination are in
its list. Use the ID of the Playground OU, also when the OU is nested. An OU
ARN names only the organization and the OU, not the parent OUs, so the
statement is the same at any depth. Do not add the parent OUs. Test it with
the IAM policy simulator before you rely on it. A move from the root into
the Playground OU must be allowed. A move from a quarantine OU into the
Playground OU must be denied. For a nested OU, a move from its parent OU
into the Playground OU must also be denied.

## Billing and closure safeguards

### Deploy the purchase and trail SCP before granting access

`aws/playground-safeguards-scp.json` is a **service control policy**, not the
runtime IAM policy above. Deploy it separately through your organization
administrator/infrastructure workflow. `playplace init` does **not** install it,
and playplace does not validate its attachment at runtime. No extra runtime IAM
permissions or member-account role assumption are needed.

The SCP denies:

- Savings Plan creation; EC2, RDS, Redshift, ElastiCache, MemoryDB, OpenSearch,
  and DynamoDB reservation purchases, plus listed reservation exchanges.
- EC2 host reservations, scheduled-instance purchases and capacity blocks
  (including extensions), MediaConnect/MediaLive offerings, and SageMaker
  training plans.
- New Marketplace subscriptions and the listed agreement creation/acceptance
  actions. This also affects services, such as some model offerings, that
  require Marketplace subscriptions.
- `cloudtrail:CreateTrail` by member-account principals, and
  `organizations:LeaveOrganization`.

It deliberately does **not** deny subscription cancellation, reservation
inspection, or authorized cleanup of pre-existing account trails. It is an
explicit deny-list, not proof that all current or future AWS services are free
of financial commitments. Restrict allowed services and review new purchase
APIs as your playground expands. Existing purchases are not canceled by an SCP.

From the repository root, using an **organization-administrator profile**, not
playplace's runtime role:

```sh
# Optional static validation; review all findings before proceeding.
aws accessanalyzer validate-policy \
  --policy-type SERVICE_CONTROL_POLICY \
  --policy-document file://deploy/aws/playground-safeguards-scp.json

# Review the target carefully. Start with a dedicated test OU/account.
export PLAYGROUND_OU_ID=ou-xxxx-yyyyyyyy
POLICY_ID=$(aws organizations create-policy \
  --name PlayplaceBillingSafeguards \
  --description 'Block playground commitments, Marketplace purchases, and account trails' \
  --type SERVICE_CONTROL_POLICY \
  --content file://deploy/aws/playground-safeguards-scp.json \
  --query 'Policy.PolicySummary.Id' --output text)
aws organizations attach-policy --policy-id "$POLICY_ID" --target-id "$PLAYGROUND_OU_ID"
aws organizations list-policies-for-target \
  --target-id "$PLAYGROUND_OU_ID" --filter SERVICE_CONTROL_POLICY
```

A nested Playground OU also gets the SCPs of its parent OUs, so you can
attach the SCP to a parent OU instead. An SCP on a parent OU also denies
these purchases in every other OU and account below that parent. Attach it
there only when that is what you want. See
[The Playground OU](#the-playground-ou).

The organization must use all features with SCPs enabled at its root. Keep the
necessary allow SCPs (normally `FullAWSAccess`) attached: this deny-only policy
does not grant permissions. Check the inherited policies and available policy
attachment quota too. For upgrades, use `update-policy` with the existing ID
rather than creating duplicates. Rollback is `detach-policy` for that ID and
OU; do not delete or detach unrelated policies. Rollback re-enables purchases.

Test in an isolated OU before broad rollout. Verify denied actions using
supported dry-run APIs or controlled test identities; **do not make real paid
purchases just to test the policy**. Verify ordinary on-demand operations and
central CloudTrail delivery still work. LocalStack and the repository's offline
policy tests are not evidence that an SCP is enforced in production.

SCPs restrict member-account principals, including their root users; they do
not restrict management-account principals or service-linked roles. The OU
policy only applies after placement. Playplace places an account before granting
owner access, but separately managed root access, bootstrap roles, or other
provisioning paths must not hand out access before guardrails apply. Keep the
management account and any delegated audit administrator out of the Playground
OU, and keep organization-admin credentials away from playground owners.

### Preserve central CloudTrail auditing

Use an organization trail managed centrally, with logs delivered to a dedicated
log-archive account. Do **not** delete or stop that trail to save money at expiry.
The member `CreateTrail` deny does not stop an existing organization trail;
organization trails are centrally managed and CloudTrail's service-linked role
is not restricted by SCPs. Verify delivery after attaching the policy.

Before adopting existing accounts, an authorized operator should inventory trails
in **every enabled Region**, including shadow trails, for example:

```sh
# Run with your separately authorized member-account inspection credentials.
aws cloudtrail describe-trails --include-shadow-trails --region us-east-1
# Repeat for all enabled Regions; inspect each trail in its HomeRegion.
```

Review `IsOrganizationTrail`, `HomeRegion`, destinations, event selectors,
Insights, and duplicate delivery. Preserve organization trails. Review retention
requirements before deciding whether any **account-owned** trail should be
removed by an authorized operator; playplace never deletes trails or logs.
The SCP does not remediate existing trails. Closed-account trails can continue
delivering to a bucket in another account, including hourly digest files when
log validation is enabled. Budget for central S3/KMS/CloudWatch usage, use
approved retention/lifecycle settings, and inspect duplicate event charges.
If an account is already closed and an account-owned trail needs deletion,
contact AWS Support rather than reopening it casually (reopening restarts
charges for remaining services).

### Review existing financial obligations

Before onboarding existing accounts, check Bills and Marketplace subscriptions
and inventory reservations/Savings Plans (including queued purchases) across
services and Regions. Explicitly cancel subscriptions according to the product's
terms; the SCP leaves cancellation APIs available but does not grant access to
them. Existing contractual charges can continue through the commitment term;
resource deletion or account closure does not cancel them. Keep an operator-owned
record of obligations and continue reviewing consolidated billing after closure.

### Monitor closure, not just the request

`CloseAccount` is asynchronous. Playplace keeps an account `closing` until AWS
Organizations reports `State=CLOSED`; `PENDING_CLOSURE` is not success and can
still permit use and charges. `SUSPENDED` is not proof of voluntary closure and
is shown as `unavailable`, along with unknown or pending-activation states.

```sh
# While serve runs, its worker already checks closures every minute.
playplace --no-refresh list --all
playplace --no-refresh show ACCOUNT_ID
# Only when no worker runs, as the single writer:
playplace reconcile --close-alert-after 24h
# Independent confirmation using management-account credentials:
aws organizations describe-account --account-id ACCOUNT_ID --query Account.State
```

After the configured positive duration from the close request, an unconfirmed
closure causes a nonzero reconciliation exit, error logs, a `closure-overdue`
history event, and the *overdue* notice, sent once (configure `--slack-webhook`
to reach operators). The clock starts when playplace first observes an externally
initiated `PENDING_CLOSURE`. Alerts occur on refresh, not via an AWS TTL timer.
The `--alert-email` setting is for **Budgets**, not closure notices.

AWS limits closures. Within a rolling 30 days, an organization can close 250
member accounts or 20% of them, whichever is higher, up to 1,000. Only three
closures can run at the same time. When AWS refuses a close for one of these
limits, playplace keeps the close intent and retries it on a later refresh.

A closed account counts toward the account limit of the organization until
AWS closes it permanently, 90 days after the closure. The default limit is
10 accounts. You can ask for a higher limit in the Service Quotas console.
See [Quotas for AWS Organizations](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_reference_limits.html).

Playplace retries refusals while the provider still reports `ACTIVE`, monitors
pending requests without resubmitting them, and records `closed` only after
observing `CLOSED`. Closure markers in account tags prevent repeated notices
across restarts. Tag-write errors are surfaced for retry; history and notification
delivery are best-effort, not an exactly-once alerting system. Alert on worker
errors, missed schedules, and failed reconciliation independently. Do not silence
them because a close API call previously returned success.

Inspect closure quotas, IAM failures, Control Tower
requirements, and unexpected suspension when closure stalls. Restrict remaining
access through your infrastructure incident procedure if required; playplace
only revokes its configured Identity Center assignment, not every credential or
session. These safeguards neither guarantee immediate shutdown nor zero future
bills. There is no `aws-nuke` integration or resource-deletion step.

### Move an account out of the Playground OU

Move an account out of the Playground OU to contain it, for example into a
quarantine OU with a deny-all SCP. This ends playplace management of the
account:

- Playplace does not move it back into the OU.
- It no longer shows in the web UI, the TUI, or `list`.
- Playplace does not close it at expiry and does not revoke owner access.
  Remove the Identity Center assignment through your incident procedure.
- The AWS budget and its alerts stay.
- Its name and its place in the owner's per-owner limit are free again. The
  owner can request another account at once. A new account that reuses the
  root email of the moved account fails at AWS, because AWS never frees the
  root email of an account. By default, `--email-pattern` makes the root
  email from the name, so the same name with the same pattern fails. With
  another root email, AWS creates a second account with the same name.

If you move the account back into the Playground OU, management resumes with
its existing tags. If its expiry passed in the meantime, the next refresh
closes it.

A new account waits in the organization root until playplace places it.
While it waits, it is `creating`. After one hour the row also shows a note
that it has no budget, OU guardrail, or expiry yet. If placement fails, every
refresh fails until placement succeeds. Fix the cause, for example a missing
`organizations:MoveAccount` permission. To finish the placement by hand, move
the account from the root into the Playground OU. The next refresh then
continues with budget protection and owner access. If the approved expiry
passed while the account waited in the root, the pass that places it also
closes it. Playplace cannot extend an account that is still `creating`. If
the owner still needs the account, set a later `playplace:expires` tag on it
in AWS (RFC 3339, for example `2026-11-01T00:00:00Z`) before you fix the
placement.

To stop playplace from retrying a stuck creation, remove the
`playplace:handoff` tag from the account, or move the account to an OU other
than the Playground OU. Playplace then leaves it alone, like any account
outside the OU. Playplace never closes an account in the root.

### Official AWS references

- [Account closure and billing exceptions](https://docs.aws.amazon.com/accounts/latest/reference/manage-acct-closing.html)
- [Bills after account closure](https://repost.aws/knowledge-center/closed-account-bill)
- [CloudTrail after account closure](https://docs.aws.amazon.com/awscloudtrail/latest/userguide/cloudtrail-account-closure.html)
- [Asynchronous CloseAccount API](https://docs.aws.amazon.com/organizations/latest/APIReference/API_CloseAccount.html)
- [Account State versus retired Status](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_accounts_account_state.html)
- [SCP effects and limitations](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps.html)
- [Service authorization reference](https://docs.aws.amazon.com/service-authorization/latest/reference/reference_policies_actions-resources-contextkeys.html)
- [AWS policy-generator action catalog](https://awspolicygen.s3.amazonaws.com/js/policies.js) (action names checked 2026-09-18)

## Additional guardrails outside the tool

From your infrastructure code, also consider denying IAM users and access keys,
allow-listing regions and services, and capping instance types.

For per-account budget-triggered restrictions, separately deploy the policy and
role in [Budget protection](../docs/budgets.md), then configure
`--budget-scp-id`, `--budget-action-role`, and `--alert-email`. Unlike the
purchase/trail SCP, **do not attach the budget SCP to the Playground OU or to
any OU above it**: AWS Budgets manages its account attachments. The admin
budget-change operation reverses and resets its action after a sufficient
increase. After confirmed closure, reconciliation retires the owned action,
retaining the budget and alerts. Update the runtime budget-actions policy to
include its ownership-tag-constrained `budgets:DeleteBudgetAction` permission,
and keep the original policy/role configuration until health reads `retired`.
Failed/locked deletions retry; they never trigger direct SCP detachment. See
the retirement runbook and remaining live-validation requirements in [Budget
protection](../docs/budgets.md). Billing data and execution are delayed;
neither a budget nor an account TTL is a hard spending cap.
