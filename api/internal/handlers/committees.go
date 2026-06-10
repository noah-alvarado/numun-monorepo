package handlers

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// CommitteeService implements numunv1connect.CommitteeServiceHandler.
// All mutating RPCs are staff-admin only; reads require any authenticated
// caller with scope on the parent conference.
type CommitteeService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Logger *slog.Logger
}

func (s *CommitteeService) ListCommittees(ctx context.Context, req *connect.Request[v1.ListCommitteesRequest]) (*connect.Response[v1.ListCommitteesResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, conferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	rows, err := s.Store.ListCommitteesByConference(ctx, conferenceID)
	if err != nil {
		s.log().Error("ListCommittees: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListCommitteesResponse{Items: make([]*v1.Committee, 0, len(rows))}
	for _, c := range rows {
		out.Items = append(out.Items, committeeToProto(c))
	}
	return connect.NewResponse(out), nil
}

func (s *CommitteeService) GetCommittee(ctx context.Context, req *connect.Request[v1.GetCommitteeRequest]) (*connect.Response[v1.GetCommitteeResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetCommitteeId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("committee_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnCommittee(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	c, err := s.Store.FindCommitteeByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetCommittee: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetCommitteeResponse{Committee: committeeToProto(c)}), nil
}

func (s *CommitteeService) CreateCommittee(ctx context.Context, req *connect.Request[v1.CreateCommitteeRequest]) (*connect.Response[v1.CreateCommitteeResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	ct, ok := domainCommitteeType(req.Msg.GetType())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("type required"))
	}
	cs, ok := domainCommitteeSize(req.Msg.GetSize())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("size required"))
	}
	c := domain.Committee{
		ConferenceID:       conferenceID,
		Name:               strings.TrimSpace(req.Msg.GetName()),
		Type:               ct,
		Size:               cs,
		BackgroundGuideRef: strings.TrimSpace(req.Msg.GetBackgroundGuideRef()),
		CreatedBy:          caller.UserID,
		UpdatedBy:          caller.UserID,
	}
	created, err := s.Store.CreateCommittee(ctx, c)
	if err != nil {
		s.log().Error("CreateCommittee: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventCommitteeCreated,
		Metadata: map[string]string{
			"committeeId":  created.ID,
			"conferenceId": conferenceID,
		},
	})
	return connect.NewResponse(&v1.CreateCommitteeResponse{Committee: committeeToProto(created)}), nil
}

func (s *CommitteeService) UpdateCommittee(ctx context.Context, req *connect.Request[v1.UpdateCommitteeRequest]) (*connect.Response[v1.UpdateCommitteeResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetCommitteeId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("committee_id required"))
	}
	existing, err := s.Store.FindCommitteeByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdateCommittee: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	patch := store.UpdateCommitteePatch{UpdatedBy: caller.UserID}
	if v := req.Msg.Name; v != nil {
		val := strings.TrimSpace(*v)
		patch.Name = &val
	}
	if v := req.Msg.Type; v != nil {
		if t, ok := domainCommitteeType(*v); ok {
			patch.Type = &t
		}
	}
	if v := req.Msg.Size; v != nil {
		if cs, ok := domainCommitteeSize(*v); ok {
			patch.Size = &cs
		}
	}
	if v := req.Msg.BackgroundGuideRef; v != nil {
		val := strings.TrimSpace(*v)
		patch.BackgroundGuideRef = &val
	}
	updated, err := s.Store.UpdateCommittee(ctx, existing.ConferenceID, id, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdateCommittee: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventCommitteeUpdated,
		Metadata:    map[string]string{"committeeId": id, "conferenceId": existing.ConferenceID},
	})
	return connect.NewResponse(&v1.UpdateCommitteeResponse{Committee: committeeToProto(updated)}), nil
}

func (s *CommitteeService) DeleteCommittee(ctx context.Context, req *connect.Request[v1.DeleteCommitteeRequest]) (*connect.Response[v1.DeleteCommitteeResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetCommitteeId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("committee_id required"))
	}
	existing, err := s.Store.FindCommitteeByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("DeleteCommittee: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Store.SoftDeleteCommittee(ctx, existing.ConferenceID, id, int(req.Msg.GetExpectedVersion()), caller.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("DeleteCommittee: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventCommitteeDeleted,
		Metadata:    map[string]string{"committeeId": id, "conferenceId": existing.ConferenceID},
	})
	return connect.NewResponse(&v1.DeleteCommitteeResponse{}), nil
}

