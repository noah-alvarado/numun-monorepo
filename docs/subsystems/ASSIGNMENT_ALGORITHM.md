# ASSIGNMENT_ALGORITHM.md

This document specifies the **delegate-to-position assignment algorithm** for NUMUN. It builds on [DATA_MODEL.md](../DATA_MODEL.md) (entities, key design) and [APPLICATION.md](../APPLICATION.md) (Go Lambdalith).

The algorithm runs **after** Secretariat approves a delegation's final delegate list, **before** the conference begins. It proposes a placement of every delegate into a position; Secretariat then edits and approves.

Several schema additions are required to support this algorithm. Those are listed inline where they appear and consolidated in §10 as proposed amendments to DATA_MODEL.md.

---

## 1. Goals & non-goals

### Goals
- Produce a high-quality, deterministic proposal that places **every delegate into exactly one position**.
- Respect all hard constraints (capacity, clustering caps, dual-delegation rules).
- Optimize a layered soft objective: **fairness → spreading → type preference → size preference → committee balance**, with single-seat positions filled in preference to double-seat positions.
- Be **explainable** — every proposed assignment carries a score and a human-readable reason.
- Be **fast** — finish within an API Gateway HTTP API request (29s ceiling, 20s internal soft timeout).
- Be **reproducible** — same inputs + same seed → identical output.
- Preserve **staff-approved** assignments across re-runs.

### Non-goals
- Provably optimal solutions. A well-tuned heuristic suffices; staff edits the proposal anyway.
- Multi-stage scheduling (e.g., session-by-session committee assignments). Each delegate gets one position for the whole conference.
- Per-delegate preferences. Per PROJECT.md, preferences are delegation-level only.

---

## 2. Inputs

The algorithm reads, for one Conference:

| Source | Shape |
|---|---|
| Delegations | id, status (only `approved` are included), `committeePreferences` (trinary, see §10.1), school identity, delegate count |
| Delegates | id, delegationId, firstName, lastName, **`experienceLevel`** (see §10.2) |
| Committees | id, type (crisis / non-crisis), size (small / medium / large) |
| Positions | id, committeeId, `maxDelegates`, **`dualDelegation`** (see §10.3), **`prestigeTier`** (see §10.4) |
| Existing Assignments | items with `status = approved` are **pinned**. Items with `status = proposed` are discarded at the start of a re-run. |

Inputs not used by the algorithm:
- `financiallyQualifying` count — informational only, per PROJECT.md confirmation.
- Payment status, addresses, advisor identities, awards — irrelevant to assignment.

---

## 3. Hard constraints

The algorithm must produce an output that satisfies all of these. A failure to satisfy any of them is a bug, surfaced as an error to staff with a diagnostic.

| # | Constraint |
|---|---|
| H1 | Every non-pinned delegate is assigned exactly one position. |
| H2 | No position exceeds its `maxDelegates`. |
| H3 | No more than **2 delegates from the same school** in the same committee. |
| H4 | If a position has `dualDelegation = true`, both its seats are filled by delegates from the **same school**. |
| H5 | Positions with `prestigeTier = reserved` are **not** filled by the algorithm. They must be manually assigned. |
| H6 | Pinned (approved) assignments are not modified. The capacity they consume is removed from the available pool before the algorithm runs. |

If the input is **infeasible** (e.g., 900 delegates for 800 non-reserved seats, or H3 cannot be satisfied because too many delegates from one school), the algorithm fails fast with a diagnostic identifying the conflict. No partial output is committed.

---

## 4. Soft objective

The algorithm picks among feasible assignments by maximizing a weighted score. The objective is built from per-assignment terms and global terms.

### 4.1 Trinary preference scoring

For each delegate at each candidate position, score the match on **type** and **size** axes independently. Negative preferences cost more than positive preferences reward — i.e., violating an "avoid this" preference is worse than missing a "want this" preference.

