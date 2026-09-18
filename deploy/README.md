# Deploying playplace

playplace must run with management-account credentials, because AWS allows
`CreateAccount` and `CloseAccount` only from there. Two things run:

- `playplace serve`: the web UI, Slack endpoint, and a refresh every minute.
  One small host in the management account (ECS, App Runner, or a VM) behind
  HTTPS, with an instance or task role carrying `aws/playplace-policy.json`.
- a daily `playplace reconcile` for the days nobody opens the UI. The GitLab
  schedule in `gitlab/.gitlab-ci.yml` does this, or use cron next to `serve`.

## IAM

`aws/playplace-policy.json` is the least privilege the tool needs: read and
lifecycle actions on Organizations, budgets named `playplace-*`, one Cost
Explorer read, and the Identity Center assignment calls. Identity Center
provisions its own role into member accounts through its service role, so
the tool needs no IAM actions for that. The two `OrganizationsBootstrap*`
statements are only for `playplace init` on a fresh organization:
`CreateOrganization` also has to create the Organizations service-linked
role. Remove both afterwards. `HistoryLogGroupOptional` is only needed
with `--history cloudwatch`; with the default file sink, drop it.

For GitLab, create an IAM OIDC identity provider for `gitlab.com` (or your
self-managed host) and a role with `aws/gitlab-oidc-trust.json` as trust
policy, after filling in the account id, the project path, and the branch
the pipeline runs from (the file says `main`; change it if yours differs).

## GitLab flow

`gitlab/.gitlab-ci.yml` gives three manual jobs run from "Run pipeline" with
prefilled fields, and one scheduled job:

| job | who | what |
|---|---|---|
| request | anyone with pipeline rights | `playplace request` in the runner's name |
| approve | members allowed on the `approvals` protected environment | `playplace approve` |
| deny | same | `playplace deny` |
| reconcile | schedule | `playplace reconcile` |

The runner's GitLab email is exported as `PLAYPLACE_OPERATOR`, so the
`requested-by` and `approved-by` tags carry a real person. Because the queue
lives in AWS, a request made from GitLab can be approved in the web UI or in
Slack, and one made in the web UI can be approved from the `approve` job.

`gitlab/issue_template.md` is an optional issue template for teams that want
the request to start as a conversation.

## Guardrails outside the tool

Attach service control policies to the Playground OU from your infrastructure
code. The common set: deny leaving the organization, deny IAM users and
access keys, allow-list regions, cap instance types, deny Marketplace. A
Budgets Action that attaches a deny policy at 100 percent of budget closes the
gap left by Cost Explorer's one-day lag.
