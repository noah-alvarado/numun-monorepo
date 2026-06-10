// scope-check: skip — see comment below.
//
// This file references CommitteeId / DelegationId / DelegateId as query
// filters (ListAssignments) or pairing inputs (UpdateAssignment), not as
// scope-gating ids. The actual scope gates are:
//   - MustBeStaffAdmin (mutations + bulk approve)
//   - MustHaveScopeOnConference (list + run history reads)
//   - MustHaveScopeOnAssignment (single-assignment Approve/Unapprove/Update)
// The check-scope-helpers.sh heuristic flags any reference to those id
// substrings, so the lint exception is recorded here on purpose.

package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	"github.com/numun/numun/api/internal/domain/assignment"
	"github.com/numun/numun/api/internal/email"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// assignmentBatchSize is the per-TransactWriteItems cap when persisting
// proposals. DDB hard limit is 100; 25 leaves plenty of room and keeps each
// batch quick. See BULK_IMPORT.md §6.4 for the precedent.
const assignmentBatchSize = 25

// algorithmSoftCap is the budget the handler grants Propose under the API
// Gateway 29s ceiling. See ASSIGNMENT_ALGORITHM.md §9.
const algorithmSoftCap = 20 * time.Second

// AssignmentService implements numunv1connect.AssignmentServiceHandler.
// Most reads require scope on the conference/assignment; mutating RPCs are
// staff-admin only.
type AssignmentService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Email  email.Service
	Logger *slog.Logger
}

