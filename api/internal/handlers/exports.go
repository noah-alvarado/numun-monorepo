// scope-check: skip — exports are HTTP routes outside the Connect surface,
// not RPC handlers. Authorization is enforced inline against the conference
// scope and per-delegation scope; the bash gate's per-method heuristic does
// not apply.

package handlers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/store"
)

// ExportRoutes mounts the parallel non-Connect CSV download surface per
// API.md §12.1. Routes live on the same http.ServeMux so they share the
// auth + CSRF middleware; CSRF is skipped naturally for safe GETs.
type ExportRoutes struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Logger *slog.Logger
}

// Register wires the export routes into the given mux. Currently:
//   - GET /v1/exports/payments.csv?conference_id=<uuid>
//
// Future M11 work adds /v1/exports/assignments.csv and /delegates.csv against
// this same handler set.
func (e *ExportRoutes) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/exports/payments.csv", e.handlePaymentsCSV)
}

// handlePaymentsCSV streams the payment ledger as UTF-8-with-BOM CSV using
// CRLF line endings (RFC 4180 + Excel-friendly). Per API.md §12.2:
//   - staff-admin: all delegations in the conference.
//   - advisor: only delegations they're attached to.
//   - staff-staffer: case (a) delegations they oversee directly. Case (c)
//     committee-only staffers are excluded entirely.
//
// Soft-deleted rows are omitted.
func (e *ExportRoutes) handlePaymentsCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	caller, ok := auth.FromContext(ctx)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conferenceID := strings.TrimSpace(r.URL.Query().Get("conference_id"))
	if conferenceID == "" {
		http.Error(w, "conference_id required", http.StatusBadRequest)
		return
	}
	if err := e.Scoper.MustHaveScopeOnConference(ctx, conferenceID); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Resolve the set of delegations the caller is allowed to see.
	delegations, err := e.scopedDelegations(ctx, caller, conferenceID)
	if err != nil {
		e.log().Error("exports.payments: scope delegations", "err", err)
		http.Error(w, "store unavailable", http.StatusServiceUnavailable)
		return
	}

	// Stream the CSV.
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="payments-%s.csv"`, conferenceID))

	// UTF-8 BOM so Excel detects the encoding.
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true

	// Column order per API.md §12.3 "payments.csv columns (v1)".
	if err := cw.Write([]string{
		"payment_id", "conference_id", "delegation_id", "school",
		"recorded_at", "amount_currency", "amount_units", "amount_cents",
		"kind", "method", "reference", "notes", "recorded_by",
	}); err != nil {
		return
	}

	for _, del := range delegations {
		rows, err := e.Store.ListAllPaymentsByDelegation(ctx, del.ID)
		if err != nil {
			e.log().Error("exports.payments: list", "delegationId", del.ID, "err", err)
			continue
		}
		for _, p := range rows {
			if p.IsDeleted {
				continue
			}
			_ = cw.Write([]string{
				p.ID,
				p.ConferenceID,
				p.DelegationID,
				del.School,
				p.RecordedAt.UTC().Format(time.RFC3339),
				p.AmountCurrency,
				fmt.Sprintf("%d", p.AmountUnits),
				fmt.Sprintf("%d", p.AmountCents),
				string(p.Kind),
				string(p.Method),
				p.Reference,
				p.Notes,
				p.RecordedBy,
			})
		}
	}
	cw.Flush()
}

// scopedDelegations returns the delegations the caller may see in this
// conference, matching API.md §9.2 + §12.2.
func (e *ExportRoutes) scopedDelegations(ctx context.Context, caller auth.Caller, conferenceID string) ([]domain.Delegation, error) {
	all, _, err := e.Store.ListDelegationsByConference(ctx, conferenceID, "", 1000)
	if err != nil {
		return nil, err
	}
	switch caller.Role {
	case domain.RoleStaffAdmin:
		return all, nil
	case domain.RoleAdvisor:
		out := make([]domain.Delegation, 0, len(all))
		for _, d := range all {
			if _, err := e.Store.GetAdvisor(ctx, d.ID, caller.UserID); err == nil {
				out = append(out, d)
			} else if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
		return out, nil
	case domain.RoleStaffStaffer:
		out := make([]domain.Delegation, 0, len(all))
		for _, d := range all {
			if _, err := e.Store.GetStaffDelegationAssignment(ctx, d.ID, caller.UserID); err == nil {
				out = append(out, d)
			} else if !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, nil
}

func (e *ExportRoutes) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}
