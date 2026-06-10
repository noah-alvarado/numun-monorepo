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

// EmailHealthService implements numunv1connect.EmailHealthServiceHandler.
// staff-admin only. See EMAIL.md §9.
type EmailHealthService struct {
	Store  *store.Client
	Logger *slog.Logger
}

func (s *EmailHealthService) ListSuppressed(ctx context.Context, _ *connect.Request[v1.ListSuppressedRequest]) (*connect.Response[v1.ListSuppressedResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	users, err := s.Store.ListSuppressedUsers(ctx)
	if err != nil {
		s.log().Error("ListSuppressed", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListSuppressedResponse{Items: make([]*v1.SuppressedUser, 0, len(users))}
	for _, u := range users {
		out.Items = append(out.Items, &v1.SuppressedUser{
			UserId: u.ID,
			Email:  u.Email,
			Name:   u.Name,
			Status: string(u.EmailStatus),
		})
	}
	return connect.NewResponse(out), nil
}

func (s *EmailHealthService) Unsuppress(ctx context.Context, req *connect.Request[v1.UnsuppressRequest]) (*connect.Response[v1.UnsuppressResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	userID := strings.TrimSpace(req.Msg.GetUserId())
	if userID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id required"))
	}
	ok := domain.EmailStatusOK
	patch := store.UpdateUserPatch{
		EmailStatus: &ok,
		UpdatedBy:   caller.UserID,
	}
	if _, err := s.Store.UpdateUser(ctx, userID, int(req.Msg.GetExpectedVersion()), patch); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("Unsuppress: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	// Write an audit event so the action is searchable on the user's row.
	_ = s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      userID,
		ActorUserID: caller.UserID,
		Kind:        "email_unsuppressed", // not in the canonical enum; EMAIL.md §9 calls it out
		Metadata:    map[string]string{},
	})
	return connect.NewResponse(&v1.UnsuppressResponse{}), nil
}

func (s *EmailHealthService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
