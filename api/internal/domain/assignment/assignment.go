// Package assignment implements the M7 delegate-to-position assignment
// algorithm: a deterministic random-restart greedy + 2-opt local search
// described in docs/subsystems/ASSIGNMENT_ALGORITHM.md.
//
// The package is framework-free. Callers (Connect handlers) convert their
// protobuf-shaped inputs into the local Inputs struct, invoke Propose, and
// convert the returned Proposal back into protobuf Assignments. Errors are
// returned as *ProposeError so the handler can populate AssignmentRun
// diagnostics.
package assignment

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// CommitteeType is the trinary type axis used by Committees and preferences.
type CommitteeType string

const (
	CommitteeTypeCrisis    CommitteeType = "crisis"
	CommitteeTypeNonCrisis CommitteeType = "non-crisis"
)

// CommitteeSize is the trinary size axis used by Committees and preferences.
type CommitteeSize string

const (
	CommitteeSizeSmall  CommitteeSize = "small"
	CommitteeSizeMedium CommitteeSize = "medium"
	CommitteeSizeLarge  CommitteeSize = "large"
)

// PrestigeTier — Position.prestigeTier; reserved positions are skipped.
type PrestigeTier string

const (
	PrestigeStandard PrestigeTier = "standard"
	PrestigeElevated PrestigeTier = "elevated"
	PrestigeReserved PrestigeTier = "reserved"
)

// ExperienceLevel — Delegate.experienceLevel.
type ExperienceLevel string

const (
	ExperienceNovice       ExperienceLevel = "novice"
	ExperienceIntermediate ExperienceLevel = "intermediate"
	ExperienceAdvanced     ExperienceLevel = "advanced"
)

// Trinary is the preference value used on each axis of CommitteePreferences.
type Trinary string

const (
	TrinaryPositive Trinary = "positive"
	TrinaryNegative Trinary = "negative"
	TrinaryNeutral  Trinary = "neutral"
)

// CommitteePreferences carries the per-axis trinary matrix on a Delegation.
type CommitteePreferences struct {
	TypeCrisis    Trinary
	TypeNonCrisis Trinary
	SizeSmall     Trinary
	SizeMedium    Trinary
	SizeLarge     Trinary
}

// Conference is the scope row; only the id is used by the algorithm itself.
type Conference struct {
	ID string
}

// Delegation is a school's per-conference registration. Only id, school, and
// committeePreferences influence assignment.
type Delegation struct {
	ID                   string
	School               string
	CommitteePreferences CommitteePreferences
}

// Delegate — a student belonging to a Delegation.
type Delegate struct {
	ID              string
	DelegationID    string
	ExperienceLevel ExperienceLevel
}

// Committee — a body that contains positions.
type Committee struct {
	ID   string
	Type CommitteeType
	Size CommitteeSize
}

// Position — a country/role within a Committee.
type Position struct {
	ID             string
	CommitteeID    string
	MaxDelegates   int
	DualDelegation bool
	PrestigeTier   PrestigeTier
}

// PinnedAssignment — an existing approved Assignment that must be preserved.
type PinnedAssignment struct {
	DelegateID   string
	PositionID   string
	CommitteeID  string
	DelegationID string
	Score        float64
	Reason       string
}

// Inputs is the complete set of entities the algorithm consumes for one run.
type Inputs struct {
	Conference        Conference
	Delegations       []Delegation
	Delegates         []Delegate
	Committees        []Committee
	Positions         []Position
	PinnedAssignments []PinnedAssignment
}

// RunOptions controls the deterministic RNG and time/iteration budgets.
type RunOptions struct {
	Seed              uint64
	LocalSearchBudget time.Duration
	BacktrackBudget   int
}

// ProposedAssignment is one delegate placed on one position.
type ProposedAssignment struct {
	DelegateID   string
	PositionID   string
	CommitteeID  string
	DelegationID string
	Score        float64
	Reason       string
}

// Proposal is the algorithm's output for one run.
type Proposal struct {
	Assignments []ProposedAssignment
	Objective   float64
	Diagnostics string
}

// ProposeError is the typed error returned by Propose on infeasibility or any
// hard-constraint violation. Phase identifies where the failure surfaced and
// Constraint names the violated H1..H6 rule when applicable.
type ProposeError struct {
	Phase      string
	Constraint string
	Message    string
}

func (e *ProposeError) Error() string {
	if e.Constraint != "" {
		return fmt.Sprintf("[%s/%s] %s", e.Phase, e.Constraint, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Phase, e.Message)
}

// defaultLocalSearchBudget is the Phase C soft cap when RunOptions does not
// supply one. ASSIGNMENT_ALGORITHM.md §5.4 specifies 15 seconds.
const defaultLocalSearchBudget = 15 * time.Second

// defaultBacktrackBudget bounds Phase B's rebalancing attempts. §5.3 step 3.
const defaultBacktrackBudget = 1000

// Propose runs the full algorithm. It returns *Proposal on success and a
// *ProposeError on infeasibility or any post-condition violation. The error
// path never returns a partial proposal.
func Propose(ctx context.Context, in Inputs, opts RunOptions) (*Proposal, error) {
	if opts.LocalSearchBudget <= 0 {
		opts.LocalSearchBudget = defaultLocalSearchBudget
	}
	if opts.BacktrackBudget <= 0 {
		opts.BacktrackBudget = defaultBacktrackBudget
	}

	rng := rand.New(rand.NewSource(int64(opts.Seed))) //nolint:gosec // deterministic by design

	state, err := newWorkingState(in)
	if err != nil {
		return nil, err
	}

	if err := precheckFeasibility(state); err != nil {
		return nil, err
	}
	if err := seedDualDelegations(state, rng); err != nil {
		return nil, err
	}
	if err := greedyMainPass(state, rng, opts.BacktrackBudget); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(opts.LocalSearchBudget)
	localSearch2Opt(ctx, state, rng, deadline)

	if err := validate(state); err != nil {
		return nil, err
	}

	return buildProposal(state), nil
}
