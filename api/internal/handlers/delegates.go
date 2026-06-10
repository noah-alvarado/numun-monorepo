package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// DelegateService implements numunv1connect.DelegateServiceHandler. Bulk
// import RPCs live in delegates_bulk.go; CRUD + CheckIn live here.
type DelegateService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Email  email.Service
	Logger *slog.Logger
}

// ListDelegates within a delegation. Scope: any role with scope on the
// parent delegation.
func (s *DelegateService) ListDelegates(ctx context.Context, req *connect.Request[v1.ListDelegatesRequest]) (*connect.Response[v1.ListDelegatesResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	cursor, size := pageRequest(req.Msg.GetPage())
	rows, next, err := s.Store.ListDelegatesByDelegation(ctx, delID, cursor, size)
	if err != nil {
		s.log().Error("ListDelegates: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListDelegatesResponse{
		Items: make([]*v1.Delegate, 0, len(rows)),
		Page:  &v1.Page{NextCursor: next, PageSize: size},
	}
	for _, d := range rows {
		out.Items = append(out.Items, delegateToProto(d))
	}
	return connect.NewResponse(out), nil
}

// ListAllDelegates — non-paginated variant for callers that need the whole roster
// (assignment algorithm, bulk-import match computation).
func (s *DelegateService) ListAllDelegates(ctx context.Context, req *connect.Request[v1.ListAllDelegatesRequest]) (*connect.Response[v1.ListAllDelegatesResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	rows, err := s.Store.ListAllDelegatesByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("ListAllDelegates: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAllDelegatesResponse{Items: make([]*v1.Delegate, 0, len(rows))}
	for _, d := range rows {
		out.Items = append(out.Items, delegateToProto(d))
	}
	return connect.NewResponse(out), nil
}

func (s *DelegateService) GetDelegate(ctx context.Context, req *connect.Request[v1.GetDelegateRequest]) (*connect.Response[v1.GetDelegateResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetDelegateId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegate_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegate(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	d, err := s.Store.FindDelegateByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetDelegate: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetDelegateResponse{Delegate: delegateToProto(d)}), nil
}

func (s *DelegateService) CreateDelegate(ctx context.Context, req *connect.Request[v1.CreateDelegateRequest]) (*connect.Response[v1.CreateDelegateResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	parent, err := s.Store.FindDelegationByID(ctx, delID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("CreateDelegate: load delegation", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	in := req.Msg.GetInput()
	first := strings.TrimSpace(in.GetFirstName())
	last := strings.TrimSpace(in.GetLastName())
	if first == "" || last == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("first_name and last_name required"))
	}
	d := domain.Delegate{
		ConferenceID:    parent.ConferenceID,
		DelegationID:    delID,
		FirstName:       first,
		LastName:        last,
		Email:           strings.TrimSpace(strings.ToLower(in.GetEmail())),
		ExperienceLevel: domainExperienceLevel(in.GetExperienceLevel()),
		CreatedBy:       caller.UserID,
		UpdatedBy:       caller.UserID,
	}
	created, err := s.Store.CreateDelegate(ctx, d)
	if err != nil {
		s.log().Error("CreateDelegate: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventDelegateCreated,
		Metadata: map[string]string{
			"delegateId":   created.ID,
			"delegationId": delID,
			"conferenceId": created.ConferenceID,
		},
	})
	return connect.NewResponse(&v1.CreateDelegateResponse{Delegate: delegateToProto(created)}), nil
}

func (s *DelegateService) UpdateDelegate(ctx context.Context, req *connect.Request[v1.UpdateDelegateRequest]) (*connect.Response[v1.UpdateDelegateResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetDelegateId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegate_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegate(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	existing, err := s.Store.FindDelegateByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdateDelegate: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	patch := store.UpdateDelegatePatch{}
	if v := req.Msg.FirstName; v != nil {
		val := strings.TrimSpace(*v)
		patch.FirstName = &val
	}
	if v := req.Msg.LastName; v != nil {
		val := strings.TrimSpace(*v)
		patch.LastName = &val
	}
	if v := req.Msg.Email; v != nil {
		val := strings.TrimSpace(strings.ToLower(*v))
		patch.Email = &val
	}
	if v := req.Msg.ExperienceLevel; v != nil {
		val := domainExperienceLevel(*v)
		patch.ExperienceLevel = &val
	}
	updated, err := s.Store.UpdateDelegate(ctx, existing.DelegationID, id, patch, int(req.Msg.GetExpectedVersion()), caller.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdateDelegate: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventDelegateUpdated,
		Metadata: map[string]string{
			"delegateId":   id,
			"delegationId": existing.DelegationID,
		},
	})
	return connect.NewResponse(&v1.UpdateDelegateResponse{Delegate: delegateToProto(updated)}), nil
}

func (s *DelegateService) DeleteDelegate(ctx context.Context, req *connect.Request[v1.DeleteDelegateRequest]) (*connect.Response[v1.DeleteDelegateResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetDelegateId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegate_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegate(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	existing, err := s.Store.FindDelegateByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("DeleteDelegate: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Store.SoftDeleteDelegate(ctx, existing.DelegationID, id, int(req.Msg.GetExpectedVersion()), caller.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("DeleteDelegate: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventDelegateDeleted,
		Metadata: map[string]string{
			"delegateId":   id,
			"delegationId": existing.DelegationID,
		},
	})
	return connect.NewResponse(&v1.DeleteDelegateResponse{}), nil
}

func (s *DelegateService) CheckIn(ctx context.Context, req *connect.Request[v1.CheckInRequest]) (*connect.Response[v1.CheckInResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetDelegateId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegate_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegate(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	existing, err := s.Store.FindDelegateByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("CheckIn: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	updated, err := s.Store.CheckInDelegate(ctx, existing.DelegationID, id, caller.UserID)
	if err != nil {
		s.log().Error("CheckIn: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventDelegateCheckedIn,
		Metadata: map[string]string{
			"delegateId":   id,
			"delegationId": existing.DelegationID,
		},
	})
	return connect.NewResponse(&v1.CheckInResponse{Delegate: delegateToProto(updated)}), nil
}

// SearchDelegates returns delegates whose first/last name (case-insensitive)
// contains the query string, conference-wide. Results are filtered to the
// caller's scope: advisors see only delegates of delegations they advise;
// staff-staffers see their direct-oversight delegations plus any committee-
// linked delegations (case c, resolved via assignment → committee membership).
// Staff-admins see everything in the conference.
func (s *DelegateService) SearchDelegates(ctx context.Context, req *connect.Request[v1.SearchDelegatesRequest]) (*connect.Response[v1.SearchDelegatesResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, conferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	query := strings.TrimSpace(req.Msg.GetQuery())
	if len(query) < 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("query must be at least 2 characters"))
	}
	limit := int(req.Msg.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rows, truncated, err := s.Store.SearchDelegatesByConference(ctx, conferenceID, query, limit)
	if err != nil {
		s.log().Error("SearchDelegates: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	// Scope-filter results. Admin short-circuits to "all"; otherwise check each
	// row's delegation. Cache the per-delegation decision to avoid N scope
	// resolutions for delegates from the same delegation.
	allowed := map[string]bool{}
	denied := map[string]bool{}
	delegations := map[string]domain.Delegation{}
	out := &v1.SearchDelegatesResponse{
		Items:     make([]*v1.SearchDelegatesResponse_Hit, 0, len(rows)),
		Truncated: truncated,
	}
	for _, d := range rows {
		if caller.Role != domain.RoleStaffAdmin {
			if denied[d.DelegationID] {
				continue
			}
			if !allowed[d.DelegationID] {
				if err := s.Scoper.MustHaveScopeOnDelegation(ctx, d.DelegationID); err != nil {
					denied[d.DelegationID] = true
					continue
				}
				allowed[d.DelegationID] = true
			}
		}
		parent, ok := delegations[d.DelegationID]
		if !ok {
			loaded, err := s.Store.FindDelegationByID(ctx, d.DelegationID)
			if err == nil {
				delegations[d.DelegationID] = loaded
				parent = loaded
			}
		}
		out.Items = append(out.Items, &v1.SearchDelegatesResponse_Hit{
			Delegate:         delegateToProto(d),
			DelegationSchool: parent.School,
		})
	}
	return connect.NewResponse(out), nil
}

func (s *DelegateService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

// notifyBulkImportCommitted sends T4 to the advisor who committed the import.
// Best-effort.
func (s *DelegateService) notifyBulkImportCommitted(ctx context.Context, advisorUserID, delegationID string, mode domain.UpsertMode, creates, updates, softDeletes int) {
	if s.Email == nil {
		return
	}
	user, err := s.Store.GetUser(ctx, advisorUserID)
	if err != nil {
		return
	}
	d, err := s.Store.FindDelegationByID(ctx, delegationID)
	delegationName := delegationID
	if err == nil {
		delegationName = d.School
	}
	if err := s.Email.Send(ctx, email.SendRequest{
		User: user,
		Kind: domain.EmailKindBulkImportCommitted,
		Vars: map[string]any{
			"delegationName":  delegationName,
			"createCount":     creates,
			"updateCount":     updates,
			"softDeleteCount": softDeletes,
			"mode":            string(mode),
		},
	}); err != nil {
		s.log().Warn("bulk import notify: send", "err", err)
	}
}

func (s *DelegateService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// fmtVersion is a small helper used by the bulk-import code path; declared here
// to keep delegates_bulk.go focused on flow rather than formatting.
func fmtVersion(v int) string { return fmt.Sprintf("%d", v) }
