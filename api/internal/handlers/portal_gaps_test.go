package handlers_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/handlers"
	"github.com/numun/numun/api/internal/store"
)

func as(userID string, role domain.Role) context.Context {
	return auth.WithCaller(context.Background(), auth.Caller{UserID: userID, Role: role})
}

func wantCode(t *testing.T, err error, code connect.Code) {
	t.Helper()
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != code {
		t.Fatalf("got err %v, want %s", err, code)
	}
}

func seedUser(t *testing.T, st *store.Client, role domain.Role, email string) domain.User {
	t.Helper()
	u, err := st.CreateUser(context.Background(), domain.User{ID: newUUID(t), Role: role, Email: email, Name: "Test " + string(role)})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func seedDelegation(t *testing.T, st *store.Client, conferenceID, leadUserID string) domain.Delegation {
	t.Helper()
	d, _, err := st.CreateDelegationWithLead(context.Background(), domain.Delegation{
		ConferenceID: conferenceID,
		School:       "Test School " + newUUID(t)[:8],
	}, leadUserID, leadUserID)
	if err != nil {
		t.Fatalf("create delegation: %v", err)
	}
	return d
}

func TestListAssignmentsScopesAdvisorsToOwnApprovedRows(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	svc := &handlers.AssignmentService{Store: st, Scoper: auth.NewScoper(st)}
	confID := newUUID(t)

	advisor := seedUser(t, st, domain.RoleAdvisor, "")
	other := seedUser(t, st, domain.RoleAdvisor, "")
	mine := seedDelegation(t, st, confID, advisor.ID)
	theirs := seedDelegation(t, st, confID, other.ID)

	committee, err := st.CreateCommittee(ctx, domain.Committee{ConferenceID: confID, Name: "Security Council"})
	if err != nil {
		t.Fatalf("create committee: %v", err)
	}
	position, err := st.CreatePosition(ctx, domain.Position{CommitteeID: committee.ID, ConferenceID: confID, Name: "France"})
	if err != nil {
		t.Fatalf("create position: %v", err)
	}
	approved, err := st.CreateAssignment(ctx, domain.Assignment{ConferenceID: confID, CommitteeID: committee.ID, PositionID: position.ID, DelegationID: mine.ID, DelegateID: newUUID(t)})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if _, err := st.ApproveAssignment(ctx, position.ID, approved.DelegateID, approved.Version, "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	proposed, err := st.CreateAssignment(ctx, domain.Assignment{ConferenceID: confID, CommitteeID: committee.ID, PositionID: position.ID, DelegationID: mine.ID, DelegateID: newUUID(t)})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	advCtx := as(advisor.ID, domain.RoleAdvisor)
	_, err = svc.ListAssignments(advCtx, connect.NewRequest(&v1.ListAssignmentsRequest{ConferenceId: confID}))
	wantCode(t, err, connect.CodeInvalidArgument)
	_, err = svc.ListAssignments(advCtx, connect.NewRequest(&v1.ListAssignmentsRequest{ConferenceId: confID, DelegationId: theirs.ID}))
	wantCode(t, err, connect.CodeNotFound)

	resp, err := svc.ListAssignments(advCtx, connect.NewRequest(&v1.ListAssignmentsRequest{ConferenceId: confID, DelegationId: mine.ID}))
	if err != nil {
		t.Fatalf("list own: %v", err)
	}
	if len(resp.Msg.Items) != 1 || resp.Msg.Items[0].Id != approved.ID {
		t.Fatalf("advisor got %d items, want only the approved one", len(resp.Msg.Items))
	}
	if got := resp.Msg.Items[0]; got.CommitteeName != "Security Council" || got.PositionName != "France" {
		t.Fatalf("names = %q/%q", got.CommitteeName, got.PositionName)
	}

	_, err = svc.GetAssignment(advCtx, connect.NewRequest(&v1.GetAssignmentRequest{AssignmentId: proposed.ID}))
	wantCode(t, err, connect.CodeNotFound)

	adminResp, err := svc.ListAssignments(as("admin", domain.RoleStaffAdmin), connect.NewRequest(&v1.ListAssignmentsRequest{ConferenceId: confID}))
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if len(adminResp.Msg.Items) != 2 {
		t.Fatalf("admin got %d items, want 2", len(adminResp.Msg.Items))
	}
}

func TestCheckInIsStaffOnlyAndCanBeUndone(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	svc := &handlers.DelegateService{Store: st, Scoper: auth.NewScoper(st)}
	advisor := seedUser(t, st, domain.RoleAdvisor, "")
	del := seedDelegation(t, st, newUUID(t), advisor.ID)
	delegate, err := st.CreateDelegate(ctx, domain.Delegate{ConferenceID: del.ConferenceID, DelegationID: del.ID, FirstName: "Ada", LastName: "Lovelace", ExperienceLevel: domain.ExperienceLevelNovice})
	if err != nil {
		t.Fatalf("create delegate: %v", err)
	}

	_, err = svc.CheckIn(as(advisor.ID, domain.RoleAdvisor), connect.NewRequest(&v1.CheckInRequest{DelegateId: delegate.ID}))
	wantCode(t, err, connect.CodePermissionDenied)

	admin := as("admin", domain.RoleStaffAdmin)
	in, err := svc.CheckIn(admin, connect.NewRequest(&v1.CheckInRequest{DelegateId: delegate.ID}))
	if err != nil || in.Msg.Delegate.CheckedInAt == nil {
		t.Fatalf("check in: %v", err)
	}
	_, err = svc.UndoCheckIn(as(advisor.ID, domain.RoleAdvisor), connect.NewRequest(&v1.UndoCheckInRequest{DelegateId: delegate.ID}))
	wantCode(t, err, connect.CodePermissionDenied)
	out, err := svc.UndoCheckIn(admin, connect.NewRequest(&v1.UndoCheckInRequest{DelegateId: delegate.ID}))
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if out.Msg.Delegate.CheckedInAt != nil {
		t.Fatalf("checkedInAt still set after undo")
	}
}

func TestRestoreDelegateIsAdminOnly(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	svc := &handlers.DelegateService{Store: st, Scoper: auth.NewScoper(st)}
	advisor := seedUser(t, st, domain.RoleAdvisor, "")
	del := seedDelegation(t, st, newUUID(t), advisor.ID)
	delegate, err := st.CreateDelegate(ctx, domain.Delegate{ConferenceID: del.ConferenceID, DelegationID: del.ID, FirstName: "Alan", LastName: "Turing", ExperienceLevel: domain.ExperienceLevelNovice})
	if err != nil {
		t.Fatalf("create delegate: %v", err)
	}
	if err := st.SoftDeleteDelegate(ctx, del.ID, delegate.ID, delegate.Version, advisor.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	deleted, err := st.ListAllDelegatesIncludingDeleted(ctx, del.ID)
	if err != nil || len(deleted) != 1 || !deleted[0].IsDeleted {
		t.Fatalf("list including deleted: %v %+v", err, deleted)
	}
	req := &v1.RestoreDelegateRequest{DelegationId: del.ID, DelegateId: delegate.ID, ExpectedVersion: int32(deleted[0].Version)}

	_, err = svc.RestoreDelegate(as(advisor.ID, domain.RoleAdvisor), connect.NewRequest(req))
	wantCode(t, err, connect.CodePermissionDenied)
	got, err := svc.RestoreDelegate(as("admin", domain.RoleStaffAdmin), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got.Msg.Delegate.IsDeleted {
		t.Fatalf("still deleted after restore")
	}
	_, err = svc.RestoreDelegate(as("admin", domain.RoleStaffAdmin), connect.NewRequest(req))
	wantCode(t, err, connect.CodeAborted)
}

func TestAdvisorsAddCoAdvisorsByEmail(t *testing.T) {
	st := testStore(t)
	svc := &handlers.DelegationService{Store: st, Scoper: auth.NewScoper(st)}
	lead := seedUser(t, st, domain.RoleAdvisor, "")
	del := seedDelegation(t, st, newUUID(t), lead.ID)
	secondary := seedUser(t, st, domain.RoleAdvisor, "second-"+newUUID(t)+"@example.org")
	third := seedUser(t, st, domain.RoleAdvisor, "third-"+newUUID(t)+"@example.org")
	staffer := seedUser(t, st, domain.RoleStaffStaffer, "staff-"+newUUID(t)+"@example.org")

	leadCtx := as(lead.ID, domain.RoleAdvisor)
	added, err := svc.AddAdvisor(leadCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: secondary.Email}))
	if err != nil {
		t.Fatalf("lead adds by email: %v", err)
	}
	if a := added.Msg.Advisor; a.UserId != secondary.ID || a.Name != secondary.Name || a.Role != v1.DelegationAdvisor_ROLE_SECONDARY {
		t.Fatalf("added = %+v", a)
	}

	_, err = svc.AddAdvisor(leadCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: staffer.Email}))
	wantCode(t, err, connect.CodeNotFound)
	_, err = svc.AddAdvisor(leadCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: "nobody-" + newUUID(t) + "@example.org"}))
	wantCode(t, err, connect.CodeNotFound)

	secCtx := as(secondary.ID, domain.RoleAdvisor)
	_, err = svc.AddAdvisor(secCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: third.Email, Role: v1.DelegationAdvisor_ROLE_LEAD}))
	wantCode(t, err, connect.CodePermissionDenied)
	if _, err := svc.AddAdvisor(secCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: third.Email})); err != nil {
		t.Fatalf("secondary adds secondary: %v", err)
	}

	// Removing leaves a tombstone; adding the same advisor again must work.
	if _, err := svc.RemoveAdvisor(leadCtx, connect.NewRequest(&v1.RemoveAdvisorRequest{DelegationId: del.ID, UserId: third.ID})); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := svc.AddAdvisor(leadCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: third.Email})); err != nil {
		t.Fatalf("re-add after remove: %v", err)
	}
	_, err = svc.AddAdvisor(leadCtx, connect.NewRequest(&v1.AddAdvisorRequest{DelegationId: del.ID, Email: third.Email}))
	wantCode(t, err, connect.CodeAlreadyExists)
}

