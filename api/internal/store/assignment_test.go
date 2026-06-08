package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func TestAssignmentCRUDAndApproval(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	committeeID := newID(t)
	positionID := newID(t)
	delegateID := newID(t)
	delegationID := newID(t)

	in := domain.Assignment{
		ConferenceID: conferenceID,
		PositionID:   positionID,
		DelegateID:   delegateID,
		CommitteeID:  committeeID,
		DelegationID: delegationID,
		Score:        3.5,
		Reason:       "best fit",
	}
	created, err := c.CreateAssignment(ctx, in)
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if created.Status != domain.AssignmentStatusProposed {
		t.Fatalf("default status: %s", created.Status)
	}

	got, err := c.GetAssignment(ctx, positionID, delegateID)
	if err != nil {
		t.Fatalf("get assignment: %v", err)
	}
	if got.Score != 3.5 {
		t.Fatalf("score round-trip: %v", got.Score)
	}

	found, err := c.FindAssignmentByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if found.PositionID != positionID {
		t.Fatalf("find by id mismatch")
	}

	// GSI1 reverse lookup by delegate.
	byDelegate, err := c.FindAssignmentByDelegate(ctx, delegateID)
	if err != nil {
		t.Fatalf("find by delegate (GSI1): %v", err)
	}
	if byDelegate.ID != created.ID {
		t.Fatalf("gsi1 lookup returned wrong row: %+v", byDelegate)
	}

	// Approve.
	actor := newID(t)
	approved, err := c.ApproveAssignment(ctx, positionID, delegateID, 1, actor)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != domain.AssignmentStatusApproved || approved.ApprovedBy != actor {
		t.Fatalf("after approve: %+v", approved)
	}

	// Stale approve mismatches version.
	if _, err := c.ApproveAssignment(ctx, positionID, delegateID, 1, actor); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale approve: want ErrVersionMismatch, got %v", err)
	}

	// Unapprove flips back.
	unapproved, err := c.UnapproveAssignment(ctx, positionID, delegateID, 2, actor)
	if err != nil {
		t.Fatalf("unapprove: %v", err)
	}
	if unapproved.Status != domain.AssignmentStatusProposed || !unapproved.ApprovedAt.IsZero() {
		t.Fatalf("after unapprove: %+v", unapproved)
	}

	// Soft delete.
	if err := c.SoftDeleteAssignment(ctx, positionID, delegateID, 3, actor); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := c.GetAssignment(ctx, positionID, delegateID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestWriteAssignmentBatchAndDeleteProposed(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	committee := domain.Committee{
		ConferenceID: conferenceID,
		Name:         "BatchTest",
		Type:         domain.CommitteeTypeNonCrisis,
		Size:         domain.CommitteeSizeMedium,
	}
	committee, err := c.CreateCommittee(ctx, committee)
	if err != nil {
		t.Fatalf("create committee: %v", err)
	}

	position := domain.Position{
		ConferenceID: conferenceID,
		CommitteeID:  committee.ID,
		Name:         "Position1",
	}
	position, err = c.CreatePosition(ctx, position)
	if err != nil {
		t.Fatalf("create position: %v", err)
	}

	runID := newID(t)
	d1 := newID(t)
	d2 := newID(t)
	toCreate := []domain.Assignment{
		{
			ConferenceID: conferenceID,
			PositionID:   position.ID,
			CommitteeID:  committee.ID,
			DelegationID: newID(t),
			DelegateID:   d1,
		},
		{
			ConferenceID: conferenceID,
			PositionID:   position.ID,
			CommitteeID:  committee.ID,
			DelegationID: newID(t),
			DelegateID:   d2,
		},
	}
	if err := c.WriteAssignmentBatch(ctx, runID, toCreate, nil); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	all, err := c.ListAllAssignmentsByConference(ctx, conferenceID, "", "", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(all))
	}

	// Filter by status.
	proposed, err := c.ListAllAssignmentsByConference(ctx, conferenceID, "", "", domain.AssignmentStatusProposed)
	if err != nil {
		t.Fatalf("list filter proposed: %v", err)
	}
	if len(proposed) != 2 {
		t.Fatalf("expected 2 proposed, got %d", len(proposed))
	}

	// Delete all proposed clears the batch.
	if err := c.DeleteAllProposedAssignmentsForConference(ctx, conferenceID); err != nil {
		t.Fatalf("delete proposed: %v", err)
	}
	remaining, err := c.ListAllAssignmentsByConference(ctx, conferenceID, "", "", "")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining, got %d", len(remaining))
	}
}