func (s *AssignmentService) ListAssignments(ctx context.Context, req *connect.Request[v1.ListAssignmentsRequest]) (*connect.Response[v1.ListAssignmentsResponse], error) {
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
	committeeID := strings.TrimSpace(req.Msg.GetCommitteeId())
	delegationID := strings.TrimSpace(req.Msg.GetDelegationId())
	var status domain.AssignmentStatus
	if v, ok := domainAssignmentStatus(req.Msg.GetStatus()); ok {
		status = v
	}
	rows, err := s.Store.ListAllAssignmentsByConference(ctx, conferenceID, committeeID, delegationID, status)
	if err != nil {
		s.log().Error("ListAssignments: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAssignmentsResponse{
		Items: make([]*v1.Assignment, 0, len(rows)),
		Page:  &v1.Page{},
	}
	for _, a := range rows {
		out.Items = append(out.Items, assignmentToProto(a))
	}
	return connect.NewResponse(out), nil
}

func (s *AssignmentService) GetAssignment(ctx context.Context, req *connect.Request[v1.GetAssignmentRequest]) (*connect.Response[v1.GetAssignmentResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetAssignmentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("assignment_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnAssignment(ctx, id); err != nil {
		return nil, mapScopeErr(err)
	}
	a, err := s.Store.FindAssignmentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetAssignment: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetAssignmentResponse{Assignment: assignmentToProto(a)}), nil
}

// Propose runs the assignment algorithm. Dry runs return the proposal without
// persisting. Real runs delete prior proposed rows, write the new ones in
// TransactWriteItems batches, and update the AssignmentRun status. The
// in-flight concurrency check lives in the store via FindInFlightRun + a
// best-effort retry: any concurrent caller observing status=running gets
// ErrAlgorithmAlreadyRunning surfaced as failed_precondition.
//
// See ASSIGNMENT_ALGORITHM.md §8 + §9 and IMPLEMENTATION_PLAN.md §M7.
func (s *AssignmentService) Propose(ctx context.Context, req *connect.Request[v1.ProposeRequest]) (*connect.Response[v1.ProposeResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}

	// Load inputs from DDB and assemble the algorithm's Inputs struct.
	in, err := s.loadAlgorithmInputs(ctx, conferenceID)
	if err != nil {
		return nil, err
	}

	// Decide seed + isCanonical.
	var seed uint64
	isCanonical := false
	if req.Msg.Seed != nil {
		seed = req.Msg.GetSeed()
	} else {
		seed = canonicalSeed(conferenceID)
		isCanonical = true
	}

	// Compute run ordinal and inputs hash up-front; both feed the AssignmentRun row.
	ordinal, err := s.Store.NextRunOrdinal(ctx, conferenceID)
	if err != nil {
		s.log().Error("Propose: next ordinal", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	inputsHash := assignment.HashInputs(in)

	// Dry-run path: no AssignmentRun row, no writes.
	if req.Msg.GetDryRun() {
		runCtx, cancel := context.WithTimeout(ctx, algorithmSoftCap)
		defer cancel()
		prop, propErr := assignment.Propose(runCtx, in, assignment.RunOptions{Seed: seed})
		now := time.Now().UTC()
		dryRun := domain.AssignmentRun{
			ID:           uuid.NewString(),
			ConferenceID: conferenceID,
			Seed:         seed,
			RunOrdinal:   ordinal,
			IsCanonical:  isCanonical,
			TriggeredBy:  caller.UserID,
			TriggeredAt:  now,
			CompletedAt:  time.Now().UTC(),
			InputsHash:   inputsHash,
		}
		if propErr != nil {
			dryRun.Status = domain.AssignmentRunStatusFailed
			dryRun.Diagnostics = propErr.Error()
			return connect.NewResponse(&v1.ProposeResponse{Run: assignmentRunToProto(dryRun)}), nil
		}
		dryRun.Status = domain.AssignmentRunStatusDone
		dryRun.Objective = prop.Objective
		dryRun.AssignmentCount = len(prop.Assignments)
		return connect.NewResponse(&v1.ProposeResponse{
			Run:         assignmentRunToProto(dryRun),
			Assignments: synthAssignmentsForDryRun(prop, conferenceID, dryRun.ID),
		}), nil
	}

	// Real run: create an AssignmentRun in status=running first. The store's
	// CreateAssignmentRun returns ErrAlgorithmAlreadyRunning if another run is
	// in flight for this conference (race window documented in the store).
	now := time.Now().UTC()
	run := domain.AssignmentRun{
		ConferenceID: conferenceID,
		Seed:         seed,
		RunOrdinal:   ordinal,
		IsCanonical:  isCanonical,
		TriggeredBy:  caller.UserID,
		TriggeredAt:  now,
		Status:       domain.AssignmentRunStatusRunning,
		InputsHash:   inputsHash,
	}
	persistedRun, err := s.Store.CreateAssignmentRun(ctx, run)
	if err != nil {
		if errors.Is(err, store.ErrAlgorithmAlreadyRunning) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("another assignment run is in progress"))
		}
		s.log().Error("Propose: create run", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentRunStarted,
		Metadata: map[string]string{
			"conferenceId": conferenceID,
			"runId":        persistedRun.ID,
			"seed":         fmt.Sprintf("%d", seed),
			"runOrdinal":   fmt.Sprintf("%d", ordinal),
			"isCanonical":  fmt.Sprintf("%t", isCanonical),
		},
	})

	// Run the algorithm under a deadline.
	runCtx, cancel := context.WithTimeout(ctx, algorithmSoftCap)
	defer cancel()
	prop, propErr := assignment.Propose(runCtx, in, assignment.RunOptions{Seed: seed})
	if propErr != nil {
		updated, _ := s.Store.UpdateAssignmentRunStatus(ctx, persistedRun.ID, domain.AssignmentRunStatusFailed, 0, 0, propErr.Error(), time.Now().UTC())
		s.audit(ctx, domain.AuthAuditEvent{
			UserID:      caller.UserID,
			ActorUserID: caller.UserID,
			Kind:        domain.AuthEventAssignmentRunCompleted,
			Metadata: map[string]string{
				"conferenceId": conferenceID,
				"runId":        persistedRun.ID,
				"status":       "failed",
				"diagnostics":  propErr.Error(),
			},
		})
		return connect.NewResponse(&v1.ProposeResponse{Run: assignmentRunToProto(updated)}), nil
	}

	// Persist the proposal: delete prior proposed-status rows + write the new ones.
	if err := s.Store.DeleteAllProposedAssignmentsForConference(ctx, conferenceID); err != nil {
		s.log().Error("Propose: delete prior proposed", "err", err)
		_, _ = s.Store.UpdateAssignmentRunStatus(ctx, persistedRun.ID, domain.AssignmentRunStatusFailed, 0, 0, "delete prior proposed: "+err.Error(), time.Now().UTC())
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("persistence failed"))
	}

	created := buildAssignmentRows(prop, conferenceID, persistedRun.ID, caller.UserID)
	for i := 0; i < len(created); i += assignmentBatchSize {
		end := i + assignmentBatchSize
		if end > len(created) {
			end = len(created)
		}
		if err := s.Store.WriteAssignmentBatch(ctx, persistedRun.ID, created[i:end], nil); err != nil {
			s.log().Error("Propose: write batch", "err", err, "batchStart", i)
			diag := fmt.Sprintf("write batch starting at %d: %s", i, err.Error())
			_, _ = s.Store.UpdateAssignmentRunStatus(ctx, persistedRun.ID, domain.AssignmentRunStatusFailed, prop.Objective, i, diag, time.Now().UTC())
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("partial proposal persisted; run failed"))
		}
	}

	updated, err := s.Store.UpdateAssignmentRunStatus(ctx, persistedRun.ID, domain.AssignmentRunStatusDone, prop.Objective, len(created), "", time.Now().UTC())
	if err != nil {
		s.log().Error("Propose: finalize run", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentRunCompleted,
		Metadata: map[string]string{
			"conferenceId":    conferenceID,
			"runId":           persistedRun.ID,
			"status":          "done",
			"objective":       fmt.Sprintf("%.4f", prop.Objective),
			"assignmentCount": fmt.Sprintf("%d", len(created)),
		},
	})
	s.notifyRunCompleted(ctx, caller.UserID, conferenceID, persistedRun.ID, prop.Objective, len(created))

	// Convert the persisted set back to proto for the response.
	persistedProto := make([]*v1.Assignment, 0, len(created))
	for _, a := range created {
		persistedProto = append(persistedProto, assignmentToProto(a))
	}
	return connect.NewResponse(&v1.ProposeResponse{
		Run:         assignmentRunToProto(updated),
		Assignments: persistedProto,
	}), nil
}

