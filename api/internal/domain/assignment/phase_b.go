package assignment

import (
	"fmt"
	"math/rand"
	"sort"
)

// greedyMainPass is Phase B from §5.3. Shuffles unassigned delegates using
// the supplied RNG, places each at its highest-scoring candidate (with
// marginal global-term adjustments), then runs a bounded backtracking pass
// over any deferred delegates.
func greedyMainPass(s *workingState, rng *rand.Rand, backtrackBudget int) error {
	queue := s.unassignedDelegateIDs()
	rng.Shuffle(len(queue), func(i, j int) {
		queue[i], queue[j] = queue[j], queue[i]
	})

	deferred := make([]string, 0)
	for _, did := range queue {
		if !placeBest(s, did) {
			deferred = append(deferred, did)
		}
	}

	if len(deferred) == 0 {
		return nil
	}

	attempts := 0
	for len(deferred) > 0 && attempts < backtrackBudget {
		attempts++
		did := deferred[0]
		deferred = deferred[1:]
		if placeBest(s, did) {
			continue
		}
		if tryRebalance(s, did) {
			continue
		}
		deferred = append(deferred, did)
		// If every remaining deferred delegate has been tried at least once
		// in this pass without progress, give up.
		if attempts >= backtrackBudget {
			break
		}
	}
	if len(deferred) > 0 {
		return &ProposeError{
			Phase:      "phase-B",
			Constraint: "H1",
			Message:    fmt.Sprintf("could not place %d delegate(s) after %d backtrack attempts", len(deferred), attempts),
		}
	}
	return nil
}

// placeBest finds the highest marginal-scored feasible candidate position for
// did and applies it. Returns false if no candidate satisfies H2/H3/H4.
func placeBest(s *workingState, did string) bool {
	cand := bestCandidate(s, did)
	if cand == nil {
		return false
	}
	d := s.delegates[did]
	del := s.delegations[d.DelegationID]
	p := s.positions[cand.positionID]
	c := s.committees[p.CommitteeID]
	score := perAssignmentScore(d, del.CommitteePreferences, p, c)
	s.applyAssignment(ProposedAssignment{
		DelegateID:   did,
		PositionID:   p.ID,
		CommitteeID:  c.ID,
		DelegationID: del.ID,
		Score:        score,
		Reason:       reasonForAssignment(d, del, p, c, false),
	})
	return true
}

type candidate struct {
	positionID string
	score      float64
	fillRatio  float64
	committee  string
}

// bestCandidate enumerates feasible positions, ranks by adjusted marginal
// score, and returns the winner. nil if none feasible.
func bestCandidate(s *workingState, did string) *candidate {
	d := s.delegates[did]
	del := s.delegations[d.DelegationID]
	school := s.schoolOf[did]

	best := (*candidate)(nil)
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if p.PrestigeTier == PrestigeReserved {
			continue
		}
		if s.positionRemaining[pid] <= 0 {
			continue
		}
		// H4: skip empty dual-delegation positions for Phase B (Phase A handles them).
		if p.DualDelegation {
			occupants := s.positionFilledBy[pid]
			if len(occupants) == 0 {
				continue
			}
			// Sibling already seated; school must match.
			sibSchool := s.schoolOf[occupants[0]]
			if sibSchool != school {
				continue
			}
		}
		// H3: cluster cap of 2 per (school, committee).
		if s.clusterBySchoolCommittee[school][p.CommitteeID] >= 2 {
			continue
		}

		c := s.committees[p.CommitteeID]
		base := perAssignmentScore(d, del.CommitteePreferences, p, c)
		adj := marginalAdjustment(s, did, p, c, base)

		fillRatio := 0.0
		if cap := s.committeeCapacity[c.ID]; cap > 0 {
			fillRatio = float64(s.committeeFill[c.ID]) / float64(cap)
		}
		cur := &candidate{positionID: pid, score: base + adj, fillRatio: fillRatio, committee: c.ID}
		if best == nil || better(cur, best) {
			best = cur
		}
	}
	return best
}