func (s *CommitteeService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

func (s *CommitteeService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// ── PositionService ──────────────────────────────────────────────────────────

// PositionService implements numunv1connect.PositionServiceHandler. Position
// CRUD is staff-admin only; reads require scope on the parent committee.
type PositionService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Logger *slog.Logger
}

func (s *PositionService) ListPositions(ctx context.Context, req *connect.Request[v1.ListPositionsRequest]) (*connect.Response[v1.ListPositionsResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	committeeID := strings.TrimSpace(req.Msg.GetCommitteeId())
	if committeeID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("committee_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnCommittee(ctx, committeeID); err != nil {
		return nil, mapScopeErr(err)
	}
	rows, err := s.Store.ListPositionsByCommittee(ctx, committeeID)
	if err != nil {
		s.log().Error("ListPositions: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListPositionsResponse{Items: make([]*v1.Position, 0, len(rows))}
	for _, p := range rows {
		out.Items = append(out.Items, positionToProto(p))
	}
	return connect.NewResponse(out), nil
}

func (s *PositionService) GetPosition(ctx context.Context, req *connect.Request[v1.GetPositionRequest]) (*connect.Response[v1.GetPositionResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetPositionId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("position_id required"))
	}
	p, err := s.Store.FindPositionByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetPosition: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Scoper.MustHaveScopeOnCommittee(ctx, p.CommitteeID); err != nil {
		return nil, mapScopeErr(err)
	}
	return connect.NewResponse(&v1.GetPositionResponse{Position: positionToProto(p)}), nil
}

func (s *PositionService) CreatePosition(ctx context.Context, req *connect.Request[v1.CreatePositionRequest]) (*connect.Response[v1.CreatePositionResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	committeeID := strings.TrimSpace(req.Msg.GetCommitteeId())
	if committeeID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("committee_id required"))
	}
	parent, err := s.Store.FindCommitteeByID(ctx, committeeID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("CreatePosition: load committee", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	maxDelegates := int(req.Msg.GetMaxDelegates())
	dualDelegation := req.Msg.GetDualDelegation()
	if dualDelegation && maxDelegates != 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dual_delegation requires max_delegates == 2"))
	}
	p := domain.Position{
		ConferenceID:   parent.ConferenceID,
		CommitteeID:    committeeID,
		Name:           strings.TrimSpace(req.Msg.GetName()),
		MaxDelegates:   maxDelegates,
		DualDelegation: dualDelegation,
		PrestigeTier:   domainPrestigeTier(req.Msg.GetPrestigeTier()),
		CreatedBy:      caller.UserID,
		UpdatedBy:      caller.UserID,
	}
	created, err := s.Store.CreatePosition(ctx, p)
	if err != nil {
		s.log().Error("CreatePosition: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventPositionCreated,
		Metadata: map[string]string{
			"positionId":  created.ID,
			"committeeId": committeeID,
		},
	})
	return connect.NewResponse(&v1.CreatePositionResponse{Position: positionToProto(created)}), nil
}

func (s *PositionService) UpdatePosition(ctx context.Context, req *connect.Request[v1.UpdatePositionRequest]) (*connect.Response[v1.UpdatePositionResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetPositionId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("position_id required"))
	}
	existing, err := s.Store.FindPositionByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdatePosition: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	patch := store.UpdatePositionPatch{UpdatedBy: caller.UserID}
	if v := req.Msg.Name; v != nil {
		val := strings.TrimSpace(*v)
		patch.Name = &val
	}
	if v := req.Msg.MaxDelegates; v != nil {
		val := int(*v)
		patch.MaxDelegates = &val
	}
	if v := req.Msg.DualDelegation; v != nil {
		patch.DualDelegation = v
	}
	if v := req.Msg.PrestigeTier; v != nil {
		val := domainPrestigeTier(*v)
		patch.PrestigeTier = &val
	}
	// Enforce dual_delegation ⇒ max_delegates == 2 against the merged shape.
	merged := existing
	if patch.MaxDelegates != nil {
		merged.MaxDelegates = *patch.MaxDelegates
	}
	if patch.DualDelegation != nil {
		merged.DualDelegation = *patch.DualDelegation
	}
	if merged.DualDelegation && merged.MaxDelegates != 2 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("dual_delegation requires max_delegates == 2"))
	}
	updated, err := s.Store.UpdatePosition(ctx, existing.CommitteeID, id, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdatePosition: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventPositionUpdated,
		Metadata:    map[string]string{"positionId": id, "committeeId": existing.CommitteeID},
	})
	return connect.NewResponse(&v1.UpdatePositionResponse{Position: positionToProto(updated)}), nil
}

func (s *PositionService) DeletePosition(ctx context.Context, req *connect.Request[v1.DeletePositionRequest]) (*connect.Response[v1.DeletePositionResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetPositionId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("position_id required"))
	}
	existing, err := s.Store.FindPositionByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("DeletePosition: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Store.SoftDeletePosition(ctx, existing.CommitteeID, id, int(req.Msg.GetExpectedVersion()), caller.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("DeletePosition: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventPositionDeleted,
		Metadata:    map[string]string{"positionId": id, "committeeId": existing.CommitteeID},
	})
	return connect.NewResponse(&v1.DeletePositionResponse{}), nil
}

func (s *PositionService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

func (s *PositionService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