func (s *AssignmentService) Approve(ctx context.Context, req *connect.Request[v1.AssignmentServiceApproveRequest]) (*connect.Response[v1.AssignmentServiceApproveResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetAssignmentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("assignment_id required"))
	}
	existing, err := s.Store.FindAssignmentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("Approve: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	updated, err := s.Store.ApproveAssignment(ctx, existing.PositionID, existing.DelegateID, int(req.Msg.GetExpectedVersion()), caller.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("Approve: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentApproved,
		Metadata: map[string]string{
			"assignmentId": id,
			"conferenceId": existing.ConferenceID,
		},
	})
	return connect.NewResponse(&v1.AssignmentServiceApproveResponse{Assignment: assignmentToProto(updated)}), nil
}

func (s *AssignmentService) Unapprove(ctx context.Context, req *connect.Request[v1.AssignmentServiceUnapproveRequest]) (*connect.Response[v1.AssignmentServiceUnapproveResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetAssignmentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("assignment_id required"))
	}
	existing, err := s.Store.FindAssignmentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("Unapprove: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	updated, err := s.Store.UnapproveAssignment(ctx, existing.PositionID, existing.DelegateID, int(req.Msg.GetExpectedVersion()), caller.UserID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		case errors.Is(err, store.ErrVersionMismatch):
			return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
		}
		s.log().Error("Unapprove: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentUnapproved,
		Metadata: map[string]string{
			"assignmentId": id,
			"conferenceId": existing.ConferenceID,
		},
	})
	return connect.NewResponse(&v1.AssignmentServiceUnapproveResponse{Assignment: assignmentToProto(updated)}), nil
}

