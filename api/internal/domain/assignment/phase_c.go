package assignment

import (
	"context"
	"math/rand"
	"time"
)

const phaseCMaxIterations = 10000

// localSearch2Opt is Phase C from §5.4. Random pair-swaps + opportunistic
// 3-cycle moves; honors the deadline and ctx cancellation. Never fails — if
// improvement budget is exhausted it just exits.
func localSearch2Opt(ctx context.Context, s *workingState, rng *rand.Rand, deadline time.Time) {
	if len(s.assignments) < 2 {
		return
	}
	for iter := 0; iter < phaseCMaxIterations; iter++ {
		if ctx != nil && ctx.Err() != nil {
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		// Pick two random non-pinned, non-dual-delegation assignments. A
		// single rng collision (i==j) is just this iteration's miss; only
		// stop when there aren't enough eligible assignments at all.
		i, j, ok := pickSwapPair(s, rng)
		if !ok {
			// Too few eligible assignments — Phase C has nothing to do.
			if eligibleCount(s) < 2 {
				return
			}
			continue
		}
		if tryPairSwap(s, i, j) {
			continue
		}
		k, ok := pickThird(s, rng, i, j)
		if !ok {
			continue
		}
		tryThreeCycle(s, i, j, k)
	}
}

// pickSwapPair picks two distinct candidate indices that are eligible (not
// pinned, not seated on a dual-delegation position).
func pickSwapPair(s *workingState, rng *rand.Rand) (int, int, bool) {
	eligible := eligibleIndices(s)
	if len(eligible) < 2 {
		return 0, 0, false
	}
	a := eligible[rng.Intn(len(eligible))]
	b := eligible[rng.Intn(len(eligible))]
	if a == b {
		return 0, 0, false
	}
	return a, b, true
}

func pickThird(s *workingState, rng *rand.Rand, i, j int) (int, bool) {
	eligible := eligibleIndices(s)
	if len(eligible) < 3 {
		return 0, false
	}
	k := eligible[rng.Intn(len(eligible))]
	if k == i || k == j {
		return 0, false
	}
	return k, true
}

func eligibleCount(s *workingState) int {
	n := 0
	for _, a := range s.assignments {
		if s.pinnedDelegate[a.DelegateID] {
			continue
		}
		if s.positions[a.PositionID].DualDelegation {
			continue
		}
		n++
	}
	return n
}

func eligibleIndices(s *workingState) []int {
	out := make([]int, 0, len(s.assignments))
	for idx, a := range s.assignments {
		if s.pinnedDelegate[a.DelegateID] {
			continue
		}
		p := s.positions[a.PositionID]
		if p.DualDelegation {
			continue
		}
		out = append(out, idx)
	}
	return out
}

// tryPairSwap attempts swap (d1@p1, d2@p2) -> (d1@p2, d2@p1). Reverts on
// constraint failure or non-improvement.
func tryPairSwap(s *workingState, i, j int) bool {
	a1 := s.assignments[i]
	a2 := s.assignments[j]
	if a1.PositionID == a2.PositionID {
		return false
	}
	before := objective(s)

	r1, ok := s.removeAssignment(a1.DelegateID)
	if !ok {
		return false
	}
	r2, ok := s.removeAssignment(a2.DelegateID)
	if !ok {
		s.applyAssignment(r1)
		return false
	}

	new1, ok1 := buildAssignment(s, r1.DelegateID, r2.PositionID)
	if !ok1 {
		s.applyAssignment(r1)
		s.applyAssignment(r2)
		return false
	}
	s.applyAssignment(new1)
	new2, ok2 := buildAssignment(s, r2.DelegateID, r1.PositionID)
	if !ok2 {
		s.removeAssignment(new1.DelegateID)
		s.applyAssignment(r1)
		s.applyAssignment(r2)
		return false
	}
	s.applyAssignment(new2)
	after := objective(s)
	if after > before {
		return true
	}
	s.removeAssignment(new1.DelegateID)
	s.removeAssignment(new2.DelegateID)
	s.applyAssignment(r1)
	s.applyAssignment(r2)
	return false
}

// tryThreeCycle attempts d1->p2, d2->p3, d3->p1 (rotation). Reverts on failure.
func tryThreeCycle(s *workingState, i, j, k int) bool {
	a1 := s.assignments[i]
	a2 := s.assignments[j]
	a3 := s.assignments[k]
	if a1.PositionID == a2.PositionID || a2.PositionID == a3.PositionID || a1.PositionID == a3.PositionID {
		return false
	}
	before := objective(s)

	r1, _ := s.removeAssignment(a1.DelegateID)
	r2, _ := s.removeAssignment(a2.DelegateID)
	r3, _ := s.removeAssignment(a3.DelegateID)

	new1, ok1 := buildAssignment(s, r1.DelegateID, r2.PositionID)
	if !ok1 {
		s.applyAssignment(r1)
		s.applyAssignment(r2)
		s.applyAssignment(r3)
		return false
	}
	s.applyAssignment(new1)
	new2, ok2 := buildAssignment(s, r2.DelegateID, r3.PositionID)
	if !ok2 {
		s.removeAssignment(new1.DelegateID)
		s.applyAssignment(r1)
		s.applyAssignment(r2)
		s.applyAssignment(r3)
		return false
	}
	s.applyAssignment(new2)
	new3, ok3 := buildAssignment(s, r3.DelegateID, r1.PositionID)
	if !ok3 {
		s.removeAssignment(new1.DelegateID)
		s.removeAssignment(new2.DelegateID)
		s.applyAssignment(r1)
		s.applyAssignment(r2)
		s.applyAssignment(r3)
		return false
	}
	s.applyAssignment(new3)
	after := objective(s)
	if after > before {
		return true
	}
	s.removeAssignment(new1.DelegateID)
	s.removeAssignment(new2.DelegateID)
	s.removeAssignment(new3.DelegateID)
	s.applyAssignment(r1)
	s.applyAssignment(r2)
	s.applyAssignment(r3)
	return false
}

// buildAssignment constructs a ProposedAssignment if did fits at pid under
// H2/H3/H4. The caller is responsible for state consistency around this call.
func buildAssignment(s *workingState, did, pid string) (ProposedAssignment, bool) {
	p, ok := s.positions[pid]
	if !ok {
		return ProposedAssignment{}, false
	}
	if p.PrestigeTier == PrestigeReserved {
		return ProposedAssignment{}, false
	}
	if s.positionRemaining[pid] <= 0 {
		return ProposedAssignment{}, false
	}
	d := s.delegates[did]
	del := s.delegations[d.DelegationID]
	school := s.schoolOf[did]
	if s.clusterBySchoolCommittee[school][p.CommitteeID] >= 2 {
		return ProposedAssignment{}, false
	}
	if p.DualDelegation {
		// Cycle disallowed on dual-delegation seats; eligibleIndices filters
		// them, but defensive guard here for direct callers.
		occupants := s.positionFilledBy[pid]
		if len(occupants) > 0 && s.schoolOf[occupants[0]] != school {
			return ProposedAssignment{}, false
		}
	}
	c := s.committees[p.CommitteeID]
	score := perAssignmentScore(d, del.CommitteePreferences, p, c)
	return ProposedAssignment{
		DelegateID:   did,
		PositionID:   p.ID,
		CommitteeID:  c.ID,
		DelegationID: del.ID,
		Score:        score,
		Reason:       reasonForAssignment(d, del, p, c, false),
	}, true
}
