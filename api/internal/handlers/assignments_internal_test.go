package handlers

import (
	"testing"

	"github.com/numun/numun/api/internal/domain/assignment"
)

func TestBuildAssignmentRowsSkipsPinned(t *testing.T) {
	prop := &assignment.Proposal{Assignments: []assignment.ProposedAssignment{
		{DelegateID: "d1", PositionID: "p1"},
		{DelegateID: "d2", PositionID: "p2"},
		{DelegateID: "d3", PositionID: "p1"},
	}}
	pinned := []assignment.PinnedAssignment{{DelegateID: "d1", PositionID: "p1"}}

	rows := buildAssignmentRows(prop, pinned, "conf", "run", "actor")

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (pinned d1→p1 skipped): %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.DelegateID == "d1" {
			t.Fatalf("pinned assignment re-created: %+v", r)
		}
	}
}
