package auth

import (
	"context"
	"errors"

	"github.com/numun/numun/api/internal/domain"
)

// ErrScopeDenied is returned by the scope helpers when the caller has no
// scope on the requested entity. Handlers translate this to not_found per
// AUTH.md §7.3 (anti-enumeration default).
var ErrScopeDenied = errors.New("auth: scope denied")

// ErrUnauthenticated indicates the request reached a scope helper without a
// Caller attached. This is a programming error — the middleware should have
// rejected the request before it landed in a handler.
var ErrUnauthenticated = errors.New("auth: no caller in context")

// MustHaveScopeOnDelegation enforces resource-level scope on a Delegation id.
//
// Stub for M2: the link entities (DelegationAdvisor, StaffDelegationAssignment,
// StaffCommitteeAssignment) don't exist yet — they land in M3 with the rest of
// the data layer. Until then this helper applies the role-only gate:
//
//   - staff-admin → pass
//   - everyone else → deny (returns ErrScopeDenied, mapped to not_found)
//
// M3 fills in the real link-table queries.
func MustHaveScopeOnDelegation(ctx context.Context, delegationID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnDelegate enforces scope on a Delegate id. M3 stub — see
// MustHaveScopeOnDelegation.
func MustHaveScopeOnDelegate(ctx context.Context, delegateID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnCommittee enforces scope on a Committee id. M3 stub.
func MustHaveScopeOnCommittee(ctx context.Context, committeeID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnAssignment enforces scope on an Assignment id. M3 stub.
func MustHaveScopeOnAssignment(ctx context.Context, assignmentID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnPayment enforces scope on a Payment id. M3 stub.
func MustHaveScopeOnPayment(ctx context.Context, paymentID string) error {
	c, ok := FromContext(ctx)
	if !ok {
		return ErrUnauthenticated
	}
	if c.Role == domain.RoleStaffAdmin {
		return nil
	}
	return ErrScopeDenied
}

// MustHaveScopeOnConference enforces scope on a Conference id. M3 stub.
// Reads on active conferences are wide-open per the role matrix; the helper
// itself only enforces the write-side gate, so for v1.M2 we permit all roles.
// M3 will tighten this to "writes require staff-admin; reads allowed for all".
func MustHaveScopeOnConference(ctx context.Context, conferenceID string) error {
	if _, ok := FromContext(ctx); !ok {
		return ErrUnauthenticated
	}
	return nil
}

// MustBeStaffAdmin gates RPCs that are admin-only by the role matrix.
// Returned error is mapped to permission_denied by handlers (the caller is
// authenticated and knows the RPC exists; the privilege check is what fails).
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
