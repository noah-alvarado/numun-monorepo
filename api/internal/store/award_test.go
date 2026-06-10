package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func TestAwardCRUDOptimisticLock(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	delegateID := newID(t)
	delegationID := newID(t)
	in := domain.Award{
		ConferenceID: conferenceID,
		Name:         "Best Delegate",
		Category:     "individual",
		Recipients: []domain.AwardRecipient{
			{Kind: domain.AwardRecipientKindDelegate, ID: delegateID, DisplayName: "Jane Doe"},
		},
		AwardedBy: newID(t),
	}
	created, err := c.CreateAward(ctx, in)
	if err != nil {
		t.Fatalf("create award: %v", err)
	}
	if created.ID == "" || created.Version != 1 {
		t.Fatalf("unexpected created: %+v", created)
	}
	if len(created.Recipients) != 1 || created.Recipients[0].ID != delegateID {
		t.Fatalf("recipients round-trip: %+v", created.Recipients)
	}

	got, err := c.GetAward(ctx, conferenceID, created.ID)
	if err != nil {
		t.Fatalf("get award: %v", err)
	}
	if got.Name != "Best Delegate" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	found, err := c.FindAwardByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("find award by id: %v", err)
	}
	if found.ConferenceID != conferenceID {
		t.Fatalf("find award by id mismatch: %+v", found)
	}

	// Update: replace recipients with a delegate-pair (two DELEGATE recipients).
	newName := "Best Delegation"
	updated, err := c.UpdateAward(ctx, conferenceID, created.ID, 1, store.UpdateAwardPatch{
		Name: &newName,
		Recipients: []domain.AwardRecipient{
			{Kind: domain.AwardRecipientKindDelegation, ID: delegationID, DisplayName: "Roosevelt HS"},
		},
		RecipientsSet: true,
		UpdatedBy:     newID(t),
	})
	if err != nil {
		t.Fatalf("update award: %v", err)
	}
	if updated.Name != newName || updated.Version != 2 {
		t.Fatalf("after update: %+v", updated)
	}
	if len(updated.Recipients) != 1 || updated.Recipients[0].Kind != domain.AwardRecipientKindDelegation {
		t.Fatalf("recipients after update: %+v", updated.Recipients)
	}

	// Stale version triggers ErrVersionMismatch.
	if _, err := c.UpdateAward(ctx, conferenceID, created.ID, 1, store.UpdateAwardPatch{Name: &newName}); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale update: want ErrVersionMismatch, got %v", err)
	}

	// List by conference returns the award.
	list, err := c.ListAwardsByConference(ctx, conferenceID)
	if err != nil {
		t.Fatalf("list awards: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 award, got %d", len(list))
	}

	// Soft delete then it disappears.
	if err := c.SoftDeleteAward(ctx, conferenceID, created.ID, 2, newID(t)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := c.GetAward(ctx, conferenceID, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestAddDismissedAwardIdempotent(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	u := domain.User{
		ID:    newID(t),
		Role:  domain.RoleAdvisor,
		Email: "adv@example.com",
		Name:  "Adv",
	}
	if _, err := c.CreateUser(ctx, u); err != nil {
		t.Fatalf("create user: %v", err)
	}

	awardA, awardB := newID(t), newID(t)
	got, err := c.AddDismissedAward(ctx, u.ID, awardA)
	if err != nil {
		t.Fatalf("dismiss A: %v", err)
	}
	if len(got.DismissedAwardIDs) != 1 || got.DismissedAwardIDs[0] != awardA {
		t.Fatalf("after first dismiss: %+v", got.DismissedAwardIDs)
	}

	// Second call with the same award is a no-op on the set.
	got, err = c.AddDismissedAward(ctx, u.ID, awardA)
	if err != nil {
		t.Fatalf("dismiss A again: %v", err)
	}
	if len(got.DismissedAwardIDs) != 1 {
		t.Fatalf("idempotency failed: %+v", got.DismissedAwardIDs)
	}

	// Add a second award.
	got, err = c.AddDismissedAward(ctx, u.ID, awardB)
	if err != nil {
		t.Fatalf("dismiss B: %v", err)
	}
	if len(got.DismissedAwardIDs) != 2 {
		t.Fatalf("after second dismiss: %+v", got.DismissedAwardIDs)
	}

	// Missing user returns ErrNotFound.
	if _, err := c.AddDismissedAward(ctx, newID(t), awardA); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing user: want ErrNotFound, got %v", err)
	}
}
