package store_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/smithy-go/middleware"

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
}

// proposalFixture is a conference with one committee and two positions.
type proposalFixture struct {
	conferenceID, committeeID, pos1, pos2 string
}

func newProposalFixture(t *testing.T, c *store.Client) proposalFixture {
	t.Helper()
	ctx := context.Background()
	f := proposalFixture{conferenceID: newID(t)}
	committee, err := c.CreateCommittee(ctx, domain.Committee{
		ConferenceID: f.conferenceID,
		Name:         "ReplaceTest",
		Type:         domain.CommitteeTypeNonCrisis,
		Size:         domain.CommitteeSizeMedium,
	})
	if err != nil {
		t.Fatalf("create committee: %v", err)
	}
	f.committeeID = committee.ID
	for _, dst := range []*string{&f.pos1, &f.pos2} {
		p, err := c.CreatePosition(ctx, domain.Position{ConferenceID: f.conferenceID, CommitteeID: committee.ID, Name: newID(t)})
		if err != nil {
			t.Fatalf("create position: %v", err)
		}
		*dst = p.ID
	}
	return f
}

func (f proposalFixture) row(positionID, delegateID string) domain.Assignment {
	return domain.Assignment{
		ConferenceID: f.conferenceID,
		CommitteeID:  f.committeeID,
		PositionID:   positionID,
		DelegateID:   delegateID,
		DelegationID: "delegation-" + delegateID,
	}
}

// proposedByKey lists the conference's proposed rows keyed by position|delegate.
func proposedByKey(t *testing.T, c *store.Client, conferenceID string) map[string]domain.Assignment {
	t.Helper()
	rows, err := c.ListAllAssignmentsByConference(context.Background(), conferenceID, "", "", domain.AssignmentStatusProposed)
	if err != nil {
		t.Fatalf("list proposed: %v", err)
	}
	out := make(map[string]domain.Assignment, len(rows))
	for _, a := range rows {
		out[a.PositionID+"|"+a.DelegateID] = a
	}
	return out
}

// failNthTransact returns a copy of c whose nth TransactWriteItems call fails
// before reaching DynamoDB.
func failNthTransact(c *store.Client, n int32) *store.Client {
	var calls atomic.Int32
	ddb := dynamodb.New(c.DDB.Options(), func(o *dynamodb.Options) {
		o.APIOptions = append(o.APIOptions, func(s *middleware.Stack) error {
			return s.Initialize.Add(middleware.InitializeMiddlewareFunc("failNthTransact",
				func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (middleware.InitializeOutput, middleware.Metadata, error) {
					if awsmiddleware.GetOperationName(ctx) == "TransactWriteItems" && calls.Add(1) == n {
						return middleware.InitializeOutput{}, middleware.Metadata{}, errors.New("injected failure")
					}
					return next.HandleInitialize(ctx, in)
				}), middleware.After)
		})
	})
	return &store.Client{DDB: ddb, Table: c.Table}
}

