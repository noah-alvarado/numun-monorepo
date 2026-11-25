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
		{RowNumber: 1, Input: &v1.DelegateInput{FirstName: "Alan", LastName: "Turing"}},
		{RowNumber: 2, Input: &v1.DelegateInput{FirstName: "Edsger", LastName: "Dijkstra"}},
	}
	summary := &v1.PreviewSummary{}

	computeRosterMatches(rows, roster, summary)

	if summary.CreateCount != 1 || summary.UpdateCount != 1 || summary.SoftDeleteCount != 1 {
		t.Fatalf("got create=%d update=%d softDelete=%d, want 1/1/1 (Barbara is missing from the file)",
			summary.CreateCount, summary.UpdateCount, summary.SoftDeleteCount)
	}
}
