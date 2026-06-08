package handlers

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// PublicService implements numunv1connect.PublicServiceHandler.
//
// scope-check: skip
//
// Unauthenticated by design. The middleware does not attach a Caller; handlers
// here must not call auth.From* helpers.
type PublicService struct {
	Store  *store.Client
	Logger *slog.Logger
}

// GetActiveConference returns the unique conference whose status is
// open-for-registration or in-progress. See API.md §10.1b.
func (s *PublicService) GetActiveConference(ctx context.Context, _ *connect.Request[v1.GetActiveConferenceRequest]) (*connect.Response[v1.GetActiveConferenceResponse], error) {
	c, err := s.Store.FindActiveConference(ctx)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			// No active conference. Returning an empty conference field with
			// HTTP 200 is friendlier for the static-site build pipeline than
			// surfacing not_found — the workflow simply omits the live block.
			return connect.NewResponse(&v1.GetActiveConferenceResponse{}), nil
		case errors.Is(err, store.ErrMultipleActiveConferences):
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("multiple active conferences"))
		}
		s.log().Error("GetActiveConference: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetActiveConferenceResponse{Conference: activeConferenceToProto(c)}), nil
}

func activeConferenceToProto(c domain.Conference) *v1.ActiveConferenceSummary {
	return &v1.ActiveConferenceSummary{
		ConferenceId:       c.ID,
		Name:               c.Name,
		EditionNumber:      int32(c.EditionNumber),
		Year:               int32(c.Year),
		StartsAt:           tsOrNilFor(c.StartsAt),
		EndsAt:             tsOrNilFor(c.EndsAt),
		RegistrationStatus: registrationStatusFor(c.Status),
		ThemeMetadata:      c.Metadata,
	}
}

func registrationStatusFor(s domain.ConferenceStatus) v1.RegistrationStatus {
	switch s {
	case domain.ConferenceStatusOpenForRegistration:
		return v1.RegistrationStatus_REGISTRATION_STATUS_OPEN
	case domain.ConferenceStatusInProgress:
		return v1.RegistrationStatus_REGISTRATION_STATUS_IN_PROGRESS
	}
	return v1.RegistrationStatus_REGISTRATION_STATUS_UNSPECIFIED
}

func (s *PublicService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
