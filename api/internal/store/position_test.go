package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func TestPositionCRUDOptimisticLock(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	committeeID := newID(t)

	in := domain.Position{
		ConferenceID: conferenceID,
		CommitteeID:  committeeID,
		Name:         "France",
		PrestigeTier: domain.PrestigeTierElevated,
	}
	created, err := c.CreatePosition(ctx, in)
	if err != nil {
		t.Fatalf("create position: %v", err)
	}
	if created.MaxDelegates != 1 || created.PrestigeTier != domain.PrestigeTierElevated {
		t.Fatalf("defaults wrong: %+v", created)
	}

	got, err := c.GetPosition(ctx, committeeID, created.ID)
	if err != nil {
		t.Fatalf("get position: %v", err)
	}
	if got.Name != "France" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	found, err := c.FindPositionByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("find position by id: %v", err)
	}
	if found.CommitteeID != committeeID {
		t.Fatalf("find position by id mismatch: %+v", found)
	}

	maxD := 2
	dual := true
	updated, err := c.UpdatePosition(ctx, committeeID, created.ID, 1, store.UpdatePositionPatch{
		MaxDelegates:   &maxD,
		DualDelegation: &dual,
		UpdatedBy:      newID(t),
	})
	if err != nil {
		t.Fatalf("update position: %v", err)
	}
	if updated.MaxDelegates != 2 || !updated.DualDelegation || updated.Version != 2 {
		t.Fatalf("after update: %+v", updated)
	}

	// Stale version triggers ErrVersionMismatch.
	if _, err := c.UpdatePosition(ctx, committeeID, created.ID, 1, store.UpdatePositionPatch{MaxDelegates: &maxD}); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale update: want ErrVersionMismatch, got %v", err)
	}

	list, err := c.ListPositionsByCommittee(ctx, committeeID)
	if err != nil {
		t.Fatalf("list positions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 position, got %d", len(list))
	}

	if err := c.SoftDeletePosition(ctx, committeeID, created.ID, 2, newID(t)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := c.GetPosition(ctx, committeeID, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}
