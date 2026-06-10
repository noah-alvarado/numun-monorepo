// scope-check: skip — see comment below.
//
// AnnouncementService is admin-only (auth.MustBeStaffAdmin gate on every RPC);
// admins have universal scope per AUTH.md §9.2, so the conferenceId in the
// audience filter is a filter input, not a scope-gating id.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/email"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// AnnouncementService implements numunv1connect.AnnouncementServiceHandler.
// staff-admin only. See EMAIL.md §5.3.
type AnnouncementService struct {
	Store  *store.Client
	Email  email.Service
	Logger *slog.Logger
}

// PreviewSendAudience returns the resolved recipient count + a sample. Used by
// the portal to confirm intent before commit. Read-only; no DDB writes.
func (s *AnnouncementService) PreviewSendAudience(ctx context.Context, req *connect.Request[v1.PreviewSendAudienceRequest]) (*connect.Response[v1.PreviewSendAudienceResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	conferenceID := strings.TrimSpace(req.Msg.GetAudience().GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("audience.conference_id required"))
	}
	status := domain.DelegationStatusApproved
	if v := req.Msg.GetAudience().GetDelegationStatus(); v != "" {
		status = domain.DelegationStatus(v)
	}
	recipients, err := s.resolveAudience(ctx, conferenceID, status)
	if err != nil {
		s.log().Error("PreviewSendAudience", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	sample := make([]string, 0, 5)
	for i, u := range recipients {
		if i >= 5 {
			break
		}
		sample = append(sample, u.Email)
	}
	return connect.NewResponse(&v1.PreviewSendAudienceResponse{
		RecipientCount:   int32(len(recipients)),
		SampleRecipients: sample,
	}), nil
}

// Send persists the announcement, then enqueues one SQS message per surviving
// recipient. Returns immediately; the actual SES sends happen in the worker.
func (s *AnnouncementService) Send(ctx context.Context, req *connect.Request[v1.AnnouncementServiceSendRequest]) (*connect.Response[v1.AnnouncementServiceSendResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	subject := strings.TrimSpace(req.Msg.GetSubject())
	bodyHTML := strings.TrimSpace(req.Msg.GetBodyHtml())
	bodyText := strings.TrimSpace(req.Msg.GetBodyText())
	if subject == "" || bodyHTML == "" || bodyText == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subject, body_html, body_text required"))
	}
	conferenceID := strings.TrimSpace(req.Msg.GetAudience().GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("audience.conference_id required"))
	}
	status := domain.DelegationStatusApproved
	if v := req.Msg.GetAudience().GetDelegationStatus(); v != "" {
		status = domain.DelegationStatus(v)
	}
	recipients, err := s.resolveAudience(ctx, conferenceID, status)
	if err != nil {
		s.log().Error("Send: resolve audience", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	// Serialize the filter so the persisted row records what was sent.
	filterJSON, _ := json.Marshal(map[string]string{
		"conferenceId":     conferenceID,
		"delegationStatus": string(status),
	})

	a := domain.Announcement{
		ConferenceID:   conferenceID,
		Subject:        subject,
		BodyHTML:       bodyHTML,
		BodyText:       bodyText,
		AudienceFilter: string(filterJSON),
		SentBy:         caller.UserID,
		RecipientCount: len(recipients),
		CreatedBy:      caller.UserID,
	}
	persisted, err := s.Store.CreateAnnouncement(ctx, a)
	if err != nil {
		s.log().Error("Send: persist", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	// Fan out one SQS message per recipient. Worker re-checks suppression at
	// delivery time, so a race between enqueue and bounce is harmless.
	if s.Email != nil {
		for _, u := range recipients {
			vars := map[string]any{
				"subject":  subject,
				"bodyHTML": bodyHTML,
				"bodyText": bodyText,
			}
			if err := s.Email.Enqueue(ctx, email.EnqueueRequest{
				UserID:         u.ID,
				RecipientEmail: u.Email,
				Kind:           domain.EmailKindAnnouncement,
				Subject:        subject,
				Vars:           vars,
				AnnouncementID: persisted.ID,
			}, 0); err != nil {
				s.log().Warn("Send: enqueue", "userId", u.ID, "err", err)
			}
		}
	}

	return connect.NewResponse(&v1.AnnouncementServiceSendResponse{
		Announcement: announcementToProto(persisted),
	}), nil
}

func (s *AnnouncementService) ListAnnouncements(ctx context.Context, req *connect.Request[v1.ListAnnouncementsRequest]) (*connect.Response[v1.ListAnnouncementsResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	cursor, size := pageRequest(req.Msg.GetPage())
	rows, next, err := s.Store.ListAnnouncements(ctx, cursor, size)
	if err != nil {
		s.log().Error("ListAnnouncements", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAnnouncementsResponse{
		Items: make([]*v1.Announcement, 0, len(rows)),
		Page:  &v1.Page{NextCursor: next, PageSize: size},
	}
	for _, a := range rows {
		out.Items = append(out.Items, announcementToProto(a))
	}
	return connect.NewResponse(out), nil
}

func (s *AnnouncementService) GetAnnouncement(ctx context.Context, req *connect.Request[v1.GetAnnouncementRequest]) (*connect.Response[v1.GetAnnouncementResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	id := strings.TrimSpace(req.Msg.GetAnnouncementId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("announcement_id required"))
	}
	a, err := s.Store.GetAnnouncement(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetAnnouncement", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetAnnouncementResponse{Announcement: announcementToProto(a)}), nil
}

// resolveAudience returns all advisor users on delegations of the given status
// in the conference, less suppressed addresses and announcements opt-outs.
func (s *AnnouncementService) resolveAudience(ctx context.Context, conferenceID string, status domain.DelegationStatus) ([]domain.User, error) {
	dels, _, err := s.Store.ListDelegationsByStatus(ctx, conferenceID, status, "", 500)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]domain.User, 0, len(dels))
	for _, d := range dels {
		advisors, err := s.Store.ListAdvisorsByDelegation(ctx, d.ID)
		if err != nil {
			continue
		}
		for _, a := range advisors {
			if seen[a.UserID] {
				continue
			}
			seen[a.UserID] = true
			u, err := s.Store.GetUser(ctx, a.UserID)
			if err != nil {
				continue
			}
			if u.EmailStatus != "" && u.EmailStatus != domain.EmailStatusOK {
				continue
			}
			if !u.AnnouncementsOptIn {
				continue
			}
			out = append(out, u)
		}
	}
	return out, nil
}

func (s *AnnouncementService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func announcementToProto(a domain.Announcement) *v1.Announcement {
	out := &v1.Announcement{
		Id:             a.ID,
		ConferenceId:   a.ConferenceID,
		Subject:        a.Subject,
		BodyHtml:       a.BodyHTML,
		BodyText:       a.BodyText,
		AudienceFilter: a.AudienceFilter,
		SentBy:         a.SentBy,
		RecipientCount: int32(a.RecipientCount),
		Version:        int32(a.Version),
	}
	if !a.SentAt.IsZero() {
		out.SentAt = timestamppb.New(a.SentAt)
	}
	if !a.CreatedAt.IsZero() {
		out.CreatedAt = timestamppb.New(a.CreatedAt)
	}
	if !a.UpdatedAt.IsZero() {
		out.UpdatedAt = timestamppb.New(a.UpdatedAt)
	}
	return out
}
