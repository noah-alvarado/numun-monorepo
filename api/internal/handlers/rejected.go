package handlers

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// advisorMayEdit refuses advisor writes to a rejected delegation (its
// details, delegates, imports and co-advisors): a rejected registration is
// read-only for the school. Staff can still edit it. Call after the scope
// check.
func advisorMayEdit(ctx context.Context, st *store.Client, delegationID string) error {
	caller, _ := auth.FromContext(ctx)
	if caller.Role != domain.RoleAdvisor {
		return nil
	}
	d, err := st.FindDelegationByID(ctx, delegationID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		return connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if d.Status == domain.DelegationStatusRejected {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("this registration wasn't approved, so it can't be changed; contact NUMUN staff"))
	}
	return nil
}
