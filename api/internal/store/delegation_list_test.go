package store_test

import (
	"context"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// Callers that need every delegation (exports, the assignment algorithm,
// ListAllDelegations) must get all of them, not just the first page.
func TestListAllDelegationsInConferenceFollowsCursor(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)
	defer store.SetListAllPageSize(2)()
	conferenceID := newID(t)
	for i := range 5 {
		status := domain.DelegationStatusApproved
		if i%2 == 1 {
			status = domain.DelegationStatusPending
		}
		if _, err := c.CreateDelegation(ctx, domain.Delegation{ConferenceID: conferenceID, School: "Paging School", Status: status}); err != nil {
			t.Fatalf("create delegation: %v", err)
		}
	}

	all, err := c.ListAllDelegationsInConference(ctx, conferenceID, "")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := c.ListAllDelegationsInConference(ctx, conferenceID, domain.DelegationStatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 || len(approved) != 3 {
		t.Fatalf("got %d delegations (%d approved), want 5 (3) across pages", len(all), len(approved))
	}
}