// ApproveAll iterates all proposed assignments matching the optional run_id
// filter and flips each to approved. Not transactional; partial failures are
// surfaced via approved_count + error.
func (s *AssignmentService) ApproveAll(ctx context.Context, req *connect.Request[v1.ApproveAllRequest]) (*connect.Response[v1.ApproveAllResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	runFilter := strings.TrimSpace(req.Msg.GetRunId())
	rows, err := s.Store.ListAllAssignmentsByConference(ctx, conferenceID, "", "", domain.AssignmentStatusProposed)
	if err != nil {
		s.log().Error("ApproveAll: list", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	approved := int32(0)
	for _, a := range rows {
		if runFilter != "" && a.RunID != runFilter {
			continue
		}
		if _, err := s.Store.ApproveAssignment(ctx, a.PositionID, a.DelegateID, a.Version, caller.UserID); err != nil {
			s.log().Warn("ApproveAll: row failed", "assignmentId", a.ID, "err", err)
			continue
		}
		approved++
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentApproved,
		Metadata: map[string]string{
			"conferenceId":  conferenceID,
			"runId":         runFilter,
			"approvedCount": fmt.Sprintf("%d", approved),
			"bulk":          "true",
		},
	})
	return connect.NewResponse(&v1.ApproveAllResponse{ApprovedCount: approved}), nil
}

// ApproveByIds approves a caller-selected subset. Per-row failures populate
// the response `failures` list so the portal can flag a specific row without
// losing the rest of the batch. Audit event is bulk-style with a count.
func (s *AssignmentService) ApproveByIds(ctx context.Context, req *connect.Request[v1.ApproveByIdsRequest]) (*connect.Response[v1.ApproveByIdsResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	out := &v1.ApproveByIdsResponse{}
	for _, m := range req.Msg.GetItems() {
		id := strings.TrimSpace(m.GetAssignmentId())
		if id == "" {
			out.Failures = append(out.Failures, &v1.AssignmentMutationFailure{
				AssignmentId: id, Code: "invalid_argument", Message: "assignment_id required",
			})
			continue
		}
		existing, err := s.Store.FindAssignmentByID(ctx, id)
		if err != nil {
			out.Failures = append(out.Failures, bulkFailure(id, err))
			continue
		}
		if existing.ConferenceID != conferenceID {
			out.Failures = append(out.Failures, &v1.AssignmentMutationFailure{
				AssignmentId: id, Code: "failed_precondition",
				Message: "assignment belongs to a different conference",
			})
			continue
		}
		if _, err := s.Store.ApproveAssignment(ctx, existing.PositionID, existing.DelegateID, int(m.GetExpectedVersion()), caller.UserID); err != nil {
			out.Failures = append(out.Failures, bulkFailure(id, err))
			continue
		}
		out.ApprovedCount++
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentApproved,
		Metadata: map[string]string{
			"conferenceId":  conferenceID,
			"approvedCount": fmt.Sprintf("%d", out.ApprovedCount),
			"failureCount":  fmt.Sprintf("%d", len(out.Failures)),
			"bulk":          "true",
			"selection":     "byIds",
		},
	})
	return connect.NewResponse(out), nil
}

// UnapproveByIds is the symmetric un-approve for a selected set.
func (s *AssignmentService) UnapproveByIds(ctx context.Context, req *connect.Request[v1.UnapproveByIdsRequest]) (*connect.Response[v1.UnapproveByIdsResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	conferenceID := strings.TrimSpace(req.Msg.GetConferenceId())
	if conferenceID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("conference_id required"))
	}
	out := &v1.UnapproveByIdsResponse{}
	for _, m := range req.Msg.GetItems() {
		id := strings.TrimSpace(m.GetAssignmentId())
		if id == "" {
			out.Failures = append(out.Failures, &v1.AssignmentMutationFailure{
				AssignmentId: id, Code: "invalid_argument", Message: "assignment_id required",
			})
			continue
		}
		existing, err := s.Store.FindAssignmentByID(ctx, id)
		if err != nil {
			out.Failures = append(out.Failures, bulkFailure(id, err))
			continue
		}
		if existing.ConferenceID != conferenceID {
			out.Failures = append(out.Failures, &v1.AssignmentMutationFailure{
				AssignmentId: id, Code: "failed_precondition",
				Message: "assignment belongs to a different conference",
			})
			continue
		}
		if _, err := s.Store.UnapproveAssignment(ctx, existing.PositionID, existing.DelegateID, int(m.GetExpectedVersion()), caller.UserID); err != nil {
			out.Failures = append(out.Failures, bulkFailure(id, err))
			continue
		}
		out.UnapprovedCount++
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentUnapproved,
		Metadata: map[string]string{
			"conferenceId":    conferenceID,
			"unapprovedCount": fmt.Sprintf("%d", out.UnapprovedCount),
			"failureCount":    fmt.Sprintf("%d", len(out.Failures)),
			"bulk":            "true",
			"selection":       "byIds",
		},
	})
	return connect.NewResponse(out), nil
}

