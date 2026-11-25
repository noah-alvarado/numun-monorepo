package handlers

import (
	"testing"

	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
)

func TestComputeRosterMatchesCountsFullSyncSoftDeletes(t *testing.T) {
	roster := []domain.Delegate{
		{ID: "alan", FirstName: "Alan", LastName: "Turing"},
		{ID: "barbara", FirstName: "Barbara", LastName: "Liskov"},
	}
	rows := []*v1.PreviewRow{
		{RowNumber: 1, Input: &v1.DelegateInput{FirstName: "Alan", LastName: "Turing", ExperienceLevel: v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED}},
		{RowNumber: 2, Input: &v1.DelegateInput{FirstName: "Edsger", LastName: "Dijkstra"}},
	}
	summary := &v1.PreviewSummary{}

	computeRosterMatches(rows, roster, summary)

	if summary.CreateCount != 1 || summary.UpdateCount != 1 || summary.SoftDeleteCount != 1 {
		t.Fatalf("got create=%d update=%d softDelete=%d, want 1/1/1 (Barbara is missing from the file)",
			summary.CreateCount, summary.UpdateCount, summary.SoftDeleteCount)
	}
}

func TestUnchangedRowsAreCountedButNotWritten(t *testing.T) {
	roster := []domain.Delegate{
		{ID: "alan", FirstName: "Alan", LastName: "Turing", ExperienceLevel: domain.ExperienceLevelIntermediate, Version: 1},
		{ID: "grace", FirstName: "Grace", LastName: "Hopper", ExperienceLevel: domain.ExperienceLevelIntermediate, Version: 1},
	}
	rows := []*v1.PreviewRow{
		{RowNumber: 1, Input: &v1.DelegateInput{FirstName: "Alan", LastName: "Turing"}},
		{RowNumber: 2, Input: &v1.DelegateInput{FirstName: "Grace", LastName: "Hopper", ExperienceLevel: v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED}},
	}
	summary := &v1.PreviewSummary{}

	computeRosterMatches(rows, roster, summary)
	if summary.UnchangedCount != 1 || summary.UpdateCount != 1 || summary.SoftDeleteCount != 0 {
		t.Fatalf("got unchanged=%d update=%d softDelete=%d, want 1/1/0",
			summary.UnchangedCount, summary.UpdateCount, summary.SoftDeleteCount)
	}

	creates, updates, softDeletes := planWrites(rows, roster, domain.UpsertModeFullSync, "actor", "conf", "del")
	if len(creates) != 0 || len(softDeletes) != 0 {
		t.Fatalf("got %d creates, %d soft-deletes; an unchanged row must still count as present", len(creates), len(softDeletes))
	}
	if len(updates) != 1 || updates[0].ID != "grace" {
		t.Fatalf("got updates %+v, want only grace", updates)
	}
}
