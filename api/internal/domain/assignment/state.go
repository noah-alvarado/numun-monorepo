package assignment

import (
	"fmt"
	"sort"
)

// workingState is the mutable bookkeeping that flows through phases A-D.
// Field names are kept short because every phase touches several of them.
type workingState struct {
	conferenceID string

	delegations map[string]Delegation
	delegates   map[string]Delegate
	committees  map[string]Committee
	positions   map[string]Position

	// Sorted identifier lists so iteration order is deterministic.
	delegationIDs []string
	delegateIDs   []string
	committeeIDs  []string
	positionIDs   []string

	// Per-delegation roster (sorted by id).
	delegatesByDelegation map[string][]string
	// Per-school roster across delegations (deduped, sorted by id).
	delegatesBySchool map[string][]string
	// Reverse lookup from delegate to its school via delegation.
	schoolOf map[string]string

	// Per-committee positions (sorted by id) — handy for capacity sums.
	positionsByCommittee map[string][]string

	// Capacity bookkeeping.
	positionRemaining map[string]int // remaining seats per position
	positionFilledBy  map[string][]string
	committeeCapacity map[string]int // total seats in non-reserved positions
	committeeFill     map[string]int // currently occupied seats per committee

	// School cluster cap (H3) — count per (school, committee).
	clusterBySchoolCommittee map[string]map[string]int
	// Spread tracking — count per (delegation, committee). §4.3.
	clusterByDelegationCommittee map[string]int

	// Live assignment state. ProposedAssignments are appended as placements
	// happen. Pinned assignments are inserted up-front and never mutated.
	assignments    []ProposedAssignment
	pinnedPosition map[string]bool // positionId -> any pinned occupant
	pinnedDelegate map[string]bool // delegateId pinned
	assignedOf     map[string]int  // delegateId -> index into assignments
}

func newWorkingState(in Inputs) (*workingState, error) {
	s := &workingState{
		conferenceID: in.Conference.ID,
		delegations:  make(map[string]Delegation, len(in.Delegations)),
		delegates:    make(map[string]Delegate, len(in.Delegates)),
		committees:   make(map[string]Committee, len(in.Committees)),
		positions:    make(map[string]Position, len(in.Positions)),

		delegatesByDelegation: make(map[string][]string),
		delegatesBySchool:     make(map[string][]string),
		schoolOf:              make(map[string]string),
		positionsByCommittee:  make(map[string][]string),

		positionRemaining: make(map[string]int),
		positionFilledBy:  make(map[string][]string),
		committeeCapacity: make(map[string]int),
		committeeFill:     make(map[string]int),

		clusterBySchoolCommittee:     make(map[string]map[string]int),
		clusterByDelegationCommittee: make(map[string]int),

		pinnedPosition: make(map[string]bool),
		pinnedDelegate: make(map[string]bool),
		assignedOf:     make(map[string]int),
	}

	for _, d := range in.Delegations {
		if _, dup := s.delegations[d.ID]; dup {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("duplicate delegation id %q", d.ID)}
		}
		s.delegations[d.ID] = d
	}
	for _, d := range in.Delegates {
		if _, dup := s.delegates[d.ID]; dup {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("duplicate delegate id %q", d.ID)}
		}
		s.delegates[d.ID] = d
	}
	for _, c := range in.Committees {
		if _, dup := s.committees[c.ID]; dup {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("duplicate committee id %q", c.ID)}
		}
		s.committees[c.ID] = c
	}
	for _, p := range in.Positions {
		if _, dup := s.positions[p.ID]; dup {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("duplicate position id %q", p.ID)}
		}
		s.positions[p.ID] = p
	}

	for id := range s.delegations {
		s.delegationIDs = append(s.delegationIDs, id)
	}
	for id := range s.committees {
		s.committeeIDs = append(s.committeeIDs, id)
	}
	for id := range s.positions {
		s.positionIDs = append(s.positionIDs, id)
	}
	for id := range s.delegates {
		s.delegateIDs = append(s.delegateIDs, id)
	}
	sort.Strings(s.delegationIDs)
	sort.Strings(s.committeeIDs)
	sort.Strings(s.positionIDs)
	sort.Strings(s.delegateIDs)

	for _, did := range s.delegateIDs {
		d := s.delegates[did]
		del, ok := s.delegations[d.DelegationID]
		if !ok {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("delegate %q references unknown delegation %q", d.ID, d.DelegationID)}
		}
		s.delegatesByDelegation[d.DelegationID] = append(s.delegatesByDelegation[d.DelegationID], did)
		s.delegatesBySchool[del.School] = append(s.delegatesBySchool[del.School], did)
		s.schoolOf[did] = del.School
	}
	for k := range s.delegatesByDelegation {
		sort.Strings(s.delegatesByDelegation[k])
	}
	for k := range s.delegatesBySchool {
		sort.Strings(s.delegatesBySchool[k])
	}

	for _, pid := range s.positionIDs {
		p := s.positions[pid]
		if _, ok := s.committees[p.CommitteeID]; !ok {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("position %q references unknown committee %q", p.ID, p.CommitteeID)}
		}
		s.positionsByCommittee[p.CommitteeID] = append(s.positionsByCommittee[p.CommitteeID], pid)

		if p.PrestigeTier == PrestigeReserved {
			s.positionRemaining[pid] = 0
			continue
		}
		s.positionRemaining[pid] = p.MaxDelegates
		s.committeeCapacity[p.CommitteeID] += p.MaxDelegates
	}
	for k := range s.positionsByCommittee {
		sort.Strings(s.positionsByCommittee[k])
	}

	// Apply pinned assignments — H6.
	for _, pa := range in.PinnedAssignments {
		if _, ok := s.positions[pa.PositionID]; !ok {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("pinned assignment references unknown position %q", pa.PositionID)}
		}
		if _, ok := s.delegates[pa.DelegateID]; !ok {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("pinned assignment references unknown delegate %q", pa.DelegateID)}
		}
		if s.pinnedDelegate[pa.DelegateID] {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("delegate %q is pinned to multiple positions", pa.DelegateID)}
		}
		if s.positionRemaining[pa.PositionID] <= 0 {
			return nil, &ProposeError{Phase: "setup", Message: fmt.Sprintf("pinned assignment exceeds capacity of position %q", pa.PositionID)}
		}
		s.applyAssignment(ProposedAssignment{
			DelegateID:   pa.DelegateID,
			PositionID:   pa.PositionID,
			CommitteeID:  pa.CommitteeID,
			DelegationID: pa.DelegationID,
			Score:        pa.Score,
			Reason:       pa.Reason,
		})
		s.pinnedPosition[pa.PositionID] = true
		s.pinnedDelegate[pa.DelegateID] = true
	}

	return s, nil
}

