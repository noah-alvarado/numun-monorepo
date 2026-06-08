package assignment

import "fmt"

// precheckFeasibility runs the §5.1.6 feasibility gates. Each failure returns
// a *ProposeError with Phase="precheck" and a human-readable diagnostic.
func precheckFeasibility(s *workingState) error {
	// Seat count: unassigned delegates must fit in remaining non-reserved seats.
	var remainingSeats int
	for _, pid := range s.positionIDs {
		remainingSeats += s.positionRemaining[pid]
	}
	unassigned := len(s.delegateIDs) - len(s.assignedOf)
	if remainingSeats < unassigned {
		return &ProposeError{
			Phase:   "precheck",
			Message: fmt.Sprintf("infeasible: %d unassigned delegates exceed %d available seats", unassigned, remainingSeats),
		}
	}

	// School spread (H3 necessary condition): delegateCount <= 2 * committeeCount.
	committeeCount := 0
	for _, cid := range s.committeeIDs {
		if s.committeeCapacity[cid] > 0 {
			committeeCount++
		}
	}
	for _, school := range sortedKeys(s.delegatesBySchool) {
		delegateCount := len(s.delegatesBySchool[school])
		// Subtract pinned delegates from this school — they already occupy committees.
		freeFromSchool := 0
		for _, did := range s.delegatesBySchool[school] {
			if !s.pinnedDelegate[did] {
				freeFromSchool++
			}
		}
		if freeFromSchool > 2*committeeCount {
			return &ProposeError{
				Phase:      "precheck",
				Constraint: "H3",
				Message: fmt.Sprintf("infeasible: school %q has %d unassigned delegates but only %d committees available (need <= %d)",
					school, freeFromSchool, committeeCount, 2*committeeCount),
			}
		}
		_ = delegateCount
	}

	// Dual-delegation feasibility (H4): every empty dual-delegation position
	// must have at least one school with ≥2 unpinned delegates and committee
	// cluster headroom.
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if !p.DualDelegation || p.PrestigeTier == PrestigeReserved {
			continue
		}
		if s.positionRemaining[pid] < 2 {
			// Could be pre-filled (pinned). If it's not full, that's an inputs bug.
			if s.positionRemaining[pid] == 1 {
				return &ProposeError{
					Phase:      "precheck",
					Constraint: "H4",
					Message:    fmt.Sprintf("dual-delegation position %q has 1 pinned seat; both seats must be pinned together", pid),
				}
			}
			continue
		}
		if !hasDualDelegationCandidate(s, p) {
			return &ProposeError{
				Phase:      "precheck",
				Constraint: "H4",
				Message:    fmt.Sprintf("dual-delegation position %q has no school with 2+ delegates and committee headroom", pid),
			}
		}
	}
	return nil
}

func hasDualDelegationCandidate(s *workingState, p Position) bool {
	for _, school := range sortedKeys(s.delegatesBySchool) {
		free := 0
		for _, did := range s.delegatesBySchool[school] {
			if !s.pinnedDelegate[did] {
				free++
			}
		}
		if free < 2 {
			continue
		}
		current := s.clusterBySchoolCommittee[school][p.CommitteeID]
		if current+2 <= 2 {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Reusing sort.Strings would force an import here; defer to inline.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
