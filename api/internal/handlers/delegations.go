package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// portalBase resolves the portal URL used to build links in notification
// emails. Mirrors email.Config.PortalBaseURL without dragging the email
// Service into pure-data helpers. Returns "" if the env var is unset —
// callers should treat that as "no link" rather than baking a prod apex.
// email.Config.Validate enforces non-empty at boot in prod / test.
func portalBase() string {
	return os.Getenv("PORTAL_BASE_URL")
}

// DelegationService implements numunv1connect.DelegationServiceHandler.
type DelegationService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Email  email.Service // optional; nil-safe — handlers no-op email side-effects
	Logger *slog.Logger
}

// ListDelegations within a conference. Filterable by status. Scope:
//   - admin → all
//   - advisor → only their own delegations
//   - staff-staffer → delegations they oversee (case a). Committee-derived
//     scope (case c) is deferred to M7.
func (s *DelegationService) ListDelegations(ctx context.Context, req *connect.Request[v1.ListDelegationsRequest]) (*connect.Response[v1.ListDelegationsResponse], error) {
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
	cursor, size := pageRequest(req.Msg.GetPage())

	var dels []domain.Delegation
	var next string
	var err error
	if status, ok := domainDelegationStatus(req.Msg.GetStatus()); ok && status != "" {
		dels, next, err = s.Store.ListDelegationsByStatus(ctx, conferenceID, status, cursor, size)
	} else {
		dels, next, err = s.Store.ListDelegationsByConference(ctx, conferenceID, cursor, size)
	}
	if err != nil {
		s.log().Error("ListDelegations: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	// Scope-filter the result set in-app rather than at the store.
	dels = filterDelegationsForCaller(ctx, s.Scoper, caller, dels)

	out := &v1.ListDelegationsResponse{
		Items: make([]*v1.Delegation, 0, len(dels)),
		Page:  &v1.Page{NextCursor: next, PageSize: size},
	}
	for _, d := range dels {
		out.Items = append(out.Items, delegationToProto(d))
	}
	return connect.NewResponse(out), nil
}

// ListAllDelegations — non-paginated variant. Same scope filter.
func (s *DelegationService) ListAllDelegations(ctx context.Context, req *connect.Request[v1.ListAllDelegationsRequest]) (*connect.Response[v1.ListAllDelegationsResponse], error) {
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
	var dels []domain.Delegation
	var err error
	if status, ok := domainDelegationStatus(req.Msg.GetStatus()); ok && status != "" {
		dels, _, err = s.Store.ListDelegationsByStatus(ctx, conferenceID, status, "", 1000)
	} else {
		dels, _, err = s.Store.ListDelegationsByConference(ctx, conferenceID, "", 1000)
	}
	if err != nil {
		s.log().Error("ListAllDelegations: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if len(dels) >= 1000 {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("set exceeds non-paginated cap; use ListDelegations"))
	}
	dels = filterDelegationsForCaller(ctx, s.Scoper, caller, dels)
	out := &v1.ListAllDelegationsResponse{Items: make([]*v1.Delegation, 0, len(dels))}
	for _, d := range dels {
		out.Items = append(out.Items, delegationToProto(d))
	}
	return connect.NewResponse(out), nil
}

// GetDelegation — scope-gated.
func (s *DelegationService) GetDelegation(ctx context.Context, req *connect.Request[v1.GetDelegationRequest]) (*connect.Response[v1.GetDelegationResponse], error) {
	id := strings.TrimSpace(req.Msg.GetDelegationId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	d, err := s.Store.FindDelegationByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetDelegation: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetDelegationResponse{Delegation: delegationToProto(d)}), nil
}

// CreateDelegation — advisors create their own (auto-attached as lead);
// admins may create on behalf and the caller is *not* attached.
func (s *DelegationService) CreateDelegation(ctx context.Context, req *connect.Request[v1.CreateDelegationRequest]) (*connect.Response[v1.CreateDelegationResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	if caller.Role == domain.RoleStaffStaffer {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("staffers cannot create delegations"))
	}
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, conferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	school := strings.TrimSpace(req.Msg.GetSchool())
	if school == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("school required"))
	}
	if _, err := s.Store.GetConference(ctx, conferenceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("conference not found"))
		}
		s.log().Error("CreateDelegation: load conference", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	in := domain.Delegation{
		ConferenceID:         conferenceID,
		School:               school,
		Address:              domainAddress(req.Msg.GetAddress()),
		EstimatedDelegates:   domainEstimated(req.Msg.GetEstimatedDelegates()),
		CommitteePreferences: domainPrefs(req.Msg.GetCommitteePreferences()),
		Status:               domain.DelegationStatusPending,
		CreatedBy:            caller.UserID,
	}

	if caller.Role == domain.RoleAdvisor {
		// Self-service create: bind the advisor as LEAD atomically with the
		// delegation insert.
		created, _, err := s.Store.CreateDelegationWithLead(ctx, in, caller.UserID, caller.UserID)
		if err != nil {
			s.log().Error("CreateDelegation: txn", "err", err)
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
		}
		s.scheduleNewRegistrationSummary(ctx, conferenceID)
		return connect.NewResponse(&v1.CreateDelegationResponse{Delegation: delegationToProto(created)}), nil
	}

	// Admin path: create empty; the at-least-one-advisor invariant is
	// enforced at Approve time.
	created, err := s.Store.CreateDelegation(ctx, in)
	if err != nil {
		s.log().Error("CreateDelegation: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.scheduleNewRegistrationSummary(ctx, conferenceID)
	return connect.NewResponse(&v1.CreateDelegationResponse{Delegation: delegationToProto(created)}), nil
}

// scheduleNewRegistrationSummary opens a 15-minute dedupe window on first
// registration of the window; subsequent calls within the window are no-ops.
// EMAIL.md §7.1.
func (s *DelegationService) scheduleNewRegistrationSummary(ctx context.Context, conferenceID string) {
	if s.Email == nil {
		return
	}
	const window = 15 * time.Minute
	acquired, windowStart, err := s.Store.AcquireNotificationDedupe(ctx,
		domain.NotificationDedupeNewRegistration, conferenceID, window)
	if err != nil {
		s.log().Warn("schedule registration summary: dedupe", "err", err)
		return
	}
	if !acquired {
		return
	}
	if err := s.Email.Enqueue(ctx, email.EnqueueRequest{
		Kind:            domain.EmailKindNewRegistrationSummary,
		ConferenceID:    conferenceID,
		WindowStartedAt: windowStart,
	}, window); err != nil {
		s.log().Warn("schedule registration summary: enqueue", "err", err)
	}
}

// UpdateDelegation — scope-gated.
func (s *DelegationService) UpdateDelegation(ctx context.Context, req *connect.Request[v1.UpdateDelegationRequest]) (*connect.Response[v1.UpdateDelegationResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetDelegationId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	d, err := s.Store.FindDelegationByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdateDelegation: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	patch := store.UpdateDelegationPatch{UpdatedBy: caller.UserID}
	if req.Msg.School != nil {
		v := strings.TrimSpace(*req.Msg.School)
		patch.School = &v
	}
	if req.Msg.Address != nil {
		a := domainAddress(req.Msg.GetAddress())
		patch.Address = &a
	}
	if req.Msg.EstimatedDelegates != nil {
		e := domainEstimated(req.Msg.GetEstimatedDelegates())
		patch.EstimatedDelegates = &e
	}
	if req.Msg.CommitteePreferences != nil {
		p := domainPrefs(req.Msg.GetCommitteePreferences())
		patch.CommitteePreferences = &p
	}
	updated, err := s.Store.UpdateDelegation(ctx, d.ConferenceID, id, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdateDelegation: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.UpdateDelegationResponse{Delegation: delegationToProto(updated)}), nil
}

// Approve — admin only. Refuses if the delegation has zero advisors
// (at-least-one-advisor invariant).
func (s *DelegationService) Approve(ctx context.Context, req *connect.Request[v1.ApproveRequest]) (*connect.Response[v1.ApproveResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetDelegationId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	d, err := s.Store.FindDelegationByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("Approve: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if d.Status == domain.DelegationStatusApproved {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("already approved"))
	}
	advisors, err := s.Store.ListAdvisorsByDelegation(ctx, id)
	if err != nil {
		s.log().Error("Approve: list advisors", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if len(advisors) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("delegation has no advisors; add at least one before approving"))
	}
	if !hasLead(advisors) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("delegation has no lead advisor"))
	}

	updated, err := s.Store.SetDelegationStatus(ctx, d.ConferenceID, id, int(req.Msg.GetExpectedVersion()), domain.DelegationStatusApproved, caller.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("Approve: set status", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventDelegationApproved,
		Metadata:    map[string]string{"delegationId": id, "conferenceId": d.ConferenceID},
	})
	conferenceName := d.ConferenceID
	if conf, err := s.Store.GetConference(ctx, d.ConferenceID); err == nil {
		conferenceName = conf.Name
	}
	s.notifyAdvisors(ctx, id, domain.EmailKindDelegationApproved, map[string]any{
		"delegationName": d.School,
		"conferenceName": conferenceName,
		"portalLink":     portalBase() + "/delegation",
	})
	return connect.NewResponse(&v1.ApproveResponse{Delegation: delegationToProto(updated)}), nil
}

// Reject — admin only.
func (s *DelegationService) Reject(ctx context.Context, req *connect.Request[v1.RejectRequest]) (*connect.Response[v1.RejectResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetDelegationId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	d, err := s.Store.FindDelegationByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("Reject: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if d.Status == domain.DelegationStatusRejected {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("already rejected"))
	}
	updated, err := s.Store.SetDelegationStatus(ctx, d.ConferenceID, id, int(req.Msg.GetExpectedVersion()), domain.DelegationStatusRejected, caller.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("Reject: set status", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	meta := map[string]string{"delegationId": id, "conferenceId": d.ConferenceID}
	if r := strings.TrimSpace(req.Msg.GetReason()); r != "" {
		meta["reason"] = r
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventDelegationRejected,
		Metadata:    meta,
	})
	conferenceName := d.ConferenceID
	if conf, err := s.Store.GetConference(ctx, d.ConferenceID); err == nil {
		conferenceName = conf.Name
	}
	vars := map[string]any{
		"delegationName": d.School,
		"conferenceName": conferenceName,
		"reason":         "",
	}
	if r := meta["reason"]; r != "" {
		vars["reason"] = r
	}
	s.notifyAdvisors(ctx, id, domain.EmailKindDelegationRejected, vars)
	return connect.NewResponse(&v1.RejectResponse{Delegation: delegationToProto(updated)}), nil
}

// AddAdvisor — lead-of-own delegation or admin. Per the project decision
// (memory: advisor lead rule), multiple LEADs are allowed; adding a LEAD never
// fails on uniqueness.
func (s *DelegationService) AddAdvisor(ctx context.Context, req *connect.Request[v1.AddAdvisorRequest]) (*connect.Response[v1.AddAdvisorResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	userID := strings.TrimSpace(req.Msg.GetUserId())
	if delID == "" || userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id and user_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	// Only the lead-of-own-delegation or admin may add advisors.
	if caller.Role == domain.RoleAdvisor {
		me, err := s.Store.GetAdvisor(ctx, delID, caller.UserID)
		if err != nil || me.Role != domain.AdvisorRoleLead {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only a lead advisor may add advisors"))
		}
	} else if caller.Role != domain.RoleStaffAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not permitted"))
	}

	d, err := s.Store.FindDelegationByID(ctx, delID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("AddAdvisor: load delegation", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	role, _ := domainAdvisorRole(req.Msg.GetRole())
	if role == "" {
		role = domain.AdvisorRoleSecondary
	}
	out, err := s.Store.AddAdvisor(ctx, domain.DelegationAdvisor{
		UserID:       userID,
		DelegationID: delID,
		ConferenceID: d.ConferenceID,
		Role:         role,
		CreatedBy:    caller.UserID,
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("advisor already linked"))
		}
		s.log().Error("AddAdvisor: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      userID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventScopeGranted,
		Metadata: map[string]string{
			"resource":     "delegation",
			"delegationId": delID,
			"role":         string(role),
		},
	})
	return connect.NewResponse(&v1.AddAdvisorResponse{Advisor: advisorToProto(out)}), nil
}

// RemoveAdvisor — lead-of-own delegation or admin. Refuses if removing the
// row would violate "at least one advisor" or "at least one lead".
func (s *DelegationService) RemoveAdvisor(ctx context.Context, req *connect.Request[v1.RemoveAdvisorRequest]) (*connect.Response[v1.RemoveAdvisorResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	userID := strings.TrimSpace(req.Msg.GetUserId())
	if delID == "" || userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id and user_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	if caller.Role == domain.RoleAdvisor {
		me, err := s.Store.GetAdvisor(ctx, delID, caller.UserID)
		if err != nil || me.Role != domain.AdvisorRoleLead {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only a lead advisor may remove advisors"))
		}
	} else if caller.Role != domain.RoleStaffAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not permitted"))
	}

	advisors, err := s.Store.ListAdvisorsByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("RemoveAdvisor: list", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	target, found := findAdvisor(advisors, userID)
	if !found {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	if len(advisors) == 1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("cannot remove the only advisor"))
	}
	if target.Role == domain.AdvisorRoleLead && countLeads(advisors) == 1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("promote another advisor to lead before removing the last lead"))
	}

	if err := s.Store.SoftDeleteAdvisor(ctx, delID, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("RemoveAdvisor: delete", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      userID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventScopeRevoked,
		Metadata: map[string]string{
			"resource":     "delegation",
			"delegationId": delID,
		},
	})
	return connect.NewResponse(&v1.RemoveAdvisorResponse{}), nil
}

// SetAdvisorRole — promotes/demotes a single advisor. Refuses to demote the
// only LEAD; a demotion that would leave zero leads must be preceded by
// promoting someone else.
func (s *DelegationService) SetAdvisorRole(ctx context.Context, req *connect.Request[v1.SetAdvisorRoleRequest]) (*connect.Response[v1.SetAdvisorRoleResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	userID := strings.TrimSpace(req.Msg.GetUserId())
	if delID == "" || userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id and user_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	if caller.Role == domain.RoleAdvisor {
		me, err := s.Store.GetAdvisor(ctx, delID, caller.UserID)
		if err != nil || me.Role != domain.AdvisorRoleLead {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only a lead advisor may change advisor roles"))
		}
	} else if caller.Role != domain.RoleStaffAdmin {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not permitted"))
	}
	role, ok := domainAdvisorRole(req.Msg.GetRole())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("role required"))
	}

	advisors, err := s.Store.ListAdvisorsByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("SetAdvisorRole: list", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	target, found := findAdvisor(advisors, userID)
	if !found {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	if target.Role == domain.AdvisorRoleLead && role == domain.AdvisorRoleSecondary && countLeads(advisors) == 1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("promote another advisor to lead before demoting the last lead"))
	}

	updated, err := s.Store.SetAdvisorRole(ctx, delID, userID, int(req.Msg.GetExpectedVersion()), role, caller.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("SetAdvisorRole: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.SetAdvisorRoleResponse{Advisor: advisorToProto(updated)}), nil
}

// ListAdvisors — scope-gated; non-paginated under the hood (advisors per
// delegation are bounded). The PageRequest envelope is accepted but `cursor`
// is currently a no-op.
func (s *DelegationService) ListAdvisors(ctx context.Context, req *connect.Request[v1.ListAdvisorsRequest]) (*connect.Response[v1.ListAdvisorsResponse], error) {
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	advisors, err := s.Store.ListAdvisorsByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("ListAdvisors: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAdvisorsResponse{
		Items: make([]*v1.DelegationAdvisor, 0, len(advisors)),
		Page:  &v1.Page{NextCursor: "", PageSize: int32(len(advisors))},
	}
	for _, a := range advisors {
		out.Items = append(out.Items, advisorToProto(a))
	}
	return connect.NewResponse(out), nil
}

// ListAllAdvisors — non-paginated.
func (s *DelegationService) ListAllAdvisors(ctx context.Context, req *connect.Request[v1.ListAllAdvisorsRequest]) (*connect.Response[v1.ListAllAdvisorsResponse], error) {
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	advisors, err := s.Store.ListAdvisorsByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("ListAllAdvisors: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAllAdvisorsResponse{Items: make([]*v1.DelegationAdvisor, 0, len(advisors))}
	for _, a := range advisors {
		out.Items = append(out.Items, advisorToProto(a))
	}
	return connect.NewResponse(out), nil
}

// AssignStaffer — admin only.
func (s *DelegationService) AssignStaffer(ctx context.Context, req *connect.Request[v1.AssignStafferRequest]) (*connect.Response[v1.AssignStafferResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	userID := strings.TrimSpace(req.Msg.GetUserId())
	if delID == "" || userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id and user_id required"))
	}
	d, err := s.Store.FindDelegationByID(ctx, delID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("AssignStaffer: load delegation", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Store.AssignStaffer(ctx, domain.StaffDelegationAssignment{
		UserID:       userID,
		DelegationID: delID,
		ConferenceID: d.ConferenceID,
		CreatedBy:    caller.UserID,
	}); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("already assigned"))
		}
		s.log().Error("AssignStaffer: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      userID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventScopeGranted,
		Metadata: map[string]string{
			"resource":     "delegation-oversight",
			"delegationId": delID,
		},
	})
	s.notifyScopeChange(ctx, userID, caller.UserID, fmt.Sprintf("You were assigned to oversee delegation %s.", d.School))
	return connect.NewResponse(&v1.AssignStafferResponse{}), nil
}

// UnassignStaffer — admin only.
func (s *DelegationService) UnassignStaffer(ctx context.Context, req *connect.Request[v1.UnassignStafferRequest]) (*connect.Response[v1.UnassignStafferResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	userID := strings.TrimSpace(req.Msg.GetUserId())
	if delID == "" || userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id and user_id required"))
	}
	if err := s.Store.UnassignStaffer(ctx, delID, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UnassignStaffer: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      userID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventScopeRevoked,
		Metadata: map[string]string{
			"resource":     "delegation-oversight",
			"delegationId": delID,
		},
	})
	s.notifyScopeChange(ctx, userID, caller.UserID, "Your delegation-oversight assignment was removed.")
	return connect.NewResponse(&v1.UnassignStafferResponse{}), nil
}

// audit is a best-effort write — logged but never failed to the user.
func (s *DelegationService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

// notifyScopeChange sends a T6 scope/role-change notification to the affected
// user. Synchronous, best-effort.
func (s *DelegationService) notifyScopeChange(ctx context.Context, affectedUserID, actorUserID, summary string) {
	if s.Email == nil {
		return
	}
	user, err := s.Store.GetUser(ctx, affectedUserID)
	if err != nil {
		return
	}
	actorName := actorUserID
	if actor, err := s.Store.GetUser(ctx, actorUserID); err == nil && actor.Name != "" {
		actorName = actor.Name
	}
	if err := s.Email.Send(ctx, email.SendRequest{
		User: user,
		Kind: domain.EmailKindScopeRoleChanged,
		Vars: map[string]any{
			"actorName":     actorName,
			"changeSummary": summary,
		},
	}); err != nil {
		s.log().Warn("notify scope change: send", "err", err)
	}
}

// notifyAdvisors fans the given email kind out to every advisor of the
// delegation. Synchronous (EMAIL.md §5.1); a per-recipient SES failure is
// logged but does not roll back the originating mutation.
func (s *DelegationService) notifyAdvisors(ctx context.Context, delegationID string, kind domain.EmailKind, vars map[string]any) {
	if s.Email == nil {
		return
	}
	advisors, err := s.Store.ListAdvisorsByDelegation(ctx, delegationID)
	if err != nil {
		s.log().Warn("notify: list advisors", "err", err)
		return
	}
	for _, a := range advisors {
		user, err := s.Store.GetUser(ctx, a.UserID)
		if err != nil {
			continue
		}
		if err := s.Email.Send(ctx, email.SendRequest{
			User: user,
			Kind: kind,
			Vars: vars,
		}); err != nil {
			s.log().Warn("notify: send", "kind", kind, "userId", user.ID, "err", err)
		}
	}
}

func (s *DelegationService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// ── helpers ──────────────────────────────────────────────────────────────────

func hasLead(advisors []domain.DelegationAdvisor) bool {
	for _, a := range advisors {
		if a.Role == domain.AdvisorRoleLead {
			return true
		}
	}
	return false
}

func countLeads(advisors []domain.DelegationAdvisor) int {
	n := 0
	for _, a := range advisors {
		if a.Role == domain.AdvisorRoleLead {
			n++
		}
	}
	return n
}

func findAdvisor(advisors []domain.DelegationAdvisor, userID string) (domain.DelegationAdvisor, bool) {
	for _, a := range advisors {
		if a.UserID == userID {
			return a, true
		}
	}
	return domain.DelegationAdvisor{}, false
}

// filterDelegationsForCaller drops rows the caller has no scope on. Admin
// passes through; advisors keep only their advisor-linked delegations;
// staff-staffer keeps oversight (case a). Case (c) deferred to M7.
func filterDelegationsForCaller(ctx context.Context, scoper *auth.Scoper, caller auth.Caller, dels []domain.Delegation) []domain.Delegation {
	if caller.Role == domain.RoleStaffAdmin {
		return dels
	}
	out := make([]domain.Delegation, 0, len(dels))
	for _, d := range dels {
		if err := scoper.MustHaveScopeOnDelegation(ctx, d.ID); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// ensure import usage when fmt is unused after edits.
var _ = fmt.Sprintf