// applyAssignment registers a placement in every bookkeeping map.
func (s *workingState) applyAssignment(a ProposedAssignment) {
	idx := len(s.assignments)
	s.assignments = append(s.assignments, a)
	s.assignedOf[a.DelegateID] = idx
	s.positionRemaining[a.PositionID]--
	s.positionFilledBy[a.PositionID] = append(s.positionFilledBy[a.PositionID], a.DelegateID)
	s.committeeFill[a.CommitteeID]++

	school := s.schoolOf[a.DelegateID]
	if school != "" {
		m := s.clusterBySchoolCommittee[school]
		if m == nil {
			m = make(map[string]int)
			s.clusterBySchoolCommittee[school] = m
		}
		m[a.CommitteeID]++
	}
	if a.DelegationID != "" {
		s.clusterByDelegationCommittee[clusterKey(a.DelegationID, a.CommitteeID)]++
	}
}

// removeAssignment is used by Phase C swaps. It expects the delegate is
// currently assigned.
func (s *workingState) removeAssignment(delegateID string) (ProposedAssignment, bool) {
	idx, ok := s.assignedOf[delegateID]
	if !ok {
		return ProposedAssignment{}, false
	}
	a := s.assignments[idx]
	last := len(s.assignments) - 1
	if idx != last {
		s.assignments[idx] = s.assignments[last]
		s.assignedOf[s.assignments[idx].DelegateID] = idx
	}
	s.assignments = s.assignments[:last]
	delete(s.assignedOf, delegateID)

	s.positionRemaining[a.PositionID]++
	occupants := s.positionFilledBy[a.PositionID]
	for i, did := range occupants {
		if did == delegateID {
			occupants = append(occupants[:i], occupants[i+1:]...)
			break
		}
	}
	if len(occupants) == 0 {
		delete(s.positionFilledBy, a.PositionID)
	} else {
		s.positionFilledBy[a.PositionID] = occupants
	}
	s.committeeFill[a.CommitteeID]--

	school := s.schoolOf[delegateID]
	if school != "" {
		if m := s.clusterBySchoolCommittee[school]; m != nil {
			m[a.CommitteeID]--
			if m[a.CommitteeID] <= 0 {
				delete(m, a.CommitteeID)
			}
		}
	}
	if a.DelegationID != "" {
		k := clusterKey(a.DelegationID, a.CommitteeID)
		s.clusterByDelegationCommittee[k]--
		if s.clusterByDelegationCommittee[k] <= 0 {
			delete(s.clusterByDelegationCommittee, k)
		}
	}
	return a, true
}

// unassignedDelegateIDs returns delegate ids without an assignment, sorted.
func (s *workingState) unassignedDelegateIDs() []string {
	out := make([]string, 0, len(s.delegateIDs))
	for _, id := range s.delegateIDs {
		if _, ok := s.assignedOf[id]; !ok {
			out = append(out, id)
		}
	}
	return out
}

// clusterKey is the composite key for clusterByDelegationCommittee.
func clusterKey(delegationID, committeeID string) string {
	return delegationID + "\x00" + committeeID
}