```
typeMatch(delegationPref, committee.type):
  if delegationPref.type[committee.type] == "positive"  →  +1.0
  if delegationPref.type[committee.type] == "negative"  →  -2.0
  if delegationPref.type[committee.type] == "neutral"   →   0.0

sizeMatch(delegationPref, committee.size):
  same scoring against delegationPref.size[committee.size]
```

This asymmetry encodes the example in the interview: a delegation neutral on crisis and negative on small/large should be placed in crisis over small/large, even though "type" outranks "size" in the global hierarchy.

### 4.2 Per-assignment score

```
s(d, p) = W_TYPE * typeMatch(d.delegation.pref, p.committee.type)
        + W_SIZE * sizeMatch(d.delegation.pref, p.committee.size)
        + W_EXPERIENCE * experienceMatch(d, p)
        + W_SINGLE_SEAT * singleSeatBonus(p)
```

- `W_TYPE = 1.0`, `W_SIZE = 0.8` — type slightly outranks size, but combined with the 2× negative penalty in §4.1, this still lets size-negatives dominate type-neutrals (as required).
- `experienceMatch`: `+0.5` if delegate's `experienceLevel` aligns with the position's `prestigeTier` and committee type — specifically, `advanced` delegates on `elevated` positions and on crisis committees; `novice` delegates on non-crisis. Otherwise 0. `W_EXPERIENCE = 0.5`.
- `singleSeatBonus`: `+0.2` if `p.maxDelegates == 1`, else 0. This biases toward filling single-seat positions before double-seat ones (priority 5 in the interview).

### 4.3 Global objective

```
O = Σ over all proposed assignments of s(d, p)
    + W_FAIRNESS * fairnessTerm
    - W_SPREAD   * spreadPenalty
    - W_BALANCE  * balancePenalty
```

- `fairnessTerm` (priority 1): the **minimum** per-delegation average match score across all delegations. This is a leximin-style fairness measure — improving the worst-off delegation is worth more than improving an already-happy one. `W_FAIRNESS = 10.0`.
- `spreadPenalty` (priority 2): sum over `(delegation, committee)` pairs of `max(0, count_in_committee - 1) ^ 2`. Quadratic penalty so the second co-delegate in a committee costs more than discouraging just one. `W_SPREAD = 2.0`.
- `balancePenalty` (priority 5): variance of fill rates across committees. `W_BALANCE = 0.1` — least concern, per interview.

Weights are tuned to produce the priority hierarchy stated in the interview:
**fairness ≫ spread > type > size > balance**, with single-seat preference layered in.

### 4.4 Weights live in code, not config

All weights are constants in the `domain/assignment` package. Tuning happens in code review, not config. If staff wants to influence the objective, they edit the proposal — not the weights.

---

## 5. Algorithm

A **deterministic random-restart greedy + 2-opt local search**. Pure Go, no external solver, ships in the Lambdalith.

### 5.1 Setup

1. Load all inputs for the conference (§2).
2. Compute the **pinned set** = all approved assignments.
3. Compute the **available pool** = positions minus pinned occupants; delegates minus pinned delegates.
4. Filter out positions with `prestigeTier = reserved` from the available pool (H5).
5. Initialize the RNG with the run's `seed` (see §6).
6. **Feasibility precheck**:
   - `totalAvailableSeats >= unpinnedDelegateCount`?
   - For each school, `delegateCount <= 2 * committeeCount` (necessary condition for H3)?
   - For each dual-delegation position, are at least 2 delegates available from some school that can fit (H4)?
   - If any precheck fails, return error with diagnostic.

### 5.2 Phase A — Reserved & dual-delegation seeding

Dual-delegation positions are filled first because their constraint (H4: both seats same school) is the most restrictive.

1. For each `dualDelegation = true` position, find candidate **schools** that have ≥ 2 unassigned delegates and whose delegates' preferences best match this committee.
2. For each such position, pick the school with the highest combined per-delegate score (§4.2) on this position, choosing 2 delegates from that school deterministically (sorted by delegateId).
3. If no school has ≥ 2 available delegates whose placement here satisfies H3 at the committee level, leave this position empty for now and revisit during local search (it may turn out to be infeasible — handled by the validator).

