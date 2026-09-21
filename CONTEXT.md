# Playground accounts

Short-lived AWS playground accounts with approved ownership, expiry, budget protection, and optional managed owner access.

## Language

**Account readiness**:
An unexpired account with no closure underway has observed budget protection, no active budget restriction, and, when owner access is managed, a successful owner access grant. Readiness does not mean spending is capped.
_Avoid_: Created, placed, active (as synonyms for ready)

**Initial handoff**:
The account's first readiness milestone, whether created by playplace or newly adopted, celebrated by the account-ready notification once its owner is identified. Later recovery of protection or access is not another initial handoff.
_Avoid_: Recovery (as a synonym for initial handoff)

**Identified owner**:
An owner resolved for managed access or, when access management is disabled, a nonblank free-text owner other than the placeholders `unknown` and `?`. An email address is not required when access management is disabled.

**Budget recovery**:
Removal of an account's budget restriction after an approved increase exceeds its known pre-change spend, followed by rearming protection for the approved monthly limit. Recovery is not account reopening, action retirement, or another initial handoff.
_Avoid_: Budget reset (as a synonym for the whole recovery)

**Recovery approval**:
Permission to reverse a specific budget action following an approved limit increase, bound to that action, limit, pre-change spend, and monthly budget period. It does not authorize reversal of a new execution after protection has been rearmed.
_Avoid_: Extra credit, blanket unlock
