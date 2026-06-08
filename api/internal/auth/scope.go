package auth

import (
	"context"
	"errors"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// ErrScopeDenied is returned by the scope helpers when the caller has no
// scope on the requested entity. Handlers translate this to not_found per
// AUTH.md §7.3 (anti-enumeration default).
var ErrScopeDenied = errors.New("auth: scope denied")

// ErrUnauthenticated indicates the request reached a scope helper without a
// Caller attached. This is a programming error — the middleware should have
// rejected the request before it landed in a handler.
var ErrUnauthenticated = errors.New("auth: no caller in context")

// Scoper composes resource-level scope checks against the link entities.
// Construct one at startup with a *store.Client and inject it into every
// handler that needs to gate access on a delegation/committee/etc.
//
// Helpers return ErrScopeDenied when the caller has no path to the resource.
// Handlers translate that to connect.CodeNotFound per AUTH.md §7.3 (no
// distinction between "doesn't exist" and "you can't see it").
type Scoper struct {
	Store *store.Client
}

// NewScoper constructs a Scoper. nil-store is permitted only for tests that
// don't traverse data lookups; production code paths must pass a real client.
func NewScoper(s *store.Client) *Scoper {
	return &Scoper{Store: s}
}

// MustHaveScopeOnDelegation gates access to a Delegation id.
//
//   - staff-admin → always pass.
//   - advisor → pass iff a DelegationAdvisor row links the caller to this
//     delegation (any role).
//   - staff-staffer → pass iff a StaffDelegationAssignment row covers this
//     delegation (case a) OR the staffer has a committee whose positions hold
//     at least one assignment to a delegate in this delegation (case c —
//     implemented lazily by the handler; the scope helper handles case (a) and
//     defers case (c) to a follow-up walk that the caller composes when
//     needed).
//
// Case (c) for staff-staffers is intentionally not resolved here because it
// requires querying Assignment rows for delegationId matches — an expensive
// fan-out that we want callers to opt into explicitly. Until M7 lands the
// algorithm + assignments, case (c) returns ErrScopeDenied; case (a) is the
// production path.
func (s *Scoper) MustHaveScopeOnDelegation(ctx context.Context, delegationID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	if s == nil || s.Store == nil {
		return ErrScopeDenied
	}
	switch c.Role {
	case domain.RoleAdvisor:
		_, err := s.Store.GetAdvisor(ctx, delegationID, c.UserID)
		if err == nil {
			return nil
		}
		if errors.Is(err, store.ErrNotFound) {
			return ErrScopeDenied
		}
		return err
	case domain.RoleStaffStaffer:
		_, err := s.Store.GetStaffDelegationAssignment(ctx, delegationID, c.UserID)
		if err == nil {
			return nil
		}
		if errors.Is(err, store.ErrNotFound) {
			// Case (c) — committee-based scope — is deferred to M7 when
			// Assignment rows exist. Until then a staffer with only committee
			// scope cannot reach a delegation through this helper.
			return ErrScopeDenied
		}
		return err
	}
	return ErrScopeDenied
}

// MustHaveScopeOnDelegate gates access to a Delegate id by resolving the
// delegate's parent delegation and delegating to MustHaveScopeOnDelegation.
func (s *Scoper) MustHaveScopeOnDelegate(ctx context.Context, delegateID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	if s == nil || s.Store == nil {
		return ErrScopeDenied
	}
	d, err := s.Store.FindDelegateByID(ctx, delegateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrScopeDenied
		}
		return err
	}
	return s.MustHaveScopeOnDelegation(ctx, d.DelegationID)
}

// MustHaveScopeOnCommittee gates access to a Committee id. M3 wires the
// staffer (c) path via StaffCommitteeAssignment; the committee entity itself
// lands in M7, so until then advisors are denied and staff-staffers pass only
// when an explicit committee assignment exists.
func (s *Scoper) MustHaveScopeOnCommittee(ctx context.Context, committeeID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	if c.Role != domain.RoleStaffStaffer {
		return ErrScopeDenied
	}
	if s == nil || s.Store == nil {
		return ErrScopeDenied
	}
	_, err := s.Store.GetStaffCommitteeAssignment(ctx, committeeID, c.UserID)
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return ErrScopeDenied
	}
	return err
}

// MustHaveScopeOnAssignment — Assignment repo lands in M7. Until then,
// admin-only.
func (s *Scoper) MustHaveScopeOnAssignment(ctx context.Context, _ string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnPayment — Payment repo lands in M8. Until then, admin-only.
func (s *Scoper) MustHaveScopeOnPayment(ctx context.Context, _ string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnConference gates Conference access. Reads on conferences
// are open to any authenticated caller per the role matrix (API.md §9.2);
// admin-only writes are gated by handlers calling MustBeStaffAdmin before
// the mutate.
func (s *Scoper) MustHaveScopeOnConference(ctx context.Context, _ string) error {
	if _, ok := FromContext(ctx); !ok {
		return ErrUnauthenticated
	}
	return nil
}

// MustBeStaffAdmin gates RPCs that are admin-only. Stateless — kept as a
// package-level function so handlers don't need a Scoper for the simple role
// gate.
func MustBeStaffAdmin(ctx context.Context) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role != domain.RoleStaffAdmin {
		return ErrScopeDenied
	}
	return nil
}
