package assignment

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// HashInputs returns the sha256 hex digest of a canonicalized Inputs payload.
// Canonicalization sorts every entity slice by id and emits fields in a fixed
// order; same Inputs (regardless of slice order) produces the same digest.
// Stored on AssignmentRun.inputsHash per ASSIGNMENT_ALGORITHM.md §10.5.
func HashInputs(in Inputs) string {
	var b strings.Builder
	b.Grow(4096)
	b.WriteString("conf=")
	b.WriteString(in.Conference.ID)
	b.WriteByte('\n')

	dels := append([]Delegation(nil), in.Delegations...)
	sort.Slice(dels, func(i, j int) bool { return dels[i].ID < dels[j].ID })
	for _, d := range dels {
		b.WriteString("delegation|")
		b.WriteString(d.ID)
		b.WriteByte('|')
		b.WriteString(d.School)
		b.WriteByte('|')
		b.WriteString(string(d.CommitteePreferences.TypeCrisis))
		b.WriteByte('|')
		b.WriteString(string(d.CommitteePreferences.TypeNonCrisis))
		b.WriteByte('|')
		b.WriteString(string(d.CommitteePreferences.SizeSmall))
		b.WriteByte('|')
		b.WriteString(string(d.CommitteePreferences.SizeMedium))
		b.WriteByte('|')
		b.WriteString(string(d.CommitteePreferences.SizeLarge))
		b.WriteByte('\n')
	}

	delegates := append([]Delegate(nil), in.Delegates...)
	sort.Slice(delegates, func(i, j int) bool { return delegates[i].ID < delegates[j].ID })
	for _, d := range delegates {
		b.WriteString("delegate|")
		b.WriteString(d.ID)
		b.WriteByte('|')
		b.WriteString(d.DelegationID)
		b.WriteByte('|')
		b.WriteString(string(d.ExperienceLevel))
		b.WriteByte('\n')
	}

	comms := append([]Committee(nil), in.Committees...)
	sort.Slice(comms, func(i, j int) bool { return comms[i].ID < comms[j].ID })
	for _, c := range comms {
		b.WriteString("committee|")
		b.WriteString(c.ID)
		b.WriteByte('|')
		b.WriteString(string(c.Type))
		b.WriteByte('|')
		b.WriteString(string(c.Size))
		b.WriteByte('\n')
	}

	positions := append([]Position(nil), in.Positions...)
	sort.Slice(positions, func(i, j int) bool { return positions[i].ID < positions[j].ID })
	for _, p := range positions {
		b.WriteString("position|")
		b.WriteString(p.ID)
		b.WriteByte('|')
		b.WriteString(p.CommitteeID)
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(p.MaxDelegates))
		b.WriteByte('|')
		b.WriteString(strconv.FormatBool(p.DualDelegation))
		b.WriteByte('|')
		b.WriteString(string(p.PrestigeTier))
		b.WriteByte('\n')
	}

	pinned := append([]PinnedAssignment(nil), in.PinnedAssignments...)
	sort.Slice(pinned, func(i, j int) bool {
		if pinned[i].PositionID != pinned[j].PositionID {
			return pinned[i].PositionID < pinned[j].PositionID
		}
		return pinned[i].DelegateID < pinned[j].DelegateID
	})
	for _, pa := range pinned {
		b.WriteString("pinned|")
		b.WriteString(pa.PositionID)
		b.WriteByte('|')
		b.WriteString(pa.DelegateID)
		b.WriteByte('|')
		b.WriteString(pa.CommitteeID)
		b.WriteByte('|')
		b.WriteString(pa.DelegationID)
		b.WriteByte('\n')
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
