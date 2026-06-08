package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

func TestAssignmentRunLifecycle(t *testing.T) {
	ctx := context.Background()
	c := testClient(t)

	conferenceID := newID(t)
	ordinal, err := c.NextRunOrdinal(ctx, conferenceID)
	if err != nil {
		t.Fatalf("next ordinal: %v", err)
	}
	if ordinal != 1 {
		t.Fatalf("expected ordinal=1, got %d", ordinal)
	}

	actor := newID(t)
	run := domain.AssignmentRun{
		ConferenceID: conferenceID,
		Seed:         42,
		RunOrdinal:   ordinal,
		IsCanonical:  true,
		TriggeredBy:  actor,
		Status:       domain.AssignmentRunStatusRunning,
		InputsHash:   "sha256:abc",
	}
	created, err := c.CreateAssignmentRun(ctx, run)
	if err != nil {
		t.Fatalf("create assignment run: %v", err)
	}
	if created.Status != domain.AssignmentRunStatusRunning {
		t.Fatalf("status: %s", created.Status)
	}

	// A second run while one is in-flight is rejected.
	second := run
	second.RunOrdinal = 2
	if _, err := c.CreateAssignmentRun(ctx, second); !errors.Is(err, store.ErrAlgorithmAlreadyRunning) {
		t.Fatalf("second run: want ErrAlgorithmAlreadyRunning, got %v", err)
	}

	// In-flight lookup finds the running row via GSI2.
	inflight, err := c.FindInFlightRun(ctx, conferenceID)
	if err != nil {
		t.Fatalf("find in-flight: %v", err)
	}
	if inflight.ID != created.ID {
		t.Fatalf("in-flight returned wrong run: %s vs %s", inflight.ID, created.ID)
	}

	// Get by id.
	got, err := c.GetAssignmentRun(ctx, created.ID)
	if err != nil {
		t.Fatalf("get assignment run: %v", err)
	}
	if got.Seed != 42 {
		t.Fatalf("seed mismatch: %d", got.Seed)
	}

	// Mark done — GSI2 in-flight query should now return ErrNotFound.
	completed, err := c.UpdateAssignmentRunStatus(ctx, created.ID, domain.AssignmentRunStatusDone, 12.5, 7, "", time.Now().UTC())
	if err != nil {
		t.Fatalf("update status: %v", err)
	}
	if completed.Status != domain.AssignmentRunStatusDone || completed.AssignmentCount != 7 {
		t.Fatalf("after complete: %+v", completed)
	}
	if _, err := c.FindInFlightRun(ctx, conferenceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("in-flight after done: want ErrNotFound, got %v", err)
	}

	// Now a new run can start.
	second.RunOrdinal = 2
	if _, err := c.CreateAssignmentRun(ctx, second); err != nil {
		t.Fatalf("second run after done: %v", err)
	}

	// Next ordinal returns 3 (max=2).
	ordinal, err = c.NextRunOrdinal(ctx, conferenceID)
	if err != nil {
		t.Fatalf("next ordinal: %v", err)
	}
	if ordinal != 3 {
		t.Fatalf("expected ordinal=3, got %d", ordinal)
	}

	// List returns both runs newest-first.
	runs, _, err := c.ListAssignmentRunsByConference(ctx, conferenceID, "", 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
}
