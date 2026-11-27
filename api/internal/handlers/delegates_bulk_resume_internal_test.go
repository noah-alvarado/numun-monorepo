package handlers

import (
	"context"
	"os"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// A large full-sync import whose second batch fails, then resumes: the
// result must be exactly the committed rows, with nothing duplicated, lost or
// soft-deleted by mistake.
func TestBulkImportResumesAfterPartialFailure(t *testing.T) {
	if os.Getenv("AWS_ENDPOINT_URL_DYNAMODB") == "" {
		t.Skip("set AWS_ENDPOINT_URL_DYNAMODB to run DDB integration tests")
	}
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("AWS_ACCESS_KEY_ID", "local")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "local")
	st, err := store.New(context.Background())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	bg := context.Background()
	id := func() string { return uuid.Must(uuid.NewV7()).String() }

	advisor, err := st.CreateUser(bg, domain.User{ID: id(), Role: domain.RoleAdvisor, Name: "Resume Tester"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	confID := id()
	del, _, err := st.CreateDelegationWithLead(bg, domain.Delegation{ConferenceID: confID, School: "Resume School"}, advisor.ID, advisor.ID)
	if err != nil {
		t.Fatalf("create delegation: %v", err)
	}
	for _, name := range [][2]string{{"Ada", "Lovelace"}, {"Grace", "Hopper"}} {
		if _, err := st.CreateDelegate(bg, domain.Delegate{ConferenceID: confID, DelegationID: del.ID, FirstName: name[0], LastName: name[1], ExperienceLevel: domain.ExperienceLevelNovice}); err != nil {
			t.Fatalf("create delegate: %v", err)
		}
	}

	// 60 new delegates, Ada promoted, Grace left out (full sync removes her).
	// The 30th new delegate lands in the second batch and fails it once.
	var rows []*v1.DelegateInput
	for i := range 60 {
		last := "Resume"
		if i == 29 {
			last = "Zfail"
		}
		rows = append(rows, &v1.DelegateInput{FirstName: "Delegate" + string(rune('A'+i/26)) + string(rune('a'+i%26)), LastName: last})
	}
	rows = append(rows, &v1.DelegateInput{FirstName: "Ada", LastName: "Lovelace", ExperienceLevel: v1.ExperienceLevel_EXPERIENCE_LEVEL_ADVANCED})

	// The cached upload had a typo the advisor fixed in the preview; resume
	// must use the fixed rows.
	uploaded := []*v1.PreviewRow{{RowNumber: 1, Input: &v1.DelegateInput{FirstName: "Typo", LastName: "Row"}}}
	raw, err := marshalPreviewRows(uploaded)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := st.PutBulkImportPreview(bg, domain.BulkImportPreview{UserID: advisor.ID, DelegationID: del.ID, ConferenceID: confID, ParsedRowsRaw: raw})
	if err != nil {
		t.Fatalf("put preview: %v", err)
	}

	failed := false
	svc := &DelegateService{Store: st, Scoper: auth.NewScoper(st), DevFailBatch: func(names []string) bool {
		for _, n := range names {
			if n == "Zfail" && !failed {
				failed = true
				return true
			}
		}
		return false
	}}
	ctx := auth.WithCaller(bg, auth.Caller{UserID: advisor.ID, Role: domain.RoleAdvisor})

	commit, err := svc.UpsertDelegatesBulk(ctx, connect.NewRequest(&v1.UpsertDelegatesBulkRequest{
		UploadId: preview.ID, DelegationId: del.ID, Mode: v1.UpsertMode_UPSERT_MODE_FULL_SYNC, Rows: rows,
	}))
	if err != nil {
		t.Fatalf("commit returned an error instead of a failed summary: %v", err)
	}
	if s := commit.Msg.Summary; s.Status != "failed" || s.CompletedBatches != 1 || s.TotalBatches != 3 || s.BulkImportJobId == "" {
		t.Fatalf("commit summary = %+v, want failed at batch 2 of 3", s)
	}

	resume, err := svc.ResumeBulkImport(ctx, connect.NewRequest(&v1.ResumeBulkImportRequest{BulkImportJobId: commit.Msg.Summary.BulkImportJobId}))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if s := resume.Msg.Summary; s.Status != "complete" || s.CompletedBatches != s.TotalBatches {
		t.Fatalf("resume summary = %+v, want complete", s)
	}

	roster, err := st.ListAllDelegatesByDelegation(bg, del.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, d := range roster {
		seen[d.FirstName+" "+d.LastName]++
		if d.FirstName == "Ada" && d.ExperienceLevel != domain.ExperienceLevelAdvanced {
			t.Errorf("Ada not updated: %s", d.ExperienceLevel)
		}
	}
	if len(roster) != 61 {
		t.Errorf("roster has %d delegates, want 61", len(roster))
	}
	all, err := st.ListAllDelegatesIncludingDeleted(bg, del.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range all {
		if d.IsDeleted && d.FirstName != "Grace" {
			t.Errorf("%s %s was soft-deleted", d.FirstName, d.LastName)
		}
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("%s created %d times", name, n)
		}
		if strings.HasPrefix(name, "Grace") || strings.HasPrefix(name, "Typo") {
			t.Errorf("%s should not be on the roster", name)
		}
	}
}