func TestReplaceProposedAssignments(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)
	f := newProposalFixture(t, c)
	kept, stale, approved, tombstoned, fresh := newID(t), newID(t), newID(t), newID(t), newID(t)

	if err := c.WriteAssignmentBatch(ctx, "run-1", []domain.Assignment{
		f.row(f.pos1, kept), f.row(f.pos1, stale), f.row(f.pos2, approved), f.row(f.pos2, tombstoned),
	}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := c.ApproveAssignment(ctx, f.pos2, approved, 1, "actor"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := c.SoftDeleteAssignment(ctx, f.pos2, tombstoned, 1, "actor"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	err := c.ReplaceProposedAssignments(ctx, f.conferenceID, "run-2", []domain.Assignment{
		f.row(f.pos1, kept), f.row(f.pos2, tombstoned), f.row(f.pos1, fresh),
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	got := proposedByKey(t, c, f.conferenceID)
	for _, key := range []string{f.pos1 + "|" + kept, f.pos2 + "|" + tombstoned, f.pos1 + "|" + fresh} {
		if got[key].RunID != "run-2" {
			t.Errorf("%s: want run-2 proposal, got %+v", key, got[key])
		}
	}
	if len(got) != 3 {
		t.Errorf("want 3 proposed rows, got %d: %+v", len(got), got)
	}
	if a, err := c.GetAssignment(ctx, f.pos2, approved); err != nil || a.Status != domain.AssignmentStatusApproved {
		t.Errorf("approved row disturbed: %+v, %v", a, err)
	}

	// A new row may never overwrite an approved one.
	err = c.ReplaceProposedAssignments(ctx, f.conferenceID, "run-3", []domain.Assignment{f.row(f.pos2, approved)})
	if err == nil {
		t.Fatal("replace over an approved row: want error")
	}
	if after := proposedByKey(t, c, f.conferenceID); len(after) != 3 || after[f.pos1+"|"+kept].RunID != "run-2" {
		t.Errorf("run-2 proposals not kept after rejected run: %+v", after)
	}
}

func TestReplaceProposedAssignmentsRollsBack(t *testing.T) {
	cases := []struct {
		name     string
		newRows  int   // 30 rows → two put transactions, then one delete transaction
		failCall int32 // which TransactWriteItems call fails
	}{
		{name: "second write chunk fails", newRows: 30, failCall: 2},
		{name: "discarding stale rows fails", newRows: 3, failCall: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			c := testClient(t)
			f := newProposalFixture(t, c)
			overwritten, stale := newID(t), newID(t)

			if err := c.WriteAssignmentBatch(ctx, "run-1", []domain.Assignment{
				f.row(f.pos1, overwritten), f.row(f.pos1, stale),
			}, nil); err != nil {
				t.Fatalf("seed: %v", err)
			}
			before := proposedByKey(t, c, f.conferenceID)

			rows := []domain.Assignment{f.row(f.pos1, overwritten)}
			for len(rows) < tc.newRows {
				rows = append(rows, f.row(f.pos2, newID(t)))
			}
			err := failNthTransact(c, tc.failCall).ReplaceProposedAssignments(ctx, f.conferenceID, "run-2", rows)
			if err == nil {
				t.Fatal("want error from injected failure")
			}
			if errors.Is(err, store.ErrProposalRollbackIncomplete) {
				t.Fatalf("rollback should have completed: %v", err)
			}

			after := proposedByKey(t, c, f.conferenceID)
			if len(after) != len(before) {
				t.Fatalf("want the %d prior proposals back, got %d: %+v", len(before), len(after), after)
			}
			for key, prev := range before {
				if after[key].ID != prev.ID || after[key].RunID != "run-1" {
					t.Errorf("%s: want prior row %+v, got %+v", key, prev, after[key])
				}
			}
		})
	}
}

func TestMoveAssignments(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)
	f := newProposalFixture(t, c)
	ada, grace := newID(t), newID(t)

	if err := c.WriteAssignmentBatch(ctx, "run-1", []domain.Assignment{f.row(f.pos1, ada), f.row(f.pos2, grace)}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	live := func() map[string]domain.Assignment { return proposedByKey(t, c, f.conferenceID) }
	swap := func() error {
		cur := live()
		var a, b domain.Assignment
		for _, r := range cur {
			if r.DelegateID == ada {
				a = r
			} else {
				b = r
			}
		}
		_, err := c.MoveAssignments(ctx, []domain.Assignment{a, b}, []domain.Assignment{
			f.row(a.PositionID, b.DelegateID), f.row(b.PositionID, a.DelegateID),
		}, "actor")
		return err
	}

	// Swapping back re-creates rows at keys that now hold tombstones.
	for round := range 2 {
		if err := swap(); err != nil {
			t.Fatalf("swap %d: %v", round, err)
		}
	}
	got := live()
	if len(got) != 2 || got[f.pos1+"|"+ada].ID == "" || got[f.pos2+"|"+grace].ID == "" {
		t.Fatalf("after swapping back: %+v", got)
	}

	// A stale version aborts the whole move.
	stale := got[f.pos1+"|"+ada]
	stale.Version--
	if _, err := c.MoveAssignments(ctx, []domain.Assignment{stale}, []domain.Assignment{f.row(f.pos2, ada)}, "actor"); !errors.Is(err, store.ErrVersionMismatch) {
		t.Fatalf("stale move: want ErrVersionMismatch, got %v", err)
	}
	// So does a target key holding a live row.
	if _, err := c.MoveAssignments(ctx, []domain.Assignment{got[f.pos1+"|"+ada]}, []domain.Assignment{f.row(f.pos2, grace)}, "actor"); !errors.Is(err, store.ErrAlreadyExists) {
		t.Fatalf("move onto live row: want ErrAlreadyExists, got %v", err)
	}
	if after := live(); len(after) != 2 || after[f.pos1+"|"+ada].ID != got[f.pos1+"|"+ada].ID {
		t.Fatalf("failed moves changed rows: %+v", after)
	}
}
