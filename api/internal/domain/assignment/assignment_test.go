package assignment

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// builder makes synthetic inputs concise across tests. All ids carry stable
// prefixes so assertions can target specific entities.
type builder struct {
	in Inputs
}

func newBuilder(confID string) *builder {
	return &builder{in: Inputs{Conference: Conference{ID: confID}}}
}

func (b *builder) delegation(id, school string, pref CommitteePreferences) *builder {
	b.in.Delegations = append(b.in.Delegations, Delegation{ID: id, School: school, CommitteePreferences: pref})
	return b
}

func (b *builder) delegate(id, delegationID string, lvl ExperienceLevel) *builder {
	b.in.Delegates = append(b.in.Delegates, Delegate{ID: id, DelegationID: delegationID, ExperienceLevel: lvl})
	return b
}

func (b *builder) committee(id string, t CommitteeType, sz CommitteeSize) *builder {
	b.in.Committees = append(b.in.Committees, Committee{ID: id, Type: t, Size: sz})
	return b
}

func (b *builder) position(id, committeeID string, max int, dual bool, tier PrestigeTier) *builder {
	b.in.Positions = append(b.in.Positions, Position{ID: id, CommitteeID: committeeID, MaxDelegates: max, DualDelegation: dual, PrestigeTier: tier})
	return b
}

func (b *builder) pinned(pa PinnedAssignment) *builder {
	b.in.PinnedAssignments = append(b.in.PinnedAssignments, pa)
	return b
}

func neutralPref() CommitteePreferences {
	return CommitteePreferences{
		TypeCrisis:    TrinaryNeutral,
		TypeNonCrisis: TrinaryNeutral,
		SizeSmall:     TrinaryNeutral,
		SizeMedium:    TrinaryNeutral,
		SizeLarge:     TrinaryNeutral,
	}
}

func defaultOpts() RunOptions {
	return RunOptions{Seed: 42, LocalSearchBudget: 200 * time.Millisecond, BacktrackBudget: 200}
}

func TestPropose_Trivial(t *testing.T) {
	in := newBuilder("conf-1").
		delegation("dl-1", "School A", neutralPref()).
		delegate("dg-1", "dl-1", ExperienceIntermediate).
		committee("cm-1", CommitteeTypeCrisis, CommitteeSizeMedium).
		position("po-1", "cm-1", 1, false, PrestigeStandard).
		in

	prop, err := Propose(context.Background(), in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prop.Assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(prop.Assignments))
	}
	a := prop.Assignments[0]
	if a.DelegateID != "dg-1" || a.PositionID != "po-1" || a.CommitteeID != "cm-1" || a.DelegationID != "dl-1" {
		t.Errorf("wrong assignment: %+v", a)
	}
}

func TestPropose_TinyAllConstraintsHold(t *testing.T) {
	b := newBuilder("conf-2")
	// 6 delegates across 2 schools, 3 committees with 2 positions each.
	pref := CommitteePreferences{
		TypeCrisis: TrinaryPositive, TypeNonCrisis: TrinaryNeutral,
		SizeSmall: TrinaryNeutral, SizeMedium: TrinaryPositive, SizeLarge: TrinaryNegative,
	}
	b.delegation("dl-A", "School A", pref).delegation("dl-B", "School B", pref)
	for i := 0; i < 3; i++ {
		b.delegate(fmt.Sprintf("dg-A-%d", i), "dl-A", ExperienceIntermediate)
		b.delegate(fmt.Sprintf("dg-B-%d", i), "dl-B", ExperienceIntermediate)
	}
	b.committee("cm-1", CommitteeTypeCrisis, CommitteeSizeSmall)
	b.committee("cm-2", CommitteeTypeNonCrisis, CommitteeSizeMedium)
	b.committee("cm-3", CommitteeTypeCrisis, CommitteeSizeLarge)
	for ci, cid := range []string{"cm-1", "cm-2", "cm-3"} {
		b.position(fmt.Sprintf("po-%d-a", ci), cid, 1, false, PrestigeStandard)
		b.position(fmt.Sprintf("po-%d-b", ci), cid, 1, false, PrestigeStandard)
	}

	prop, err := Propose(context.Background(), b.in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prop.Assignments) != 6 {
		t.Fatalf("expected 6 assignments, got %d", len(prop.Assignments))
	}
	checkAllConstraints(t, b.in, prop)
}

