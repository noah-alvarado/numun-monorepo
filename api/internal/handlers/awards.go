package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/cms"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// AwardService implements numunv1connect.AwardServiceHandler.
//
// Reads are open to any authenticated caller with scope on the parent
// conference (matches the public awards-archive visibility). Writes are
// scoped by role per IMPLEMENTATION_PLAN.md M11:
//   - staff-admin: any kind, any ID.
//   - staff-staffer: DELEGATE / DELEGATION / USER recipients tied to a
//     delegation the caller has a StaffDelegationAssignment on.
//   - advisor: no write access.
//
// On every mutation, the handler runs the resulting Award through the inline
// CMS sync (cms.Client) with 3-attempt exponential backoff. DDB is the source
// of truth; CMS sync failure is reported back to the caller but does not roll
// back the DDB write — callers retry via UpdateAward to re-sync.
type AwardService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	CMS    *cms.Client
	Logger *slog.Logger
}

func (s *AwardService) ListAwards(ctx context.Context, req *connect.Request[v1.ListAwardsRequest]) (*connect.Response[v1.ListAwardsResponse], error) {
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
	rows, err := s.Store.ListAwardsByConference(ctx, conferenceID)
	if err != nil {
		s.log().Error("ListAwards: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAwardsResponse{Items: make([]*v1.Award, 0, len(rows))}
	for _, a := range rows {
		out.Items = append(out.Items, awardToProto(a))
	}
	return connect.NewResponse(out), nil
}

func (s *AwardService) GetAward(ctx context.Context, req *connect.Request[v1.GetAwardRequest]) (*connect.Response[v1.GetAwardResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetAwardId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("award_id required"))
	}
	a, err := s.Store.FindAwardByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetAward: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, a.ConferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	return connect.NewResponse(&v1.GetAwardResponse{Award: awardToProto(a)}), nil
}

func (s *AwardService) CreateAward(ctx context.Context, req *connect.Request[v1.CreateAwardRequest]) (*connect.Response[v1.CreateAwardResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name required"))
	}
	recipients, err := recipientsFromProto(req.Msg.GetRecipients())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if len(recipients) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("recipients required"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, conferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	if err := s.assertWriteScope(ctx, caller, conferenceID, recipients); err != nil {
		return nil, err
	}
	// Denormalize display names server-side so the CMS markdown and Astro
	// renderer don't need to re-fetch.
	recipients = s.populateDisplayNames(ctx, recipients)

	created, err := s.Store.CreateAward(ctx, domain.Award{
		ConferenceID: conferenceID,
		Name:         name,
		Category:     strings.TrimSpace(req.Msg.GetCategory()),
		Recipients:   recipients,
		AwardedBy:    caller.UserID,
	})
	if err != nil {
		s.log().Error("CreateAward: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	status := s.syncToCMS(ctx, conferenceID, created, false)
	s.audit(ctx, caller, domain.AuthEventAwardCreated, created.ID, conferenceID, status.OK)

	return connect.NewResponse(&v1.CreateAwardResponse{
		Award:   awardToProto(created),
		CmsSync: statusToProto(status),
	}), nil
}

func (s *AwardService) UpdateAward(ctx context.Context, req *connect.Request[v1.UpdateAwardRequest]) (*connect.Response[v1.UpdateAwardResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetAwardId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("award_id required"))
	}
	existing, err := s.Store.FindAwardByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdateAward: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, existing.ConferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	// Scope check uses the recipient set the caller is asserting authority
	// over. If they're replacing recipients, both old and new must be in
	// scope so they can't escape their lane in either direction.
	scopeRecipients := existing.Recipients
	patch := store.UpdateAwardPatch{UpdatedBy: caller.UserID}
	if v := req.Msg.Name; v != nil {
		val := strings.TrimSpace(*v)
		patch.Name = &val
	}
	if v := req.Msg.Category; v != nil {
		val := strings.TrimSpace(*v)
		patch.Category = &val
	}
	if req.Msg.GetRecipientsSet() {
		newRecips, rerr := recipientsFromProto(req.Msg.GetRecipients())
		if rerr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, rerr)
		}
		if len(newRecips) == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("recipients must not be empty"))
		}
		newRecips = s.populateDisplayNames(ctx, newRecips)
		patch.Recipients = newRecips
		patch.RecipientsSet = true
		scopeRecipients = append(scopeRecipients, newRecips...)
	}
	if err := s.assertWriteScope(ctx, caller, existing.ConferenceID, scopeRecipients); err != nil {
		return nil, err
	}

	updated, err := s.Store.UpdateAward(ctx, existing.ConferenceID, id, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdateAward: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	status := s.syncToCMS(ctx, existing.ConferenceID, updated, false)
	s.audit(ctx, caller, domain.AuthEventAwardModified, id, existing.ConferenceID, status.OK)

	return connect.NewResponse(&v1.UpdateAwardResponse{
		Award:   awardToProto(updated),
		CmsSync: statusToProto(status),
	}), nil
}

