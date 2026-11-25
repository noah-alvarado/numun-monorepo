package handlers

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"connectrpc.com/connect"
	cognitotypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	usersv1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// UserService implements numunv1connect.UserServiceHandler.
type UserService struct {
	Store   *store.Client
	Cognito *auth.Cognito
	Logger  *slog.Logger
}

// GetMe — returns the authenticated caller's User row. See API.md §10.3.
func (s *UserService) GetMe(ctx context.Context, _ *connect.Request[usersv1.GetMeRequest]) (*connect.Response[usersv1.GetMeResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	u, err := s.Store.GetUser(ctx, c.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
		}
		s.logger().Error("GetMe: load user", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&usersv1.GetMeResponse{User: userToProto(u)}), nil
}

// GetUser — self or admin. See API.md §10.3.
func (s *UserService) GetUser(ctx context.Context, req *connect.Request[usersv1.GetUserRequest]) (*connect.Response[usersv1.GetUserResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	target := req.Msg.GetUserId()
	if target == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id required"))
	}
	if target != c.UserID && c.Role != domain.RoleStaffAdmin {
		// Anti-enumeration: a non-admin asking about another user gets not_found
		// regardless of whether the user exists (AUTH.md §7.3).
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	u, err := s.Store.GetUser(ctx, target)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.logger().Error("GetUser: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&usersv1.GetUserResponse{User: userToProto(u)}), nil
}

// UpdateUser — patch profile fields. Self or admin.
func (s *UserService) UpdateUser(ctx context.Context, req *connect.Request[usersv1.UpdateUserRequest]) (*connect.Response[usersv1.UpdateUserResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	target := req.Msg.GetUserId()
	if target == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id required"))
	}
	if target != c.UserID && c.Role != domain.RoleStaffAdmin {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}

	patch := store.UpdateUserPatch{UpdatedBy: c.UserID}
	if req.Msg.Name != nil {
		v := strings.TrimSpace(*req.Msg.Name)
		patch.Name = &v
	}
	if req.Msg.Phone != nil {
		v := strings.TrimSpace(*req.Msg.Phone)
		patch.Phone = &v
	}
	if req.Msg.AnnouncementsOptIn != nil {
		v := *req.Msg.AnnouncementsOptIn
		patch.AnnouncementsOptIn = &v
	}

	u, err := s.Store.UpdateUser(ctx, target, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.logger().Error("UpdateUser: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&usersv1.UpdateUserResponse{User: userToProto(u)}), nil
}

// InviteStaff — admin invites a new staff user.
func (s *UserService) InviteStaff(ctx context.Context, req *connect.Request[usersv1.InviteStaffRequest]) (*connect.Response[usersv1.InviteStaffResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}

	email := strings.TrimSpace(strings.ToLower(req.Msg.GetEmail()))
	if email == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email required"))
	}
	role, ok := domainRole(req.Msg.GetRole())
	if !ok || (role != domain.RoleStaffAdmin && role != domain.RoleStaffStaffer) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("role must be staff-admin or staff-staffer"))
	}

	if s.Cognito == nil || s.Cognito.UserPoolID == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("cognito not configured"))
	}
	out, err := s.Cognito.AdminCreateUser(ctx, auth.AdminCreateUserInput{
		Email: email,
		Name:  req.Msg.GetName(),
		Role:  string(role),
	})
	if err != nil {
		var exists *cognitotypes.UsernameExistsException
		if errors.As(err, &exists) {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("an account with that email already exists"))
		}
		s.logger().Error("InviteStaff: cognito", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("cognito unavailable"))
	}

	u, err := s.Store.CreateUser(ctx, domain.User{
		ID:        out.Sub,
		Role:      role,
		Email:     email,
		Name:      req.Msg.GetName(),
		CreatedBy: c.UserID,
	})
	if err != nil {
		// Cognito has already been written to; treat duplicate-mirror as a
		// soft success (resend invite scenario).
		if errors.Is(err, store.ErrAlreadyExists) {
			existing, gerr := s.Store.GetUser(ctx, out.Sub)
			if gerr == nil {
				return connect.NewResponse(&usersv1.InviteStaffResponse{User: userToProto(existing)}), nil
			}
		}
		s.logger().Error("InviteStaff: write mirror", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	// Audit event — best-effort, not load-bearing for the response.
	if err := s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      u.ID,
		ActorUserID: c.UserID,
		Kind:        domain.AuthEventStaffInvited,
		Metadata:    map[string]string{"role": string(role)},
	}); err != nil {
		s.logger().Warn("InviteStaff: audit", "err", err)
	}

	return connect.NewResponse(&usersv1.InviteStaffResponse{User: userToProto(u)}), nil
}

// ListStaff returns every staff account (admins and staffers), sorted by
// name. Staff only — it backs staffer assignment and award recipient pickers.
func (s *UserService) ListStaff(ctx context.Context, _ *connect.Request[usersv1.ListStaffRequest]) (*connect.Response[usersv1.ListStaffResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	if c.Role != domain.RoleStaffAdmin && c.Role != domain.RoleStaffStaffer {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("staff only"))
	}
	var staff []domain.User
	for _, role := range []domain.Role{domain.RoleStaffAdmin, domain.RoleStaffStaffer} {
		users, err := s.Store.ListUsersByRole(ctx, role)
		if err != nil {
			s.logger().Error("ListStaff: store", "err", err)
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
		}
		staff = append(staff, users...)
	}
	slices.SortFunc(staff, func(a, b domain.User) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.Email, b.Email))
	})
	out := &usersv1.ListStaffResponse{Items: make([]*usersv1.User, 0, len(staff))}
	for _, u := range staff {
		out.Items = append(out.Items, userToProto(u))
	}
	return connect.NewResponse(out), nil
}

// DismissAward records a permanent dismissal of the given award from the
// caller's congratulatory hero on /awards. Idempotent. See M11.
func (s *UserService) DismissAward(ctx context.Context, req *connect.Request[usersv1.DismissAwardRequest]) (*connect.Response[usersv1.DismissAwardResponse], error) {
	c, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	awardID := strings.TrimSpace(req.Msg.GetAwardId())
	if awardID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("award_id required"))
	}
	u, err := s.Store.AddDismissedAward(ctx, c.UserID, awardID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
		}
		s.logger().Error("DismissAward: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&usersv1.DismissAwardResponse{User: userToProto(u)}), nil
}

func (s *UserService) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