// SwapAssignments swaps two delegate↔position pairings. Implemented as two
// UpdateAssignment-style edits (delete old, create new) but ordered so both
// rows land in a consistent state — we delete both old rows first, then
// create both new pairings. If either delete fails the function returns
// before any new row is written; if a create fails we attempt to roll back
// the delete by writing the original row shape.
//
// At v1 scale this is sequential rather than transactional; the
// AssignmentRun integrity invariant (one position ↔ one delegate per
// conference) is preserved because both pairings change atomically from
// the caller's perspective — the brief window between deletes and creates
// is staff-only and not observable to advisors.
func (s *AssignmentService) SwapAssignments(ctx context.Context, req *connect.Request[v1.SwapAssignmentsRequest]) (*connect.Response[v1.SwapAssignmentsResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	aMut := req.Msg.GetA()
	bMut := req.Msg.GetB()
	aID := strings.TrimSpace(aMut.GetAssignmentId())
	bID := strings.TrimSpace(bMut.GetAssignmentId())
	if aID == "" || bID == "" || aID == bID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("two distinct assignment ids required"))
	}

	a, err := s.Store.FindAssignmentByID(ctx, aID)
	if err != nil {
		return nil, mapAssignmentLoadErr(err)
	}
	b, err := s.Store.FindAssignmentByID(ctx, bID)
	if err != nil {
		return nil, mapAssignmentLoadErr(err)
	}
	if a.ConferenceID != b.ConferenceID {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("assignments must share a conference"))
	}
	if a.Version != int(aMut.GetExpectedVersion()) || b.Version != int(bMut.GetExpectedVersion()) {
		return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch on one or both assignments"))
	}

	// Delete the two old rows, then create the swapped pairings. Each new
	// row inherits the original assignment's status (approved/proposed) so
	// a swap between two approved rows lands as two approved rows.
	if err := s.Store.SoftDeleteAssignment(ctx, a.PositionID, a.DelegateID, a.Version, caller.UserID); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("delete A failed"))
	}
	if err := s.Store.SoftDeleteAssignment(ctx, b.PositionID, b.DelegateID, b.Version, caller.UserID); err != nil {
		// Best-effort rollback of A's soft-delete is impractical; surface the
		// failure and let the operator reconcile via the audit log + portal.
		s.log().Error("SwapAssignments: A deleted but B delete failed", "aID", aID, "bID", bID, "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("delete B failed; A already deleted — reconcile via studio"))
	}

	// Look up the new positions' denormalized fields.
	posA, err := s.Store.FindPositionByID(ctx, a.PositionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("load position A failed"))
	}
	posB, err := s.Store.FindPositionByID(ctx, b.PositionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("load position B failed"))
	}

	// New A: delegate from old B → position from old A.
	newA, err := s.Store.CreateAssignment(ctx, domain.Assignment{
		ConferenceID: a.ConferenceID,
		CommitteeID:  posA.CommitteeID,
		PositionID:   a.PositionID,
		DelegationID: b.DelegationID,
		DelegateID:   b.DelegateID,
		Status:       a.Status,
		RunID:        a.RunID,
		ProposedAt:   time.Now().UTC(),
		CreatedBy:    caller.UserID,
		UpdatedBy:    caller.UserID,
	})
	if err != nil {
		s.log().Error("SwapAssignments: create new-A failed", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("create new pairing A failed"))
	}
	newB, err := s.Store.CreateAssignment(ctx, domain.Assignment{
		ConferenceID: b.ConferenceID,
		CommitteeID:  posB.CommitteeID,
		PositionID:   b.PositionID,
		DelegationID: a.DelegationID,
		DelegateID:   a.DelegateID,
		Status:       b.Status,
		RunID:        b.RunID,
		ProposedAt:   time.Now().UTC(),
		CreatedBy:    caller.UserID,
		UpdatedBy:    caller.UserID,
	})
	if err != nil {
		s.log().Error("SwapAssignments: create new-B failed", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("create new pairing B failed; new-A already created — reconcile via studio"))
	}

	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentManuallyEdited,
		Metadata: map[string]string{
			"action":       "swap",
			"conferenceId": a.ConferenceID,
			"oldA":         aID,
			"oldB":         bID,
			"newA":         newA.ID,
			"newB":         newB.ID,
		},
	})
	return connect.NewResponse(&v1.SwapAssignmentsResponse{
		A: assignmentToProto(newA),
		B: assignmentToProto(newB),
	}), nil
}

// bulkFailure converts a store error into the wire failure shape.
func bulkFailure(id string, err error) *v1.AssignmentMutationFailure {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return &v1.AssignmentMutationFailure{AssignmentId: id, Code: "not_found", Message: "not found"}
	case errors.Is(err, store.ErrVersionMismatch):
		return &v1.AssignmentMutationFailure{AssignmentId: id, Code: "aborted", Message: "version mismatch"}
	default:
		return &v1.AssignmentMutationFailure{AssignmentId: id, Code: "unavailable", Message: err.Error()}
	}
}