func better(a, b *candidate) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.fillRatio != b.fillRatio {
		return a.fillRatio < b.fillRatio
	}
	if a.committee != b.committee {
		return a.committee < b.committee
	}
	return a.positionID < b.positionID
}

// marginalAdjustment approximates how placing did at p would shift the global
// terms (fairness floor + spread quadratic). Computed cheaply: we look at the
// delegation's current average and the (delegation, committee) cluster count.
func marginalAdjustment(s *workingState, did string, p Position, c Committee, base float64) float64 {
	d := s.delegates[did]
	del := s.delegations[d.DelegationID]

	// Fairness: reward placements that lift the delegation's average above the
	// current floor. Compute the delegation's average if we add base.
	var sum float64
	var count int
	for _, a := range s.assignments {
		if a.DelegationID == del.ID {
			sum += a.Score
			count++
		}
	}
	newAvg := (sum + base) / float64(count+1)
	currentFloor := fairnessTerm(s)
	fairnessDelta := 0.0
	if currentFloor == 0 && count == 0 {
		fairnessDelta = newAvg // first placement defines this delegation's floor
	} else if newAvg < currentFloor {
		fairnessDelta = newAvg - currentFloor
	} else if count == 0 {
		// Adding a new delegation to the floor calculation can only lower it
		// if newAvg < currentFloor (handled above). Otherwise neutral.
		fairnessDelta = 0
	}

	// Spread: quadratic penalty on (delegation, committee) cluster count.
	// Adding one more co-delegate makes the new term max(0, cur+1-1)^2 = cur^2,
	// replacing the previous max(0, cur-1)^2.
	cur := s.clusterByDelegationCommittee[clusterKey(del.ID, c.ID)]
	var spreadDelta float64
	if cur >= 1 {
		spreadDelta = float64(cur*cur) - float64((cur-1)*(cur-1))
	}
	return WFairness*fairnessDelta - WSpread*spreadDelta
}

// tryRebalance attempts to free a feasible slot for did by removing one
// already-placed (non-pinned) delegate and re-placing both. Cheap: one swap
// candidate at a time, deterministic order by positionId.
func tryRebalance(s *workingState, did string) bool {
	d := s.delegates[did]
	del := s.delegations[d.DelegationID]
	school := s.schoolOf[did]

	type swap struct{ pid, kickDid string }
	swaps := make([]swap, 0)
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if p.PrestigeTier == PrestigeReserved || p.DualDelegation {
			continue
		}
		occupants := s.positionFilledBy[pid]
		for _, occ := range occupants {
			if s.pinnedDelegate[occ] {
				continue
			}
			// Would placing did here exceed cluster cap if occ is removed?
			occSchool := s.schoolOf[occ]
			localCount := s.clusterBySchoolCommittee[school][p.CommitteeID]
			if occSchool == school {
				localCount-- // occ was contributing to school's count
			}
			if localCount >= 2 {
				continue
			}
			swaps = append(swaps, swap{pid, occ})
		}
	}
	sort.Slice(swaps, func(i, j int) bool {
		if swaps[i].pid != swaps[j].pid {
			return swaps[i].pid < swaps[j].pid
		}
		return swaps[i].kickDid < swaps[j].kickDid
	})
	for _, sw := range swaps {
		removed, ok := s.removeAssignment(sw.kickDid)
		if !ok {
			continue
		}
		p := s.positions[sw.pid]
		c := s.committees[p.CommitteeID]
		score := perAssignmentScore(d, del.CommitteePreferences, p, c)
		s.applyAssignment(ProposedAssignment{
			DelegateID:   did,
			PositionID:   p.ID,
			CommitteeID:  c.ID,
			DelegationID: del.ID,
			Score:        score,
			Reason:       reasonForAssignment(d, del, p, c, false),
		})
		// Now try to re-place the kicked delegate.
		if placeBest(s, sw.kickDid) {
			return true
		}
		// Rollback: pull did back, restore removed.
		s.removeAssignment(did)
		s.applyAssignment(removed)
	}
	return false
}