func TestRejectedDelegationIsReadOnlyForAdvisors(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	delegations := &handlers.DelegationService{Store: st, Scoper: auth.NewScoper(st)}
	delegates := &handlers.DelegateService{Store: st, Scoper: auth.NewScoper(st)}
	advisor := seedUser(t, st, domain.RoleAdvisor, "")
	del := seedDelegation(t, st, newUUID(t), advisor.ID)
	rejected, err := st.SetDelegationStatus(ctx, del.ConferenceID, del.ID, del.Version, domain.DelegationStatusRejected, "admin")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}

	advCtx := as(advisor.ID, domain.RoleAdvisor)
	_, err = delegations.UpdateDelegation(advCtx, connect.NewRequest(&v1.UpdateDelegationRequest{DelegationId: del.ID, School: proto.String("Renamed"), ExpectedVersion: int32(rejected.Version)}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	_, err = delegates.CreateDelegate(advCtx, connect.NewRequest(&v1.CreateDelegateRequest{DelegationId: del.ID, Input: &v1.DelegateInput{FirstName: "Ada", LastName: "Lovelace"}}))
	wantCode(t, err, connect.CodeFailedPrecondition)

	// Staff can still correct a rejected registration.
	if _, err := delegates.CreateDelegate(as("admin", domain.RoleStaffAdmin), connect.NewRequest(&v1.CreateDelegateRequest{DelegationId: del.ID, Input: &v1.DelegateInput{FirstName: "Ada", LastName: "Lovelace"}})); err != nil {
		t.Fatalf("admin create on rejected delegation: %v", err)
	}
}
