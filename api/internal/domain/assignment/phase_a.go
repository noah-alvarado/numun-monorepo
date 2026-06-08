package assignment

import (
	"math/rand"
	"sort"
)

// seedDualDelegations is Phase A from §5.2. For each empty dual-delegation
// position (in sorted positionId order), pick the school with the highest
// combined per-delegate score that satisfies H3 at the committee level. Two
// delegates are taken from that school in sorted delegateId order.
func seedDualDelegations(s *workingState, _ *rand.Rand) error {
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if !p.DualDelegation || p.PrestigeTier == PrestigeReserved {
			continue
		}
		if s.positionRemaining[pid] < 2 {
			continue
		}
		c := s.committees[p.CommitteeID]

		bestSchool := ""
		bestScore := 0.0
		bestDelegates := []string{}

		for _, school := range sortedKeys(s.delegatesBySchool) {
			candidates := freeDelegatesFromSchool(s, school)
			if len(candidates) < 2 {
				continue
			}
			current := s.clusterBySchoolCommittee[school][c.ID]
			if current+2 > 2 {
				continue
			}

			// Score each pair by combined per-delegate score; the top two
			// delegates from this school are the deterministic pick.
			scored := make([]struct {
				id    string
				score float64
			}, 0, len(candidates))
			for _, did := range candidates {
				d := s.delegates[did]
				del := s.delegations[d.DelegationID]
				scored = append(scored, struct {
					id    string
					score float64
				}{did, perAssignmentScore(d, del.CommitteePreferences, p, c)})
			}
			sort.SliceStable(scored, func(i, j int) bool {
				if scored[i].score != scored[j].score {
					return scored[i].score > scored[j].score
				}
				return scored[i].id < scored[j].id
			})
			pairScore := scored[0].score + scored[1].score
			if bestSchool == "" || pairScore > bestScore ||
				(pairScore == bestScore && school < bestSchool) {
				bestSchool = school
				bestScore = pairScore
				bestDelegates = []string{scored[0].id, scored[1].id}
				sort.Strings(bestDelegates)
			}
		}
		if bestSchool == "" {
			// No feasible pairing — leave empty; Phase D will catch if it
			// matters. Spec allows revisit during local search.
			continue
		}
		for _, did := range bestDelegates {
			d := s.delegates[did]
			del := s.delegations[d.DelegationID]
			score := perAssignmentScore(d, del.CommitteePreferences, p, c)
			s.applyAssignment(ProposedAssignment{
				DelegateID:   did,
				PositionID:   pid,
				CommitteeID:  c.ID,
				DelegationID: del.ID,
				Score:        score,
				Reason:       reasonForAssignment(d, del, p, c, true),
			})
		}
	}
	return nil
}

func freeDelegatesFromSchool(s *workingState, school string) []string {
	all := s.delegatesBySchool[school]
	out := make([]string, 0, len(all))
	for _, did := range all {
		if _, taken := s.assignedOf[did]; taken {
			continue
		}
		out = append(out, did)
	}
	return out
}