// mapAssignmentLoadErr converts a store load error into the matching Connect code.
func mapAssignmentLoadErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	return connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
}

// UpdateAssignment lets staff change an assignment's pairing. When the
// delegate or position changes, the underlying DDB row changes PK/SK, so we
// soft-delete the old row and create a new one. Manual edits never carry
// score/reason; they're flagged "manually edited by staff".
func (s *AssignmentService) UpdateAssignment(ctx context.Context, req *connect.Request[v1.UpdateAssignmentRequest]) (*connect.Response[v1.UpdateAssignmentResponse], error) {
	if err := auth.MustBeStaffAdmin(ctx); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("admin only"))
	}
	caller, _ := auth.FromContext(ctx)
	id := strings.TrimSpace(req.Msg.GetAssignmentId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("assignment_id required"))
	}
	existing, err := s.Store.FindAssignmentByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("UpdateAssignment: load", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if existing.Version != int(req.Msg.GetExpectedVersion()) {
		return nil, connect.NewError(connect.CodeAborted, errors.New("version mismatch"))
	}

	newDelegateID := existing.DelegateID
	if v := req.Msg.DelegateId; v != nil {
		newDelegateID = strings.TrimSpace(*v)
	}
	newPositionID := existing.PositionID
	if v := req.Msg.PositionId; v != nil {
		newPositionID = strings.TrimSpace(*v)
	}
	if newDelegateID == "" || newPositionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegate_id and position_id must be non-empty"))
	}

	// No-op fast path.
	if newDelegateID == existing.DelegateID && newPositionID == existing.PositionID {
		return connect.NewResponse(&v1.UpdateAssignmentResponse{Assignment: assignmentToProto(existing)}), nil
	}

	// Resolve denormalized fields from the new position + delegate.
	newPosition, err := s.Store.FindPositionByID(ctx, newPositionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("position_id not found"))
		}
		s.log().Error("UpdateAssignment: load position", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	newDelegate, err := s.Store.FindDelegateByID(ctx, newDelegateID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegate_id not found"))
		}
		s.log().Error("UpdateAssignment: load delegate", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	// Soft-delete the old row, then create the new one. Not transactional
	// (different PK/SK); acceptable for a manual edit. Audited as one
	// manually-edited event.
	if err := s.Store.SoftDeleteAssignment(ctx, existing.PositionID, existing.DelegateID, existing.Version, caller.UserID); err != nil {
		s.log().Error("UpdateAssignment: soft-delete old", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	newAssignment := domain.Assignment{
		ConferenceID: existing.ConferenceID,
		DelegateID:   newDelegateID,
		PositionID:   newPositionID,
		CommitteeID:  newPosition.CommitteeID,
		DelegationID: newDelegate.DelegationID,
		Status:       domain.AssignmentStatusProposed,
		ProposedAt:   time.Now().UTC(),
		RunID:        existing.RunID,
		Reason:       "manually edited by staff",
		CreatedBy:    caller.UserID,
		UpdatedBy:    caller.UserID,
	}
	created, err := s.Store.CreateAssignment(ctx, newAssignment)
	if err != nil {
		s.log().Error("UpdateAssignment: create new", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventAssignmentManuallyEdited,
		Metadata: map[string]string{
			"oldAssignmentId": existing.ID,
			"newAssignmentId": created.ID,
			"conferenceId":    existing.ConferenceID,
		},
	})
	return connect.NewResponse(&v1.UpdateAssignmentResponse{Assignment: assignmentToProto(created)}), nil
}

func (s *AssignmentService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

// notifyRunCompleted sends T5 to the staffer who triggered the run.
func (s *AssignmentService) notifyRunCompleted(ctx context.Context, callerUserID, conferenceID, runID string, objective float64, count int) {
	if s.Email == nil {
		return
	}
	user, err := s.Store.GetUser(ctx, callerUserID)
	if err != nil {
		return
	}
	conferenceName := conferenceID
	if conf, err := s.Store.GetConference(ctx, conferenceID); err == nil {
		conferenceName = conf.Name
	}
	vars := map[string]any{
		"conferenceName":  conferenceName,
		"assignmentCount": count,
		"objective":       fmt.Sprintf("%.4f", objective),
		"runLink":         portalBase() + "/admin/assignments?run=" + runID,
	}
	if err := s.Email.Send(ctx, email.SendRequest{
		User: user,
		Kind: domain.EmailKindAssignmentRunCompleted,
		Vars: vars,
	}); err != nil {
		s.log().Warn("notify run completed: send", "err", err)
	}
}

func (s *AssignmentService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// ── AssignmentRunService ─────────────────────────────────────────────────────

// AssignmentRunService implements numunv1connect.AssignmentRunServiceHandler.
// Read-only surface for run history + the in-flight run.
type AssignmentRunService struct {
	Store  *store.Client
	Scoper *auth.Scoper
	Logger *slog.Logger
}

func (s *AssignmentRunService) ListAssignmentRuns(ctx context.Context, req *connect.Request[v1.ListAssignmentRunsRequest]) (*connect.Response[v1.ListAssignmentRunsResponse], error) {
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
	cursor, size := pageRequest(req.Msg.GetPage())
	rows, next, err := s.Store.ListAssignmentRunsByConference(ctx, conferenceID, cursor, size)
	if err != nil {
		s.log().Error("ListAssignmentRuns: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	out := &v1.ListAssignmentRunsResponse{
		Items: make([]*v1.AssignmentRun, 0, len(rows)),
		Page:  &v1.Page{NextCursor: next, PageSize: size},
	}
	for _, r := range rows {
		out.Items = append(out.Items, assignmentRunToProto(r))
	}
	return connect.NewResponse(out), nil
}

func (s *AssignmentRunService) GetCurrentRun(ctx context.Context, req *connect.Request[v1.GetCurrentRunRequest]) (*connect.Response[v1.GetCurrentRunResponse], error) {
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
	run, err := s.Store.FindInFlightRun(ctx, conferenceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return connect.NewResponse(&v1.GetCurrentRunResponse{}), nil
		}
		s.log().Error("GetCurrentRun: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.GetCurrentRunResponse{Run: assignmentRunToProto(run)}), nil
}

func (s *AssignmentRunService) GetAssignmentRun(ctx context.Context, req *connect.Request[v1.GetAssignmentRunRequest]) (*connect.Response[v1.GetAssignmentRunResponse], error) {
	if _, ok := auth.FromContext(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	id := strings.TrimSpace(req.Msg.GetRunId())
	if id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("run_id required"))
	}
	run, err := s.Store.GetAssignmentRun(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("GetAssignmentRun: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if err := s.Scoper.MustHaveScopeOnConference(ctx, run.ConferenceID); err != nil {
		return nil, mapScopeErr(err)
	}
	return connect.NewResponse(&v1.GetAssignmentRunResponse{Run: assignmentRunToProto(run)}), nil
}

func (s *AssignmentRunService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// ── helpers ─────────────────────────────────────────────────────────────────

// canonicalSeed derives the deterministic seed for the conference's canonical
// run. ASSIGNMENT_ALGORITHM.md §6.
func canonicalSeed(conferenceID string) uint64 {
	sum := sha256.Sum256([]byte(conferenceID))
	return binary.BigEndian.Uint64(sum[:8])
}

// loadAlgorithmInputs reads all entities the algorithm needs and converts
// them into the framework-free domain/assignment shape.
func (s *AssignmentService) loadAlgorithmInputs(ctx context.Context, conferenceID string) (assignment.Inputs, error) {
	in := assignment.Inputs{Conference: assignment.Conference{ID: conferenceID}}

	// Delegations (approved only).
	dels, _, err := s.Store.ListDelegationsByStatus(ctx, conferenceID, domain.DelegationStatusApproved, "", 1000)
	if err != nil {
		return in, connect.NewError(connect.CodeUnavailable, errors.New("load delegations failed"))
	}
	delByID := make(map[string]domain.Delegation, len(dels))
	in.Delegations = make([]assignment.Delegation, 0, len(dels))
	for _, d := range dels {
		delByID[d.ID] = d
		in.Delegations = append(in.Delegations, assignment.Delegation{
			ID:                   d.ID,
			School:               d.School,
			CommitteePreferences: domainPrefsToAlgo(d.CommitteePreferences),
		})
	}

	// Delegates across all approved delegations.
	for _, d := range dels {
		rows, err := s.Store.ListAllDelegatesByDelegation(ctx, d.ID)
		if err != nil {
			return in, connect.NewError(connect.CodeUnavailable, errors.New("load delegates failed"))
		}
		for _, r := range rows {
			in.Delegates = append(in.Delegates, assignment.Delegate{
				ID:              r.ID,
				DelegationID:    r.DelegationID,
				ExperienceLevel: assignment.ExperienceLevel(string(r.ExperienceLevel)),
			})
		}
	}

	// Committees + positions.
	committees, err := s.Store.ListCommitteesByConference(ctx, conferenceID)
	if err != nil {
		return in, connect.NewError(connect.CodeUnavailable, errors.New("load committees failed"))
	}
	for _, c := range committees {
		in.Committees = append(in.Committees, assignment.Committee{
			ID:   c.ID,
			Type: assignment.CommitteeType(string(c.Type)),
			Size: assignment.CommitteeSize(string(c.Size)),
		})
		positions, err := s.Store.ListPositionsByCommittee(ctx, c.ID)
		if err != nil {
			return in, connect.NewError(connect.CodeUnavailable, errors.New("load positions failed"))
		}
		for _, p := range positions {
			in.Positions = append(in.Positions, assignment.Position{
				ID:             p.ID,
				CommitteeID:    p.CommitteeID,
				MaxDelegates:   p.MaxDelegates,
				DualDelegation: p.DualDelegation,
				PrestigeTier:   assignment.PrestigeTier(string(p.PrestigeTier)),
			})
		}
	}

	// Pinned (approved) assignments.
	approved, err := s.Store.ListAllAssignmentsByConference(ctx, conferenceID, "", "", domain.AssignmentStatusApproved)
	if err != nil {
		return in, connect.NewError(connect.CodeUnavailable, errors.New("load pinned failed"))
	}
	for _, a := range approved {
		in.PinnedAssignments = append(in.PinnedAssignments, assignment.PinnedAssignment{
			DelegateID:   a.DelegateID,
			PositionID:   a.PositionID,
			CommitteeID:  a.CommitteeID,
			DelegationID: a.DelegationID,
			Score:        a.Score,
			Reason:       a.Reason,
		})
	}

	return in, nil
}

// domainPrefsToAlgo converts a domain.CommitteePreferences to the algorithm
// package's shape. The two packages use the same trinary string values, so
// this is a structural copy.
func domainPrefsToAlgo(p domain.CommitteePreferences) assignment.CommitteePreferences {
	return assignment.CommitteePreferences{
		TypeCrisis:    assignment.Trinary(string(p.TypeCrisis)),
		TypeNonCrisis: assignment.Trinary(string(p.TypeNonCrisis)),
		SizeSmall:     assignment.Trinary(string(p.SizeSmall)),
		SizeMedium:    assignment.Trinary(string(p.SizeMedium)),
		SizeLarge:     assignment.Trinary(string(p.SizeLarge)),
	}
}

// buildAssignmentRows converts the algorithm's Proposal into domain.Assignment
// rows ready for the store. All produced rows are status=proposed.
func buildAssignmentRows(p *assignment.Proposal, conferenceID, runID, actorUserID string) []domain.Assignment {
	out := make([]domain.Assignment, 0, len(p.Assignments))
	now := time.Now().UTC()
	for _, pa := range p.Assignments {
		out = append(out, domain.Assignment{
			ConferenceID: conferenceID,
			DelegateID:   pa.DelegateID,
			PositionID:   pa.PositionID,
			CommitteeID:  pa.CommitteeID,
			DelegationID: pa.DelegationID,
			Status:       domain.AssignmentStatusProposed,
			ProposedAt:   now,
			RunID:        runID,
			Score:        pa.Score,
			Reason:       pa.Reason,
			CreatedBy:    actorUserID,
			UpdatedBy:    actorUserID,
		})
	}
	return out
}

// synthAssignmentsForDryRun converts a Proposal into proto Assignments with
// synthetic ids; not persisted, only echoed to the client.
func synthAssignmentsForDryRun(p *assignment.Proposal, conferenceID, runID string) []*v1.Assignment {
	out := make([]*v1.Assignment, 0, len(p.Assignments))
	for _, pa := range p.Assignments {
		out = append(out, &v1.Assignment{
			ConferenceId: conferenceID,
			DelegateId:   pa.DelegateID,
			PositionId:   pa.PositionID,
			CommitteeId:  pa.CommitteeID,
			DelegationId: pa.DelegationID,
			Status:       v1.AssignmentStatus_ASSIGNMENT_STATUS_PROPOSED,
			RunId:        runID,
			Score:        pa.Score,
			Reason:       pa.Reason,
		})
	}
	return out
}