func (s *AwardService) DeleteAward(ctx context.Context, req *connect.Request[v1.DeleteAwardRequest]) (*connect.Response[v1.DeleteAwardResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetAwardId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("award_id required"))
	}
	existing, err := s.Store.FindAwardByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("DeleteAward: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, existing.ConferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	if err := s.assertWriteScope(ctx, caller, existing.ConferenceID, existing.Recipients); err != nil {
		return nil, err
	}
	if err := s.Store.SoftDeleteAward(ctx, existing.ConferenceID, id, int(req.Msg.GetExpectedVersion()), caller.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("DeleteAward: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	status := s.syncToCMS(ctx, existing.ConferenceID, existing, true)
	s.audit(ctx, caller, domain.AuthEventAwardDeleted, id, existing.ConferenceID, status.OK)

	return connect.NewResponse(&v1.DeleteAwardResponse{CmsSync: statusToProto(status)}), nil
}

// assertWriteScope enforces the role-based write rules from M11. Returns nil
// when every recipient is within the caller's lane. Returns permission_denied
// otherwise — never not_found, since by the time this is called the caller
// already has read scope on the conference.
func (s *AwardService) assertWriteScope(ctx context.Context, caller auth.Caller, conferenceID string, recipients []domain.AwardRecipient) error {
	switch caller.Role {
	case domain.RoleStaffAdmin:
		return nil
	case domain.RoleAdvisor:
		return connect.NewError(connect.CodePermissionDenied, errors.New("advisors cannot create awards"))
	case domain.RoleStaffStaffer:
		// fall through to per-recipient check below
	default:
		return connect.NewError(connect.CodePermissionDenied, errors.New("role cannot create awards"))
	}

	for _, r := range recipients {
		ok, err := s.stafferCanTarget(ctx, caller.UserID, conferenceID, r)
		if err != nil {
			return connect.NewError(connect.CodeUnavailable, fmt.Errorf("scope check: %w", err))
		}
		if !ok {
			return connect.NewError(connect.CodePermissionDenied, errors.New("recipient out of scope"))
		}
	}
	return nil
}

// stafferCanTarget reports whether a staff-staffer caller may award the given
// recipient under M11's rules: delegates / delegations / staffers attached to
// a delegation the caller is assigned to. Other kinds (committee, conference,
// USER-that-is-an-advisor-not-on-the-delegation) are denied.
func (s *AwardService) stafferCanTarget(ctx context.Context, callerID, conferenceID string, r domain.AwardRecipient) (bool, error) {
	switch r.Kind {
	case domain.AwardRecipientKindDelegate:
		d, err := s.Store.FindDelegateByID(ctx, r.ID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		return s.stafferAssignedTo(ctx, callerID, d.DelegationID)
	case domain.AwardRecipientKindDelegation:
		return s.stafferAssignedTo(ctx, callerID, r.ID)
	case domain.AwardRecipientKindUser:
		// USER recipients are allowed when the user has a StaffDelegationAssignment
		// on a delegation that the caller is also assigned to. (Advisor recipients
		// are not in scope for staffers.)
		callerAssignments, err := s.Store.ListStaffOversightsByUser(ctx, callerID)
		if err != nil {
			return false, err
		}
		if len(callerAssignments) == 0 {
			return false, nil
		}
		callerDelegations := make(map[string]struct{}, len(callerAssignments))
		for _, a := range callerAssignments {
			callerDelegations[a.DelegationID] = struct{}{}
		}
		targetAssignments, err := s.Store.ListStaffOversightsByUser(ctx, r.ID)
		if err != nil {
			return false, err
		}
		for _, a := range targetAssignments {
			if _, shared := callerDelegations[a.DelegationID]; shared {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, nil
	}
}

func (s *AwardService) stafferAssignedTo(ctx context.Context, userID, delegationID string) (bool, error) {
	_, err := s.Store.GetStaffDelegationAssignment(ctx, delegationID, userID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return false, err
}

// populateDisplayNames best-effort fills in the DisplayName of each recipient
// so the CMS-side renderer can show human labels without re-fetching. Errors
// are swallowed; a missing display name is acceptable (CMS shows the kind+id).
func (s *AwardService) populateDisplayNames(ctx context.Context, recs []domain.AwardRecipient) []domain.AwardRecipient {
	out := make([]domain.AwardRecipient, len(recs))
	for i, r := range recs {
		out[i] = r
		if r.DisplayName != "" {
			continue
		}
		switch r.Kind {
		case domain.AwardRecipientKindDelegate:
			if d, err := s.Store.FindDelegateByID(ctx, r.ID); err == nil {
				out[i].DisplayName = strings.TrimSpace(d.FirstName + " " + d.LastName)
			}
		case domain.AwardRecipientKindDelegation:
			if d, err := s.Store.FindDelegationByID(ctx, r.ID); err == nil {
				out[i].DisplayName = d.School
			}
		case domain.AwardRecipientKindCommittee:
			if c, err := s.Store.FindCommitteeByID(ctx, r.ID); err == nil {
				out[i].DisplayName = c.Name
			}
		case domain.AwardRecipientKindUser:
			if u, err := s.Store.GetUser(ctx, r.ID); err == nil {
				out[i].DisplayName = u.Name
			}
		case domain.AwardRecipientKindConference:
			if c, err := s.Store.GetConference(ctx, r.ID); err == nil {
				out[i].DisplayName = c.Name
			}
		}
	}
	return out
}

// syncToCMS runs the inline git write. When deleting, it removes the file;
// otherwise it upserts. Errors are captured in the returned Status — the
// caller decides what to do with them (we surface them to the API client).
func (s *AwardService) syncToCMS(ctx context.Context, conferenceID string, a domain.Award, isDelete bool) cms.Status {
	if s.CMS == nil || s.CMS.IsStub() {
		return cms.Status{OK: true, Attempts: 0}
	}
	path := cms.AwardMarkdownPath(a.ID)
	if isDelete {
		return s.CMS.WithRetry(ctx, func(c context.Context) (string, error) {
			return "", s.CMS.DeleteFile(c, path, fmt.Sprintf("award: delete %s", a.ID))
		})
	}
	year := s.resolveConferenceYear(ctx, conferenceID)
	content := cms.RenderAwardMarkdown(a, year)
	return s.CMS.WithRetry(ctx, func(c context.Context) (string, error) {
		return s.CMS.UpsertFile(c, path, content, fmt.Sprintf("award: upsert %s (%s)", a.ID, a.Name))
	})
}

func (s *AwardService) resolveConferenceYear(ctx context.Context, conferenceID string) int {
	c, err := s.Store.GetConference(ctx, conferenceID)
	if err != nil {
		return 0
	}
	if c.Year > 0 {
		return c.Year
	}
	if !c.EndsAt.IsZero() {
		return c.EndsAt.Year()
	}
	return 0
}

func (s *AwardService) audit(ctx context.Context, caller auth.Caller, kind domain.AuthAuditEventKind, awardID, conferenceID string, cmsOK bool) {
	meta := map[string]string{
		"awardId":      awardID,
		"conferenceId": conferenceID,
		"cmsSyncOk":    fmt.Sprintf("%t", cmsOK),
	}
	if err := s.Store.RecordAuthEvent(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        kind,
		Metadata:    meta,
	}); err != nil {
		s.log().Warn("audit write failed", "kind", kind, "err", err)
	}
}

func (s *AwardService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func recipientsFromProto(in []*v1.AwardRecipient) ([]domain.AwardRecipient, error) {
	out := make([]domain.AwardRecipient, 0, len(in))
	for i, r := range in {
		kind, ok := domainAwardRecipientKind(r.GetKind())
		if !ok {
			return nil, fmt.Errorf("recipients[%d]: unspecified kind", i)
		}
		id := strings.TrimSpace(r.GetId())
		if id == "" {
			return nil, fmt.Errorf("recipients[%d]: id required", i)
		}
		out = append(out, domain.AwardRecipient{
			Kind:        kind,
			ID:          id,
			DisplayName: strings.TrimSpace(r.GetDisplayName()),
		})
	}
	return out, nil
}

func domainAwardRecipientKind(k v1.AwardRecipientKind) (domain.AwardRecipientKind, bool) {
	switch k {
	case v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_DELEGATE:
		return domain.AwardRecipientKindDelegate, true
	case v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_DELEGATION:
		return domain.AwardRecipientKindDelegation, true
	case v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_COMMITTEE:
		return domain.AwardRecipientKindCommittee, true
	case v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_USER:
		return domain.AwardRecipientKindUser, true
	case v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_CONFERENCE:
		return domain.AwardRecipientKindConference, true
	}
	return "", false
}

func protoAwardRecipientKind(k domain.AwardRecipientKind) v1.AwardRecipientKind {
	switch k {
	case domain.AwardRecipientKindDelegate:
		return v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_DELEGATE
	case domain.AwardRecipientKindDelegation:
		return v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_DELEGATION
	case domain.AwardRecipientKindCommittee:
		return v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_COMMITTEE
	case domain.AwardRecipientKindUser:
		return v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_USER
	case domain.AwardRecipientKindConference:
		return v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_CONFERENCE
	}
	return v1.AwardRecipientKind_AWARD_RECIPIENT_KIND_UNSPECIFIED
}

func statusToProto(s cms.Status) *v1.CmsSyncStatus {
	return &v1.CmsSyncStatus{
		Ok:         s.OK,
		Attempts:   int32(s.Attempts),
		FinalError: s.FinalError,
		CommitSha:  s.CommitSHA,
	}
}
