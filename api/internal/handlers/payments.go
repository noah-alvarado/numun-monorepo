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

// PaymentService implements numunv1connect.PaymentServiceHandler. Reads are
// scoped per AUTH.md §9.2 (advisor on own delegation, staff-staffer case (a),
// admin always); mutating RPCs are staff-admin only.
type PaymentService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Logger *slog.Logger
}

func (s *PaymentService) ListPayments(ctx context.Context, req *connect.Request[v1.ListPaymentsRequest]) (*connect.Response[v1.ListPaymentsResponse], error) {
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
	rows, next, err := s.Store.ListPaymentsByDelegation(ctx, delID, cursor, size)
	if err != nil {
		s.log().Error("ListPayments: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListPaymentsResponse{
		Items: make([]*v1.PaymentRecord, 0, len(rows)),
		Page:  &v1.Page{NextCursor: next, PageSize: size},
	}
	for _, p := range rows {
		out.Items = append(out.Items, paymentToProto(p))
	}
	return connect.NewResponse(out), nil
}

func (s *PaymentService) ListAllPayments(ctx context.Context, req *connect.Request[v1.ListAllPaymentsRequest]) (*connect.Response[v1.ListAllPaymentsResponse], error) {
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
	rows, err := s.Store.ListAllPaymentsByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("ListAllPayments: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAllPaymentsResponse{Items: make([]*v1.PaymentRecord, 0, len(rows))}
	for _, p := range rows {
		out.Items = append(out.Items, paymentToProto(p))
	}
	return connect.NewResponse(out), nil
}

func (s *PaymentService) GetPayment(ctx context.Context, req *connect.Request[v1.GetPaymentRequest]) (*connect.Response[v1.GetPaymentResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetPaymentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("payment_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnPayment(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	p, err := s.Store.FindPaymentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetPayment: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetPaymentResponse{Payment: paymentToProto(p)}), nil
}

// RecordPayment is admin-only. Applies the sign convention here: charges are
// negative, payments are positive, adjustments pass through the caller's sign.
// See store/payment.go for the balanceDue arithmetic.
func (s *PaymentService) RecordPayment(ctx context.Context, req *connect.Request[v1.RecordPaymentRequest]) (*connect.Response[v1.RecordPaymentResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	parent, err := s.Store.FindDelegationByID(ctx, delID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("delegation not found"))
		}
		s.log().Error("RecordPayment: load delegation", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	kind, ok := domainPaymentKind(req.Msg.GetKind())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("kind required"))
	}
	amt := req.Msg.GetAmount()
	if amt == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("amount required"))
	}
	currency := strings.TrimSpace(amt.GetCurrency())
	if currency == "" {
		currency = "USD"
	}

	// Caller provides absolute magnitude (units, cents). The handler applies
	// the sign by kind. Adjustments may carry a negative amount on the wire.
	signedUnits := amt.GetUnits()
	signedCents := amt.GetCents()
	switch kind {
	case domain.PaymentKindCharge:
		// Charges are stored as negative; the wire shape is always positive
		// magnitude, so flip the sign here. (Adjustment kind retains caller's sign.)
		if signedUnits > 0 || signedCents > 0 {
			signedUnits, signedCents = -signedUnits, -signedCents
		}
	case domain.PaymentKindPayment:
		// Payments are stored as positive.
		if signedUnits < 0 {
			signedUnits = -signedUnits
		}
		if signedCents < 0 {
			signedCents = -signedCents
		}
	case domain.PaymentKindAdjustment:
		// Caller-supplied sign passes through verbatim.
	}

	p := domain.PaymentRecord{
		ConferenceID:   parent.ConferenceID,
		DelegationID:   delID,
		AmountCurrency: currency,
		AmountUnits:    signedUnits,
		AmountCents:    int32(signedCents),
		Kind:           kind,
		Method:         domainPaymentMethod(req.Msg.GetMethod()),
		Reference:      strings.TrimSpace(req.Msg.GetReference()),
		Notes:          strings.TrimSpace(req.Msg.GetNotes()),
		RecordedBy:     caller.UserID,
		RecordedAt:     time.Now().UTC(),
		CreatedBy:      caller.UserID,
		UpdatedBy:      caller.UserID,
	}
	created, err := s.Store.RecordPayment(ctx, p)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("delegation not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("delegation version mismatch — retry"))
		}
		s.log().Error("RecordPayment: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventPaymentRecorded,
		Metadata: map[string]string{
			"paymentId":    created.ID,
			"delegationId": delID,
			"conferenceId": parent.ConferenceID,
			"kind":         string(kind),
		},
	})
	return connect.NewResponse(&v1.RecordPaymentResponse{Payment: paymentToProto(created)}), nil
}

// UpdatePayment is admin-only and patches only notes/reference. Changing
// amount/kind/method would distort the ledger; record a new entry instead.
func (s *PaymentService) UpdatePayment(ctx context.Context, req *connect.Request[v1.UpdatePaymentRequest]) (*connect.Response[v1.UpdatePaymentResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetPaymentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("payment_id required"))
	}
	existing, err := s.Store.FindPaymentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdatePayment: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	patch := store.UpdatePaymentPatch{UpdatedBy: caller.UserID}
	if v := req.Msg.Reference; v != nil {
		val := strings.TrimSpace(*v)
		patch.Reference = &val
	}
	if v := req.Msg.Notes; v != nil {
		val := strings.TrimSpace(*v)
		patch.Notes = &val
	}
	sk := paymentSKFromRecord(existing)
	updated, err := s.Store.UpdatePayment(ctx, existing.DelegationID, sk, existing.ID, int(req.Msg.GetExpectedVersion()), patch)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("UpdatePayment: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventPaymentUpdated,
		Metadata: map[string]string{
			"paymentId":    id,
			"delegationId": existing.DelegationID,
			"conferenceId": existing.ConferenceID,
		},
	})
	return connect.NewResponse(&v1.UpdatePaymentResponse{Payment: paymentToProto(updated)}), nil
}

func (s *PaymentService) DeletePayment(ctx context.Context, req *connect.Request[v1.DeletePaymentRequest]) (*connect.Response[v1.DeletePaymentResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetPaymentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("payment_id required"))
	}
	existing, err := s.Store.FindPaymentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("DeletePayment: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	sk := paymentSKFromRecord(existing)
	if err := s.Store.SoftDeletePayment(ctx, existing.DelegationID, sk, int(req.Msg.GetExpectedVersion()), caller.UserID); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("DeletePayment: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventPaymentDeleted,
		Metadata: map[string]string{
			"paymentId":    id,
			"delegationId": existing.DelegationID,
			"conferenceId": existing.ConferenceID,
		},
	})
	return connect.NewResponse(&v1.DeletePaymentResponse{}), nil
}

// paymentSKFromRecord reconstructs the composite SK from a loaded record.
// Mirrors the store's PAYMENT#<recordedAtRFC3339Nano>#<id> shape so the
// handler can pass it back to UpdatePayment / SoftDeletePayment without
// re-querying the store's helper.
func paymentSKFromRecord(p domain.PaymentRecord) string {
	return "PAYMENT#" + p.RecordedAt.Format(time.RFC3339Nano) + "#" + p.ID
}

func (s *PaymentService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

func (s *PaymentService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
