package assignment

// Scoring weights. Tuning lives in code per ASSIGNMENT_ALGORITHM.md §4.4.
const (
	WType       = 1.0
	WSize       = 0.8
	WExperience = 0.5
	WSingleSeat = 0.2
	WFairness   = 10.0
	WSpread     = 2.0
	WBalance    = 0.1

	// Negative-vs-positive asymmetry from §4.1.
	prefPositive = 1.0
	prefNegative = -2.0
	prefNeutral  = 0.0
)

// typeMatch scores the committee-type axis of the trinary preference.
func typeMatch(pref CommitteePreferences, t CommitteeType) float64 {
	var v Trinary
	switch t {
	case CommitteeTypeCrisis:
		v = pref.TypeCrisis
	case CommitteeTypeNonCrisis:
		v = pref.TypeNonCrisis
	}
	return trinaryScore(v)
}

// sizeMatch scores the committee-size axis of the trinary preference.
func sizeMatch(pref CommitteePreferences, s CommitteeSize) float64 {
	var v Trinary
	switch s {
	case CommitteeSizeSmall:
		v = pref.SizeSmall
	case CommitteeSizeMedium:
		v = pref.SizeMedium
	case CommitteeSizeLarge:
		v = pref.SizeLarge
	}
	return trinaryScore(v)
}

func trinaryScore(v Trinary) float64 {
	switch v {
	case TrinaryPositive:
		return prefPositive
	case TrinaryNegative:
		return prefNegative
	default:
		return prefNeutral
	}
}

// experienceMatch rewards advanced delegates on elevated/crisis seats and
// novices on non-crisis seats. Anything else returns 0. §4.2.
func experienceMatch(d Delegate, p Position, c Committee) float64 {
	switch d.ExperienceLevel {
	case ExperienceAdvanced:
		if p.PrestigeTier == PrestigeElevated || c.Type == CommitteeTypeCrisis {
			return 1.0
		}
	case ExperienceNovice:
		if c.Type == CommitteeTypeNonCrisis {
			return 1.0
		}
	}
	return 0.0
}

// singleSeatBonus prefers maxDelegates==1 positions (priority 5). §4.2.
func singleSeatBonus(p Position) float64 {
	if p.MaxDelegates == 1 {
		return 1.0
	}
	return 0.0
}

// perAssignmentScore is s(d, p) from §4.2.
func perAssignmentScore(d Delegate, pref CommitteePreferences, p Position, c Committee) float64 {
	return WType*typeMatch(pref, c.Type) +
		WSize*sizeMatch(pref, c.Size) +
		WExperience*experienceMatch(d, p, c) +
		WSingleSeat*singleSeatBonus(p)
}

// objective is the global O from §4.3. Computed against the current state.
func objective(s *workingState) float64 {
	var sum float64
	for _, a := range s.assignments {
		sum += a.Score
	}

	sum += WFairness * fairnessTerm(s)
	sum -= WSpread * spreadPenalty(s)
	sum -= WBalance * balancePenalty(s)
	return sum
}

// fairnessTerm is the minimum per-delegation average s(d,p) — a leximin-style
// floor. Delegations with no proposed assignments are skipped (they have no
// score to defend).
func fairnessTerm(s *workingState) float64 {
	type acc struct {
		sum   float64
		count int
	}
	per := make(map[string]*acc, len(s.delegations))
	for _, a := range s.assignments {
		ac, ok := per[a.DelegationID]
		if !ok {
			ac = &acc{}
			per[a.DelegationID] = ac
		}
		ac.sum += a.Score
		ac.count++
	}
	if len(per) == 0 {
		return 0
	}
	first := true
	var minAvg float64
	for _, ac := range per {
		if ac.count == 0 {
			continue
		}
		avg := ac.sum / float64(ac.count)
		if first || avg < minAvg {
			minAvg = avg
			first = false
		}
	}
	return minAvg
}

// spreadPenalty is Σ over (delegation, committee) of max(0, count-1)^2. §4.3.
func spreadPenalty(s *workingState) float64 {
	var total float64
	for _, count := range s.clusterByDelegationCommittee {
		if count > 1 {
			over := float64(count - 1)
			total += over * over
		}
	}
	return total
}

// balancePenalty is variance of fill rates across committees. §4.3.
func balancePenalty(s *workingState) float64 {
	if len(s.committees) == 0 {
		return 0
	}
	rates := make([]float64, 0, len(s.committees))
	for _, c := range s.committees {
		cap := s.committeeCapacity[c.ID]
		if cap == 0 {
			continue
		}
		fill := float64(s.committeeFill[c.ID]) / float64(cap)
		rates = append(rates, fill)
	}
	if len(rates) == 0 {
		return 0
	}
	var mean float64
	for _, r := range rates {
		mean += r
	}
	mean /= float64(len(rates))
	var variance float64
	for _, r := range rates {
		d := r - mean
		variance += d * d
	}
	return variance / float64(len(rates))
}