### 5.3 Phase B — Greedy main pass

1. Shuffle unassigned delegates deterministically using the seeded RNG.
2. For each delegate `d` in shuffled order:
   a. Compute the candidate position set: all available positions that respect H2, H3 (would not exceed cluster cap), and H4 (not a dual-delegation position with the other seat held by a different school).
   b. Compute `s(d, p)` for each candidate.
   c. Compute a **marginal** adjustment for `fairnessTerm` and `spreadPenalty` — i.e., how much each candidate would change those global terms if `d` were placed there. Add into the per-candidate score.
   d. Pick the highest-scored candidate. Tiebreak by lowest current committee fill ratio.
   e. If no feasible candidate exists, push `d` onto a deferred queue.
3. After the main pass, attempt to place deferred delegates by **forcing rebalances** — try to swap an already-placed delegate to free up a spot that satisfies `d`'s constraints. Standard backtracking, bounded by 1,000 attempts. If still infeasible, fail with diagnostic.

### 5.4 Phase C — Local search (2-opt improvement)

1. Maintain a working solution from Phase A + B.
2. Compute the current `O` (§4.3).
3. Repeat until no improving swap is found, or time budget reached, or 10,000 iterations:
   a. Pick a random pair of non-pinned, non-dual-delegation assignments `(d1 @ p1, d2 @ p2)`.
   b. Test the swap: does `(d1 @ p2, d2 @ p1)` satisfy all hard constraints (H3 cluster cap on both committees)?
   c. Compute `O_new - O_old`. If positive, apply the swap.
4. Phase C also runs **chained 3-swap moves** opportunistically: if a 2-swap doesn't improve but a 3-cycle (d1→p2, d2→p3, d3→p1) does, apply that.

Time budget: **15 seconds** soft cap inside Phase C. Phases A+B are bounded by O(delegates × positions) ≈ 850 × 600 = 510k operations — sub-second in Go.

### 5.5 Phase D — Validate & emit

1. Run all hard constraints (H1–H6) against the final assignment. Any violation = bug, fail with diagnostic.
2. For each proposed assignment, compute and attach:
   - `score` — the final `s(d, p)` value.
   - `reason` — a short human-readable string, e.g., `"matched delegation's positive non-crisis preference and positive small-committee preference"` or `"violated delegation's negative small-committee preference (no feasible large alternative)"`.
3. Return the proposed assignment set.

### 5.6 Pseudocode skeleton

```go
func Propose(ctx context.Context, in Inputs, opts RunOptions) (*Proposal, error) {
    rng := newRNG(opts.Seed)
    state := newWorkingState(in)

    if err := precheckFeasibility(state); err != nil {
        return nil, err
    }

    if err := seedDualDelegations(state, rng); err != nil {
        return nil, err
    }

    if err := greedyMainPass(state, rng); err != nil {
        return nil, err
    }

    deadline := time.Now().Add(opts.LocalSearchBudget) // 15s default
    localSearch2Opt(ctx, state, rng, deadline)

    if err := validate(state); err != nil {
        return nil, fmt.Errorf("post-condition violated: %w", err)
    }

    return buildProposal(state), nil
}
```

---

## 6. Determinism & seeding

- Every run has a `seed` (a 64-bit unsigned integer). Same inputs + same seed → bit-identical output.
- The **canonical seed** for a conference is `hash(conferenceId)` — stable forever, stored on a `AssignmentRun` record (new entity, §10.5).
- The **canonical run** is the first run on the canonical seed; its output is what staff sees by default.
- **Shuffle mode** (per interview Q10): staff clicks "Re-shuffle proposal" → a new run with `seed = hash(conferenceId, runOrdinal++)`. Produces a different deterministic proposal. The canonical seed is **never overwritten**.
- All RNG calls inside the algorithm flow from one `*rand.Rand` instance initialized with the run's seed. No use of `math/rand`'s default source. No use of `time.Now()` or `runtime` data as inputs to decisions.

