package handlers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// ConferenceService implements numunv1connect.ConferenceServiceHandler.
type ConferenceService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Logger *slog.Logger
}

// ListConferences — any authenticated caller. Filterable by status.
func (s *ConferenceService) ListConferences(ctx context.Context, req *connect.Request[v1.ListConferencesRequest]) (*connect.Response[v1.ListConferencesResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	var status domain.ConferenceStatus
	if d, ok := domainConferenceStatus(req.Msg.GetStatus()); ok {
		status = d
	}
	page := req.Msg.GetPage()
	cursor, size := pageRequest(page)
	confs, next, err := s.Store.ListConferences(ctx, status, cursor, size)
	if err != nil {
		s.log().Error("ListConferences: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListConferencesResponse{
		Items: make([]*v1.Conference, 0, len(confs)),
		Page:  &v1.Page{NextCursor: next, PageSize: size},
	}
	for _, c := range confs {
		out.Items = append(out.Items, conferenceToProto(c))
	}
	return connect.NewResponse(out), nil
}

// ListAllConferences — capped non-paginated variant. 1000-item ceiling per
// API.md §4.3.
func (s *ConferenceService) ListAllConferences(ctx context.Context, req *connect.Request[v1.ListAllConferencesRequest]) (*connect.Response[v1.ListAllConferencesResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	var status domain.ConferenceStatus
	if d, ok := domainConferenceStatus(req.Msg.GetStatus()); ok {
		status = d
	}
	confs, _, err := s.Store.ListConferences(ctx, status, "", 1000)
	if err != nil {
		s.log().Error("ListAllConferences: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if len(confs) >= 1000 {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("set exceeds non-paginated cap; use ListConferences"))
	}
	out := &v1.ListAllConferencesResponse{Items: make([]*v1.Conference, 0, len(confs))}
	for _, c := range confs {
		out.Items = append(out.Items, conferenceToProto(c))
	}
	return connect.NewResponse(out), nil
}

// GetConference — any authenticated caller (scope is open per API.md §9.2).
func (s *ConferenceService) GetConference(ctx context.Context, req *connect.Request[v1.GetConferenceRequest]) (*connect.Response[v1.GetConferenceResponse], error) {
	if err := s.Scoper.MustHaveScopeOnConference(ctx, req.Msg.GetConferenceId()); err != nil {
		return nil, mapScopeErr(err)
	}
	id := strings.TrimSpace(req.Msg.GetConferenceId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	c, err := s.Store.GetConference(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetConference: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetConferenceResponse{Conference: conferenceToProto(c)}), nil
}

// CreateConference — admin only.
func (s *ConferenceService) CreateConference(ctx context.Context, req *connect.Request[v1.CreateConferenceRequest]) (*connect.Response[v1.CreateConferenceResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	c, _ := auth.FromContext(ctx)

	status := domain.ConferenceStatusDraft
	if d, ok := domainConferenceStatus(req.Msg.GetStatus()); ok && d != "" {
		status = d
	}
	in := domain.Conference{
		Name:          strings.TrimSpace(req.Msg.GetName()),
		EditionNumber: int(req.Msg.GetEditionNumber()),
		Year:          int(req.Msg.GetYear()),
		Status:        status,
		Metadata:      req.Msg.GetMetadata(),
		CreatedBy:     c.UserID,
	}
	if t := req.Msg.GetStartsAt(); t != nil {
		in.StartsAt = t.AsTime()
	}
	if t := req.Msg.GetEndsAt(); t != nil {
		in.EndsAt = t.AsTime()
	}
	if !in.StartsAt.Before(in.EndsAt) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("starts_at must precede ends_at"))
	}
	created, err := s.Store.CreateConference(ctx, in)
	if err != nil {
		s.log().Error("CreateConference: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.CreateConferenceResponse{Conference: conferenceToProto(created)}), nil
}

// UpdateConference — admin only.
func (s *ConferenceService) UpdateConference(ctx context.Context, req *connect.Request[v1.UpdateConferenceRequest]) (*connect.Response[v1.UpdateConferenceResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	c, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetConferenceId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	patch := store.UpdateConferencePatch{UpdatedBy: c.UserID}
	if req.Msg.Name != nil {
		v := strings.TrimSpace(*req.Msg.Name)
		patch.Name = &v
	}
	if req.Msg.EditionNumber != nil {
		v := int(*req.Msg.EditionNumber)
		patch.EditionNumber = &v
	}
	if req.Msg.Year != nil {
		v := int(*req.Msg.Year)
		patch.Year = &v
	}
	if t := req.Msg.GetStartsAt(); t != nil && req.Msg.StartsAt != nil {
		v := t.AsTime()
		patch.StartsAt = &v
	}
	if t := req.Msg.GetEndsAt(); t != nil && req.Msg.EndsAt != nil {
		v := t.AsTime()
		patch.EndsAt = &v
	}
	if req.Msg.Status != nil {
		if d, ok := domainConferenceStatus(*req.Msg.Status); ok {
			patch.Status = &d
		} else {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid status"))
		}
	}
	if req.Msg.GetMetadataSet() {
		m := req.Msg.GetMetadata()
		if m == nil {
			m = map[string]string{}
		}
		patch.Metadata = &m
	}
	updated, err := s.Store.UpdateConference(ctx, id, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdateConference: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.UpdateConferenceResponse{Conference: conferenceToProto(updated)}), nil
}

func (s *ConferenceService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// pageRequest extracts (cursor, pageSize) from the request envelope, applying
// the documented defaults (100 / 500 cap).
func pageRequest(p *v1.PageRequest) (string, int32) {
	if p == nil {
		return "", 100
	}
	size := p.GetPageSize()
	if size <= 0 {
		size = 100
	}
	if size > 500 {
		size = 500
	}
	return p.GetCursor(), size
}

// mapScopeErr maps auth.Err* sentinels onto Connect codes per AUTH.md §7.3.
func mapScopeErr(err error) error {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	case errors.Is(err, auth.ErrScopeDenied):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	return connect.NewError(connect.CodeUnavailable, err)
}

// ensureNotPast is reserved for future use when we enforce conference dates.
var _ = time.Now
