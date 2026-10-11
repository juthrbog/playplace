# Playground accounts

Short-lived AWS playground accounts with approved ownership, expiry, budget protection, and optional managed owner access.

## Language

**Lifecycle journey**:
A distinct playground request's progression and any resulting account lifecycle, or an account lifecycle beginning with adoption or direct creation when no request exists. Reusing a name does not join separate journeys.
_Avoid_: Account name (as journey identity)

**Request journey**:
A lifecycle journey beginning with one playground request submission, including its edits and decisions and any account it produces. Resubmission after denial, withdrawal, or request expiry starts a separate journey, even when the name is reused.

**Request version**:
A pending request's journey together with the values an actor saw: owner, lifetime, budget, purpose, limit override, and who asked and when. An approval, denial, withdrawal, or edit applies only to the version the actor saw. After an edit or a resubmission, the actor must look at the request again.
_Avoid_: Request name (as the target of a decision)

**Lifecycle history**:
The shared operational record of lifecycle journeys, including requests that never produce an account. It makes known gaps explicit; it is not an authoritative audit trail or proof of current account state.
_Avoid_: Audit trail (as a synonym), account history (when excluding requests)

**Playplace ownership**:
An account's enrollment under playplace management, independent of whether its stored lifetime, approved budget, or close intent is usable. This is distinct from the human owner responsible for the account. Ownership applies in the Playground OU, and to a new account that waits in the organization root for placement. On an account that someone moved out of the OU, the `playplace:managed` tag has no effect.
_Avoid_: Valid account (as a synonym for owned)

**Adoption**:
Enrollment of a previously unowned playground account, using defaults for unspecified lifetime and budget. Restoring damaged facts on an already-owned account is repair, not adoption.

**Guardrail facts**:
The approved expiry, approved monthly budget, and any close intent that determine which account operations are justified. A damaged fact is unknown; independently valid facts remain usable for closure and retirement.
_Avoid_: Defaults (as a substitute for lost approved facts)

**Placement**:
The one move of a new account from the organization root into the Playground OU. It happens only while the account's handoff tag says `placing`. An account that leaves the OU after placement is no longer a playground, and playplace never moves it back. An account that leaves the root before placement is never placed.
_Avoid_: Adoption (an account that is already in the OU), re-placement

**Account readiness**:
An unexpired account with no closure underway has observed budget protection, no active budget restriction, and, when owner access is managed, a successful owner access grant. Readiness does not mean spending is capped.
_Avoid_: Created, placed, active (as synonyms for ready)

**Settling protection**:
Budget protection that a later reconciliation pass completes: `setup-pending`, `restricting`, `recovering`, or `retiring`. New access and the initial handoff wait for it. It is progress, not failure, until it lasts longer than two hours. A recovery step that AWS reports as failed is not settling: playplace sends it again, and the pass fails.
_Avoid_: Error, failure (as synonyms for settling protection)

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
