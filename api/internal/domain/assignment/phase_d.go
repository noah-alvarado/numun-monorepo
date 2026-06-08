package assignment

import (
	"fmt"
	"sort"
	"strings"
)

// validate runs H1..H6 against the final state. Any violation is a bug — we
// return a *ProposeError with Phase="phase-D" and the constraint name.
func validate(s *workingState) error {
	// H1: every non-pinned delegate must be assigned exactly one position.
	for _, did := range s.delegateIDs {
		if _, ok := s.assignedOf[did]; !ok {
			return &ProposeError{Phase: "phase-D", Constraint: "H1", Message: fmt.Sprintf("delegate %q is unassigned", did)}
		}
	}
	// Each delegate appears in exactly one assignment.
	count := make(map[string]int, len(s.delegateIDs))
	for _, a := range s.assignments {
		count[a.DelegateID]++
	}
	for did, n := range count {
		if n != 1 {
			return &ProposeError{Phase: "phase-D", Constraint: "H1", Message: fmt.Sprintf("delegate %q has %d assignments (expected 1)", did, n)}
		}
	}

	// H2: no position exceeds its maxDelegates.
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		used := len(s.positionFilledBy[pid])
		if used > p.MaxDelegates {
			return &ProposeError{Phase: "phase-D", Constraint: "H2", Message: fmt.Sprintf("position %q has %d occupants (max %d)", pid, used, p.MaxDelegates)}
		}
	}

	// H3: no more than 2 delegates from the same school per committee.
	for school, m := range s.clusterBySchoolCommittee {
		for cid, n := range m {
			if n > 2 {
				return &ProposeError{Phase: "phase-D", Constraint: "H3", Message: fmt.Sprintf("school %q has %d delegates in committee %q", school, n, cid)}
			}
		}
	}

	// H4: dual-delegation positions both seats same school (when filled).
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if !p.DualDelegation {
			continue
		}
		occupants := s.positionFilledBy[pid]
		if len(occupants) == 0 {
			continue
		}
		if len(occupants) != 2 {
			return &ProposeError{Phase: "phase-D", Constraint: "H4", Message: fmt.Sprintf("dual-delegation position %q has %d occupants (expected 0 or 2)", pid, len(occupants))}
		}
		if s.schoolOf[occupants[0]] != s.schoolOf[occupants[1]] {
			return &ProposeError{Phase: "phase-D", Constraint: "H4", Message: fmt.Sprintf("dual-delegation position %q has occupants from different schools", pid)}
		}
	}

	// H5: reserved positions remain empty.
	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if p.PrestigeTier == PrestigeReserved && len(s.positionFilledBy[pid]) > 0 {
			return &ProposeError{Phase: "phase-D", Constraint: "H5", Message: fmt.Sprintf("reserved position %q has occupants", pid)}
		}
	}

	// H6: every pinned assignment is present in the final state.
	for did := range s.pinnedDelegate {
		idx, ok := s.assignedOf[did]
		if !ok {
			return &ProposeError{Phase: "phase-D", Constraint: "H6", Message: fmt.Sprintf("pinned delegate %q is missing", did)}
		}
		_ = s.assignments[idx]
	}
	return nil
}

// buildProposal emits the final Proposal with deterministic ordering and a
// recomputed Objective. assignments are sorted by (committeeId, positionId,
// delegateId).
func buildProposal(s *workingState) *Proposal {
	out := make([]ProposedAssignment, len(s.assignments))
	copy(out, s.assignments)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CommitteeID != out[j].CommitteeID {
			return out[i].CommitteeID < out[j].CommitteeID
		}
		if out[i].PositionID != out[j].PositionID {
			return out[i].PositionID < out[j].PositionID
		}
		return out[i].DelegateID < out[j].DelegateID
	})
	return &Proposal{
		Assignments: out,
		Objective:   objective(s),
	}
}

// reasonForAssignment produces a terse human-readable explanation for one
// placement. dualPaired=true is used for Phase A pairings.
func reasonForAssignment(d Delegate, del Delegation, p Position, c Committee, dualPaired bool) string {
	parts := make([]string, 0, 4)
	if dualPaired {
		parts = append(parts, "dual-delegation pairing from same school")
	}

	// Type-axis fit.
	switch typePref(del.CommitteePreferences, c.Type) {
	case TrinaryPositive:
		parts = append(parts, fmt.Sprintf("matched delegation's positive %s preference", c.Type))
	case TrinaryNegative:
		parts = append(parts, fmt.Sprintf("violated delegation's negative %s preference (no feasible alternative)", c.Type))
	}
	// Size-axis fit.
	switch sizePref(del.CommitteePreferences, c.Size) {
	case TrinaryPositive:
		parts = append(parts, fmt.Sprintf("matched delegation's positive %s-committee preference", c.Size))
	case TrinaryNegative:
		parts = append(parts, fmt.Sprintf("violated delegation's negative %s-committee preference (no feasible alternative)", c.Size))
	}
	// Experience alignment.
	if experienceMatch(d, p, c) > 0 {
		parts = append(parts, fmt.Sprintf("%s experience aligned with %s committee", d.ExperienceLevel, c.Type))
	}
	if p.MaxDelegates == 1 {
		parts = append(parts, "single-seat position")
	}
	if len(parts) == 0 {
		return "neutral preference match"
	}
	return strings.Join(parts, "; ")
}

func typePref(pref CommitteePreferences, t CommitteeType) Trinary {
	switch t {
	case CommitteeTypeCrisis:
		return pref.TypeCrisis
	case CommitteeTypeNonCrisis:
		return pref.TypeNonCrisis
	}
	return TrinaryNeutral
}

func sizePref(pref CommitteePreferences, sz CommitteeSize) Trinary {
	switch sz {
	case CommitteeSizeSmall:
		return pref.SizeSmall
	case CommitteeSizeMedium:
		return pref.SizeMedium
	case CommitteeSizeLarge:
		return pref.SizeLarge
	}
	return TrinaryNeutral
}
