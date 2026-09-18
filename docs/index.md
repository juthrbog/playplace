---
hide:
  - toc
---

<img class="pp-banner" src="assets/banner.png" alt="playplace: short-lived AWS playground accounts, with guardrails">

# Welcome

playplace gives engineers their own short-lived AWS account to experiment in,
with the guardrails handled for them: every account has an owner, an expiry,
and a monthly budget, and every request is approved by a person before AWS
creates anything. The name comes from the ball pits and tube slides of the
indoor playgrounds we grew up with; this is that idea moved to the cloud.

<div class="pp-cards" markdown>

- **[I want an account](requesting.md)**
  Sign in, fill in four fields, and wait for an approver. Extend or close it
  from the same page.

- **[I review requests](approving.md)**
  The queue, the limits, and what Edit, Deny, and Withdraw do. Slack buttons
  act on the same queue.

- **[I run playplace](deploy.md)**
  One process in the management account, an IAM policy, and optional
  Slack, GitLab, and history sinks.

</div>

## How it works

1. **Request.** An engineer asks for an account with a name, a lifetime in
   days, a monthly budget, and a sentence on what it is for. Requests come
   from the web UI, the CLI, or a GitLab pipeline.
2. **Approve.** An approver looks at the request and approves, denies, or,
   for admins, adjusts it first, in the web UI, in Slack, or on the command
   line. Nobody can approve a request they made or would own.
3. **Use.** AWS creates the account in one to three minutes. The owner finds
   it in their AWS access portal with the playground permission set, and a
   budget alert is set up for it.
4. **Expire.** Three days before expiry the owner is warned. They can extend,
   within the lifetime limit. At expiry the account is closed. Owners can
   close early at any time.

## Limits

| limit | default |
|---|---|
| Lifetime | 14 days, up to 90 |
| Monthly budget | $50, up to $500 |
| Accounts per person | 1, counting requests waiting for approval |
| Time a request waits for approval | 7 days, then it is dropped |

Admins can go past the lifetime and budget ceilings when a case calls for it.

## Where to go next

- [Requesting an account](requesting.md) if you are an engineer.
- [Approving requests](approving.md) if you review them.
- [Command line](cli.md) and [Configuration](configuration.md) if you run
  playplace for your organization.
- [Deploying](deploy.md) to set it up in your AWS organization.