func TestPropose_DualDelegation(t *testing.T) {
	b := newBuilder("conf-3")
	pref := neutralPref()
	b.delegation("dl-A", "School A", pref).delegation("dl-B", "School B", pref)
	b.delegate("dg-A-1", "dl-A", ExperienceIntermediate)
	b.delegate("dg-A-2", "dl-A", ExperienceIntermediate)
	b.delegate("dg-B-1", "dl-B", ExperienceIntermediate)
	b.delegate("dg-B-2", "dl-B", ExperienceIntermediate)
	b.committee("cm-1", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.committee("cm-2", CommitteeTypeNonCrisis, CommitteeSizeMedium)
	b.position("po-dual-1", "cm-1", 2, true, PrestigeStandard)
	b.position("po-dual-2", "cm-2", 2, true, PrestigeStandard)

	prop, err := Propose(context.Background(), b.in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prop.Assignments) != 4 {
		t.Fatalf("expected 4 assignments, got %d", len(prop.Assignments))
	}
	// Verify both seats of each dual-delegation position share a school.
	bySchool := groupBySchool(t, b.in, prop)
	for _, pid := range []string{"po-dual-1", "po-dual-2"} {
		schools := map[string]bool{}
		for _, a := range prop.Assignments {
			if a.PositionID == pid {
				schools[bySchool[a.DelegateID]] = true
			}
		}
		if len(schools) != 1 {
			t.Errorf("position %q occupants span %d schools, expected 1", pid, len(schools))
		}
	}
}

func TestPropose_ReservedSkipped(t *testing.T) {
	b := newBuilder("conf-4")
	b.delegation("dl-1", "School A", neutralPref())
	b.delegate("dg-1", "dl-1", ExperienceIntermediate)
	b.committee("cm-1", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.position("po-1", "cm-1", 1, false, PrestigeStandard)
	b.position("po-reserved", "cm-1", 1, false, PrestigeReserved)

	prop, err := Propose(context.Background(), b.in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, a := range prop.Assignments {
		if a.PositionID == "po-reserved" {
			t.Errorf("reserved position got assigned: %+v", a)
		}
	}
}

func TestPropose_PinnedPreserved(t *testing.T) {
	b := newBuilder("conf-5")
	b.delegation("dl-A", "School A", neutralPref())
	b.delegation("dl-B", "School B", neutralPref())
	b.delegate("dg-A-1", "dl-A", ExperienceIntermediate)
	b.delegate("dg-A-2", "dl-A", ExperienceIntermediate)
	b.delegate("dg-B-1", "dl-B", ExperienceIntermediate)
	b.committee("cm-1", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.position("po-1", "cm-1", 1, false, PrestigeStandard)
	b.position("po-2", "cm-1", 1, false, PrestigeStandard)
	b.position("po-3", "cm-1", 1, false, PrestigeStandard)
	b.pinned(PinnedAssignment{
		DelegateID: "dg-A-1", PositionID: "po-1", CommitteeID: "cm-1", DelegationID: "dl-A",
		Score: 1.5, Reason: "pre-approved",
	})

	prop, err := Propose(context.Background(), b.in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var found *ProposedAssignment
	for i := range prop.Assignments {
		if prop.Assignments[i].DelegateID == "dg-A-1" {
			found = &prop.Assignments[i]
			break
		}
	}
	if found == nil {
		t.Fatal("pinned delegate disappeared from output")
	}
	if found.PositionID != "po-1" {
		t.Errorf("pinned delegate moved: %+v", found)
	}
	if found.Reason != "pre-approved" {
		t.Errorf("pinned reason changed: got %q", found.Reason)
	}
	if found.Score != 1.5 {
		t.Errorf("pinned score changed: got %v", found.Score)
	}
}

func TestPropose_InfeasibleSeats(t *testing.T) {
	b := newBuilder("conf-6")
	b.delegation("dl-1", "School A", neutralPref())
	b.delegate("dg-1", "dl-1", ExperienceIntermediate)
	b.delegate("dg-2", "dl-1", ExperienceIntermediate)
	b.committee("cm-1", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.position("po-1", "cm-1", 1, false, PrestigeStandard)

	_, err := Propose(context.Background(), b.in, defaultOpts())
	var pe *ProposeError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProposeError, got %T (%v)", err, err)
	}
	if pe.Phase != "precheck" {
		t.Errorf("expected precheck phase, got %q (%s)", pe.Phase, pe.Error())
	}
}

func TestPropose_DeterminismSameSeed(t *testing.T) {
	in := buildLargeCase(t, "conf-det", 12, 8, 2)
	opts := defaultOpts()
	opts.Seed = 12345

	p1, err := Propose(context.Background(), in, opts)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	p2, err := Propose(context.Background(), in, opts)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !reflect.DeepEqual(p1.Assignments, p2.Assignments) {
		t.Errorf("same seed produced different assignments")
	}
	if p1.Objective != p2.Objective {
		t.Errorf("objective drift: %v vs %v", p1.Objective, p2.Objective)
	}
}

func TestPropose_DifferentSeedShuffles(t *testing.T) {
	in := buildLargeCase(t, "conf-shuf", 16, 10, 2)
	opts := defaultOpts()

	opts.Seed = 1
	p1, err := Propose(context.Background(), in, opts)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	opts.Seed = 99999
	p2, err := Propose(context.Background(), in, opts)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if reflect.DeepEqual(p1.Assignments, p2.Assignments) {
		t.Errorf("different seeds produced identical assignments")
	}
}

func TestPropose_FairnessLeximin(t *testing.T) {
	// Two delegations of 1 delegate each, one committee with 2 single-seat
	// positions. School A's prefs strongly favor crisis; School B is neutral.
	// One position is crisis, one is non-crisis. The fairness floor improves
	// when A gets crisis; total score is the same either way.
	b := newBuilder("conf-fair")
	bestPref := CommitteePreferences{
		TypeCrisis: TrinaryPositive, TypeNonCrisis: TrinaryNeutral,
		SizeSmall: TrinaryNeutral, SizeMedium: TrinaryNeutral, SizeLarge: TrinaryNeutral,
	}
	b.delegation("dl-A", "School A", bestPref)
	b.delegation("dl-B", "School B", neutralPref())
	b.delegate("dg-A", "dl-A", ExperienceIntermediate)
	b.delegate("dg-B", "dl-B", ExperienceIntermediate)
	b.committee("cm-crisis", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.committee("cm-norm", CommitteeTypeNonCrisis, CommitteeSizeMedium)
	b.position("po-cri", "cm-crisis", 1, false, PrestigeStandard)
	b.position("po-norm", "cm-norm", 1, false, PrestigeStandard)

	// Try multiple seeds — every one should land A on crisis (only solution
	// that lifts the fairness floor).
	for _, seed := range []uint64{1, 2, 3, 7, 11, 42} {
		opts := defaultOpts()
		opts.Seed = seed
		prop, err := Propose(context.Background(), b.in, opts)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		for _, a := range prop.Assignments {
			if a.DelegateID == "dg-A" && a.PositionID != "po-cri" {
				t.Errorf("seed %d: A landed on %s, expected po-cri (fairness floor)", seed, a.PositionID)
			}
		}
	}
}

func TestPropose_NegativeDominatesNeutral(t *testing.T) {
	// Interview example: neutral on crisis, negative on small/large. Should
	// land in crisis over a small/large alternative even though "type" only
	// outranks "size" by 1.0 vs 0.8 — the 2× negative cost flips it.
	b := newBuilder("conf-neg")
	pref := CommitteePreferences{
		TypeCrisis: TrinaryNeutral, TypeNonCrisis: TrinaryNeutral,
		SizeSmall: TrinaryNegative, SizeMedium: TrinaryNeutral, SizeLarge: TrinaryNegative,
	}
	b.delegation("dl-1", "School A", pref)
	b.delegate("dg-1", "dl-1", ExperienceIntermediate)
	b.committee("cm-crisis-small", CommitteeTypeCrisis, CommitteeSizeSmall)
	b.committee("cm-norm-large", CommitteeTypeNonCrisis, CommitteeSizeLarge)
	b.committee("cm-crisis-med", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.position("po-1", "cm-crisis-small", 1, false, PrestigeStandard)
	b.position("po-2", "cm-norm-large", 1, false, PrestigeStandard)
	b.position("po-3", "cm-crisis-med", 1, false, PrestigeStandard)

	prop, err := Propose(context.Background(), b.in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prop.Assignments) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(prop.Assignments))
	}
	got := prop.Assignments[0].PositionID
	// Expected: po-3 (crisis + medium = 0 size penalty).
	if got != "po-3" {
		t.Errorf("expected delegate on po-3 (crisis+medium), got %s", got)
	}
}

func TestPropose_DiagnosticOnInfeasibility(t *testing.T) {
	// Same-school cluster cap: 5 delegates from one school but only 2 committees.
	b := newBuilder("conf-h3")
	b.delegation("dl-1", "School A", neutralPref())
	for i := 0; i < 5; i++ {
		b.delegate(fmt.Sprintf("dg-%d", i), "dl-1", ExperienceIntermediate)
	}
	b.committee("cm-1", CommitteeTypeCrisis, CommitteeSizeMedium)
	b.committee("cm-2", CommitteeTypeNonCrisis, CommitteeSizeMedium)
	b.position("po-1a", "cm-1", 3, false, PrestigeStandard)
	b.position("po-2a", "cm-2", 3, false, PrestigeStandard)

	_, err := Propose(context.Background(), b.in, defaultOpts())
	var pe *ProposeError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ProposeError, got %v", err)
	}
	if pe.Phase != "precheck" || pe.Constraint != "H3" {
		t.Errorf("expected precheck/H3, got %s/%s (%s)", pe.Phase, pe.Constraint, pe.Message)
	}
}

func TestHashInputs_Stable(t *testing.T) {
	in1 := buildLargeCase(t, "conf-hash", 5, 2, 2)
	in2 := buildLargeCase(t, "conf-hash", 5, 2, 2)
	// Shuffle the slices in in2 (order shouldn't matter).
	in2.Delegations = append([]Delegation{in2.Delegations[len(in2.Delegations)-1]}, in2.Delegations[:len(in2.Delegations)-1]...)
	in2.Delegates = append([]Delegate{in2.Delegates[len(in2.Delegates)-1]}, in2.Delegates[:len(in2.Delegates)-1]...)
	if HashInputs(in1) != HashInputs(in2) {
		t.Errorf("HashInputs should be order-independent")
	}
	in3 := buildLargeCase(t, "conf-hash", 5, 2, 2)
	in3.Conference.ID = "different"
	if HashInputs(in1) == HashInputs(in3) {
		t.Errorf("HashInputs should change when conference id changes")
	}
}

// buildLargeCase produces a feasible synthetic case with the given delegate /
// committee / positions-per-committee counts split between two schools. The
// number of seats is sized to leave a small surplus so the algorithm has
// choices.
func buildLargeCase(_ *testing.T, confID string, numDelegates, numCommittees, posPerCommittee int) Inputs {
	b := newBuilder(confID)
	pref := CommitteePreferences{
		TypeCrisis: TrinaryPositive, TypeNonCrisis: TrinaryNeutral,
		SizeSmall: TrinaryNeutral, SizeMedium: TrinaryPositive, SizeLarge: TrinaryNeutral,
	}
	b.delegation("dl-A", "School A", pref)
	b.delegation("dl-B", "School B", pref)
	for i := 0; i < numDelegates; i++ {
		school := "dl-A"
		if i%2 == 1 {
			school = "dl-B"
		}
		b.delegate(fmt.Sprintf("dg-%03d", i), school, ExperienceIntermediate)
	}
	// Ensure capacity = ceil(numDelegates / numCommittees) per committee.
	seatsPerCommittee := (numDelegates + numCommittees - 1) / numCommittees
	if seatsPerCommittee < posPerCommittee {
		seatsPerCommittee = posPerCommittee
	}
	for c := 0; c < numCommittees; c++ {
		ct := CommitteeTypeCrisis
		if c%2 == 1 {
			ct = CommitteeTypeNonCrisis
		}
		cs := []CommitteeSize{CommitteeSizeSmall, CommitteeSizeMedium, CommitteeSizeLarge}[c%3]
		cid := fmt.Sprintf("cm-%02d", c)
		b.committee(cid, ct, cs)
		// posPerCommittee positions; first one absorbs extra capacity if
		// seatsPerCommittee > posPerCommittee (max=multi-seat position).
		for p := 0; p < posPerCommittee; p++ {
			max := 1
			if p == 0 {
				max = seatsPerCommittee - (posPerCommittee - 1)
				if max < 1 {
					max = 1
				}
			}
			b.position(fmt.Sprintf("po-%02d-%02d", c, p), cid, max, false, PrestigeStandard)
		}
	}
	return b.in
}

// checkAllConstraints walks H1..H6 to validate algorithm output.
func checkAllConstraints(t *testing.T, in Inputs, prop *Proposal) {
	t.Helper()
	posOcc := map[string][]string{}
	delegateAssignments := map[string]int{}
	posByID := map[string]Position{}
	delegateByID := map[string]Delegate{}
	delegationByID := map[string]Delegation{}
	for _, p := range in.Positions {
		posByID[p.ID] = p
	}
	for _, d := range in.Delegates {
		delegateByID[d.ID] = d
	}
	for _, d := range in.Delegations {
		delegationByID[d.ID] = d
	}

	for _, a := range prop.Assignments {
		posOcc[a.PositionID] = append(posOcc[a.PositionID], a.DelegateID)
		delegateAssignments[a.DelegateID]++
	}

	// H1: every delegate has exactly one assignment.
	for _, d := range in.Delegates {
		if delegateAssignments[d.ID] != 1 {
			t.Errorf("H1 violated: delegate %q has %d assignments", d.ID, delegateAssignments[d.ID])
		}
	}
	// H2 & H5: capacity and reserved exclusion.
	for pid, occ := range posOcc {
		p := posByID[pid]
		if p.PrestigeTier == PrestigeReserved {
			t.Errorf("H5 violated: reserved position %q got %d occupants", pid, len(occ))
		}
		if len(occ) > p.MaxDelegates {
			t.Errorf("H2 violated: position %q over capacity (%d > %d)", pid, len(occ), p.MaxDelegates)
		}
	}
	// H3: school cluster cap of 2 per committee.
	schoolCommitteeCount := map[string]map[string]int{}
	for _, a := range prop.Assignments {
		d := delegateByID[a.DelegateID]
		school := delegationByID[d.DelegationID].School
		m := schoolCommitteeCount[school]
		if m == nil {
			m = map[string]int{}
			schoolCommitteeCount[school] = m
		}
		m[a.CommitteeID]++
	}
	for school, m := range schoolCommitteeCount {
		for cid, n := range m {
			if n > 2 {
				t.Errorf("H3 violated: school %q has %d in committee %q", school, n, cid)
			}
		}
	}
	// H4: dual-delegation positions both same school.
	for _, p := range in.Positions {
		if !p.DualDelegation {
			continue
		}
		occ := posOcc[p.ID]
		if len(occ) == 0 {
			continue
		}
		schools := map[string]bool{}
		for _, did := range occ {
			schools[delegationByID[delegateByID[did].DelegationID].School] = true
		}
		if len(schools) > 1 {
			t.Errorf("H4 violated: dual-delegation %q spans %d schools", p.ID, len(schools))
		}
	}
}

func groupBySchool(t *testing.T, in Inputs, _ *Proposal) map[string]string {
	t.Helper()
	delegateByID := map[string]Delegate{}
	for _, d := range in.Delegates {
		delegateByID[d.ID] = d
	}
	delegationByID := map[string]Delegation{}
	for _, d := range in.Delegations {
		delegationByID[d.ID] = d
	}
	out := map[string]string{}
	for did, d := range delegateByID {
		out[did] = delegationByID[d.DelegationID].School
	}
	return out
}

func TestProposeError_Format(t *testing.T) {
	e1 := &ProposeError{Phase: "precheck", Constraint: "H3", Message: "boom"}
	if !strings.Contains(e1.Error(), "H3") || !strings.Contains(e1.Error(), "boom") {
		t.Errorf("error format missing constraint/message: %s", e1.Error())
	}
	e2 := &ProposeError{Phase: "phase-D", Message: "boom"}
	if strings.Contains(e2.Error(), "/") {
		t.Errorf("no-constraint format should omit slash: %s", e2.Error())
	}
}

// Sanity: candidate sort tiebreakers stay deterministic across runs.
func TestPropose_AssignmentsSorted(t *testing.T) {
	in := buildLargeCase(t, "conf-sorted", 8, 5, 2)
	prop, err := Propose(context.Background(), in, defaultOpts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prev := ProposedAssignment{}
	for i, a := range prop.Assignments {
		if i == 0 {
			prev = a
			continue
		}
		if a.CommitteeID < prev.CommitteeID ||
			(a.CommitteeID == prev.CommitteeID && a.PositionID < prev.PositionID) ||
			(a.CommitteeID == prev.CommitteeID && a.PositionID == prev.PositionID && a.DelegateID < prev.DelegateID) {
			t.Errorf("assignments not sorted at index %d: %+v then %+v", i, prev, a)
		}
		prev = a
	}
}

// Sanity: HashInputs returns 64 hex chars (sha256).
func TestHashInputs_Length(t *testing.T) {
	in := buildLargeCase(t, "conf-len", 5, 2, 2)
	h := HashInputs(in)
	if len(h) != 64 {
		t.Errorf("expected 64-char sha256 hex, got %d", len(h))
	}
}

// Sort check for assignment slices (helper used during debugging).
func sortAssignments(a []ProposedAssignment) []ProposedAssignment {
	out := append([]ProposedAssignment(nil), a...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].DelegateID < out[j].DelegateID })
	return out
}