---

## 7. Re-run semantics

When staff triggers an assignment run for a conference that already has assignments:

1. **Approved assignments are pinned.** They are loaded into the pinned set (§5.1) and never modified.
2. **Proposed (non-approved) assignments from prior runs are discarded.** They do not influence the new run.
3. **Approval revocation.** Per interview Q13, staff can unmark an approval via a separate endpoint. This flips the assignment's `status` from `approved` back to `proposed` and removes it from the pinned set on the next run. (DDB: UpdateItem, conditional on current `status = approved` and `version`.)
4. **Manual edits** to proposed assignments are not pinned. If staff wants an edit to survive a re-run, they must approve it.

---

## 8. Dry-run vs. commit

The algorithm has **two distinct endpoints**:

| Endpoint | Effect |
|---|---|
| `POST /v1/conferences/{id}/assignments/propose?dryRun=true` | Runs the algorithm. Returns the proposal as JSON. **Writes nothing.** |
| `POST /v1/conferences/{id}/assignments/propose` (no flag) | Runs the algorithm. Writes a new `AssignmentRun` record + Assignment items with `status = proposed`. Discards prior proposed items in a `TransactWriteItems` batch. |

Both endpoints accept a `seed` parameter for shuffle mode (§6).

Approval is a separate operation: `POST /v1/conferences/{id}/assignments/{assignmentId}/approve` — flips one assignment's `status` from `proposed` to `approved` and pins it.

---

## 9. Operational

