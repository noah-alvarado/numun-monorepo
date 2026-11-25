package handlers_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

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
