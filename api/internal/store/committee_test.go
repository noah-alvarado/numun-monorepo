package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func TestCommitteeCRUDOptimisticLock(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	in := domain.Committee{
		ConferenceID:       conferenceID,
		Name:               "UNSC",
		Type:               domain.CommitteeTypeCrisis,
		Size:               domain.CommitteeSizeSmall,
		BackgroundGuideRef: "/content/background-guides/unsc.md",
	}
	created, err := c.CreateCommittee(ctx, in)
	if err != nil {
		t.Fatalf("create committee: %v", err)
	}
	if created.ID == "" || created.Version != 1 {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := c.GetCommittee(ctx, conferenceID, created.ID)
	if err != nil {
		t.Fatalf("get committee: %v", err)
	}
	if got.Name != "UNSC" || got.Type != domain.CommitteeTypeCrisis {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	found, err := c.FindCommitteeByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("find committee by id: %v", err)
	}
	if found.ConferenceID != conferenceID {
		t.Fatalf("find committee by id mismatch: %+v", found)
	}

	newName := "UN Security Council"
	updated, err := c.UpdateCommittee(ctx, conferenceID, created.ID, 1, store.UpdateCommitteePatch{
		Name:      &newName,
		UpdatedBy: newID(t),
	})
	if err != nil {
		t.Fatalf("update committee: %v", err)
	}
	if updated.Name != newName || updated.Version != 2 {
		t.Fatalf("after update: %+v", updated)
	}

	// Stale version triggers ErrVersionMismatch.
	if _, err := c.UpdateCommittee(ctx, conferenceID, created.ID, 1, store.UpdateCommitteePatch{Name: &newName}); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale update: want ErrVersionMismatch, got %v", err)
	}

	// List by conference returns the committee.
	list, err := c.ListCommitteesByConference(ctx, conferenceID)
	if err != nil {
		t.Fatalf("list committees: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 committee, got %d", len(list))
	}

	// Soft delete then it disappears.
	if err := c.SoftDeleteCommittee(ctx, conferenceID, created.ID, 2, newID(t)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := c.GetCommittee(ctx, conferenceID, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}