| Concern | Choice |
|---|---|
| Where it runs | The same Go Lambdalith as everything else. Single Lambda, single binary. |
| Trigger | Manual only. Staff clicks "Propose Assignments" in the portal. |
| Runtime budget | 20-second soft cap (5s headroom under API Gateway's 29s hard limit). Phase C exits early at 15s. |
| Lambda memory / timeout | Bump Lambda memory to **2048 MB** for the assignment route to give Go's GC headroom; this is purely for this code path. SAM template sets per-route memory via a separate function if it becomes a problem; otherwise Lambdalith default of 2048 MB. |
| Concurrency | Only one in-flight run per conference. Enforced via a DDB conditional write: an `AssignmentRun` item with `status = running` blocks others; on completion it flips to `done` or `failed`. |
| Failure handling | Any internal error returns HTTP 5xx with a structured `diagnostic` field naming the failed phase + constraint. No partial writes (Phase D validates before any persistence). |
| Observability | Each phase logs `slog` records with duration, item counts, and final objective `O`. Sentry captures any panic. |
| Re-runs | Idempotent given seed + inputs. Re-running with the same seed produces an identical proposal. |

If the algorithm ever exceeds 20s in practice, the upgrade path is to split it into its own Lambda + SQS queue + portal polling endpoint. The DDB schema for `AssignmentRun` (§10.5) already supports this — `status` transitions cover the async case.

---

## 10. Proposed amendments to DATA_MODEL.md

These changes are required for the algorithm. They have not yet been applied to DATA_MODEL.md.

### 10.1 Delegation.committeePreferences — trinary

Replace:
```
committeePreferences: { crisis: bool, nonCrisis: bool, sizes: [small|medium|large] }
```
with:
```
committeePreferences: {
  type: {
    crisis:    "positive" | "negative" | "neutral",
    nonCrisis: "positive" | "negative" | "neutral",
  },
  size: {
    small:  "positive" | "negative" | "neutral",
    medium: "positive" | "negative" | "neutral",
    large:  "positive" | "negative" | "neutral",
  },
}
```

### 10.2 Delegate.experienceLevel — new field

```
experienceLevel: "novice" | "intermediate" | "advanced"
```
Required field. Default `"intermediate"` if advisor doesn't specify.

### 10.3 Position.dualDelegation — new field

```
dualDelegation: bool   // default false
```
Application enforces: if `dualDelegation == true`, then `maxDelegates` must equal `2`.

### 10.4 Position.prestigeTier — new field

```
prestigeTier: "standard" | "elevated" | "reserved"   // default "standard"
```

### 10.5 New entity: AssignmentRun

A record of one execution of the algorithm. Captures inputs hash, seed, status, summary stats, and outcome.

| Attribute | Notes |
|---|---|
| `id` | UUIDv7 |
| `conferenceId` | scope |
| `seed` | uint64 |
| `runOrdinal` | sequence within the conference (1, 2, 3, ...) |
| `triggeredBy` | userId |
| `triggeredAt`, `completedAt` | timestamps |
| `status` | `running` \| `done` \| `failed` |
| `objective` | final `O` value if `done` |
| `assignmentCount` | how many assignments produced |
| `diagnostics` | nullable string — populated if `failed` |
| `isCanonical` | bool — true for the first run with the canonical seed |
| `inputsHash` | sha256 of normalized inputs (delegations, delegates, committees, positions, pinned set) — for cache validation and audit |

Key design:
- PK: `CONF#<conferenceId>`
- SK: `ASSIGNMENT_RUN#<triggeredAt>#<id>`
- GSI2PK: `CONF#<conferenceId>#ASSIGNMENT_RUN_STATUS#<status>` — to find the in-flight run quickly.

### 10.6 Assignment additions

Add to existing Assignment entity:
- `score` — float
- `reason` — string
- `runId` — UUIDv7 reference to the `AssignmentRun` that produced this proposal

---

## 11. Decisions log

| Decision | Choice | Reason |
|---|---|---|
| Algorithm style | Greedy + 2-opt local search in pure Go | Small problem size; full control; zero deps |
| Optimality target | Heuristic, no optimality guarantee | Staff edits anyway; "good enough" indistinguishable from optimal at this scale |
| Determinism | Seeded RNG; canonical seed = hash(conferenceId), never overwritten | Trust; reproducibility; re-runs |
| Shuffle mode | Fresh seed per re-shuffle; canonical run preserved | User-requested in interview Q10 |
| Re-run pinning | Approved = pinned; proposed = discarded; manual unmark supported | User-requested in interview Q13 |
| Trinary preferences | positive / negative / neutral per axis | User-requested in interview Q6; negative weighted 2× positive |
| Priority ordering | fairness > spread > type > size > balance, single-seat layered in | User-requested in interview Q7 |
| Same-school cluster cap | 2 per committee (hard) | User-requested in interview Q5 |
| Dual-delegation positions | Same-school both seats (hard) | New constraint per Q1 acknowledgment |
| Reserved positions | Algorithm skips entirely | New constraint per Q1 acknowledgment |
| Dry-run | Separate endpoint, no writes | User-requested in interview Q15 |
| Where it runs | Inside the Lambdalith, sync, 20s soft cap | Sufficient for current scale |
| Explainability | Per-assignment `score` + `reason` attached | User accepted default in Q14 |

---

## 12. Open items

- **Weight tuning.** Initial constants in §4 are best-guesses. Tune against historical NUMUN data once any is available.
- **Backtracking budget in Phase B step 3.** 1,000 attempts is a rule-of-thumb; revisit if real inputs hit the cap.
- **Local-search neighborhood.** v1 uses random pair selection. If quality is unsatisfactory, swap to best-improvement search or simulated annealing.
- **Failure UX.** When the algorithm fails (e.g., over-subscribed conference), the portal needs a UI to surface the diagnostic. Application doc concern, not algorithm.
- **Multi-objective Pareto exploration.** If staff ever wants to see "what would the proposal look like if we cared less about fairness and more about spreading?", that's a future enhancement requiring a re-parameterized run.
- **Audit log of staff edits.** Tracking who unpinned what, who approved what, and when — not in scope here but should be considered in a future audit-log entity.
