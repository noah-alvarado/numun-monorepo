package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/httpclient"
	"github.com/numun/numun/api/internal/parse"
	"github.com/numun/numun/api/internal/store"
)

// transactBatchSize is the per-TransactWriteItems cap. DDB allows 100 ops max;
// we stay at the spec-quoted 25 (BATCH_WRITE-friendly) for breathing room and
// honest large-import handling per BULK_IMPORT.md §6.4.
const transactBatchSize = 25

// PreviewUpsertDelegatesBulk parses + validates an uploaded roster, computes
// roster matches, persists a 30-minute cache row, and returns the preview.
// See BULK_IMPORT.md §4.1.
func (s *DelegateService) PreviewUpsertDelegatesBulk(ctx context.Context, req *connect.Request[v1.PreviewUpsertDelegatesBulkRequest]) (*connect.Response[v1.PreviewUpsertDelegatesBulkResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}
	parent, err := s.Store.FindDelegationByID(ctx, delID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		s.log().Error("PreviewUpsertDelegatesBulk: load delegation", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	sourceType, sourceRef, parseReq, err := s.buildParseRequest(ctx, caller.UserID, req.Msg)
	if err != nil {
		return nil, err
	}

	result, err := parse.Parse(parseReq)
	if err != nil {
		if errors.Is(err, parse.ErrInvalidArgument) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(strippedParseError(err)))
		}
		s.log().Error("PreviewUpsertDelegatesBulk: parse", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("parse failed"))
	}

	// Multi-tab path: return tabs only, no caching.
	if len(result.AvailableTabs) > 0 {
		return connect.NewResponse(&v1.PreviewUpsertDelegatesBulkResponse{
			AvailableTabs: result.AvailableTabs,
		}), nil
	}

	roster, err := s.Store.ListAllDelegatesByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("PreviewUpsertDelegatesBulk: list roster", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	computeRosterMatches(result.Rows, roster, result.Summary)

	// Persist preview cache row. ParsedRows + Summary serialized as protojson.
	rowsJSON, err := marshalPreviewRows(result.Rows)
	if err != nil {
		s.log().Error("PreviewUpsertDelegatesBulk: marshal rows", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("preview cache encode failed"))
	}
	summaryJSON, err := protoMarshal(result.Summary)
	if err != nil {
		s.log().Error("PreviewUpsertDelegatesBulk: marshal summary", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("preview cache encode failed"))
	}

	preview := domain.BulkImportPreview{
		UserID:        caller.UserID,
		DelegationID:  delID,
		ConferenceID:  parent.ConferenceID,
		SourceType:    sourceType,
		SourceRef:     sourceRef,
		TabName:       parseReq.TabName,
		ParsedRowsRaw: rowsJSON,
		SummaryRaw:    summaryJSON,
	}
	stored, err := s.Store.PutBulkImportPreview(ctx, preview)
	if err != nil {
		s.log().Error("PreviewUpsertDelegatesBulk: put preview", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventBulkImportPreviewed,
		Metadata: map[string]string{
			"delegationId": delID,
			"sourceType":   string(sourceType),
			"parsedCount":  fmtVersion(int(result.Summary.GetParsedCount())),
			"errorCount":   fmtVersion(int(result.Summary.GetErrorCount())),
		},
	})

	return connect.NewResponse(&v1.PreviewUpsertDelegatesBulkResponse{
		UploadId: stored.ID,
		Rows:     result.Rows,
		Summary:  result.Summary,
	}), nil
}

// buildParseRequest derives the parser input from the proto request, fetching
// from S3 for upload sources and constructing a safe HTTP client for the
// Google Sheets source. It also returns the BulkImportPreview-compatible
// sourceType + sourceRef.
func (s *DelegateService) buildParseRequest(ctx context.Context, callerUserID string, req *v1.PreviewUpsertDelegatesBulkRequest) (domain.BulkImportSourceType, string, parse.ParseRequest, error) {
	switch src := req.GetSource().(type) {
	case *v1.PreviewUpsertDelegatesBulkRequest_Upload:
		u := src.Upload
		key := strings.TrimSpace(u.GetUploadKey())
		if key == "" {
			return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeInvalidArgument, errors.New("upload_key required"))
		}
		if !store.UploadKeyBelongsTo(key, callerUserID) {
			return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeNotFound, errors.New("not found"))
		}
		body, err := s.Store.GetUploadedObject(ctx, key)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeNotFound, errors.New("upload not found"))
			}
			s.log().Error("buildParseRequest: get object", "err", err)
			return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeUnavailable, errors.New("upload fetch failed"))
		}
		// body is closed by the parser once it has consumed the stream;
		// the parser's xlsx/csv layers read to EOF.
		defer func() { _ = body.Close() }()
		buf, err := io.ReadAll(body)
		if err != nil {
			s.log().Error("buildParseRequest: read object", "err", err)
			return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeUnavailable, errors.New("upload fetch failed"))
		}
		fmt := u.GetFormat()
		sourceType := domain.BulkImportSourceCSV
		if fmt == v1.SourceFormat_SOURCE_FORMAT_XLSX {
			sourceType = domain.BulkImportSourceXLSX
		}
		return sourceType, key, parse.ParseRequest{
			Reader:  bytesReader(buf),
			Format:  fmt,
			TabName: strings.TrimSpace(u.GetTabName()),
		}, nil

	case *v1.PreviewUpsertDelegatesBulkRequest_GoogleSheet:
		g := src.GoogleSheet
		url := strings.TrimSpace(g.GetUrl())
		if url == "" {
			return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeInvalidArgument, errors.New("url required"))
		}
		return domain.BulkImportSourceGoogleSheet, url, parse.ParseRequest{
			SheetURL:   url,
			HTTPClient: httpclient.New(),
			TabName:    strings.TrimSpace(g.GetTabName()),
		}, nil
	}
	return "", "", parse.ParseRequest{}, connect.NewError(connect.CodeInvalidArgument, errors.New("source required"))
}

// UpsertDelegatesBulk applies the previewed import. Re-validates the rows,
// recomputes matches against the live roster, then writes through DDB
// TransactWriteItems batches. See BULK_IMPORT.md §4.2 and §6.4.
func (s *DelegateService) UpsertDelegatesBulk(ctx context.Context, req *connect.Request[v1.UpsertDelegatesBulkRequest]) (*connect.Response[v1.UpsertDelegatesBulkResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	uploadID := strings.TrimSpace(req.Msg.GetUploadId())
	delID := strings.TrimSpace(req.Msg.GetDelegationId())
	if uploadID == "" || delID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("upload_id and delegation_id required"))
	}
	if err := s.Scoper.MustHaveScopeOnDelegation(ctx, delID); err != nil {
		return nil, mapScopeErr(err)
	}

	preview, err := s.Store.GetBulkImportPreview(ctx, uploadID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("preview not found or expired"))
		}
		s.log().Error("UpsertDelegatesBulk: load preview", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if preview.UserID != caller.UserID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("preview not found"))
	}
	if preview.DelegationID != delID {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("delegation_id mismatch with preview"))
	}

	mode := domainUpsertMode(req.Msg.GetMode())
	roster, err := s.Store.ListAllDelegatesByDelegation(ctx, delID)
	if err != nil {
		s.log().Error("UpsertDelegatesBulk: list roster", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	rows := req.Msg.GetRows()
	if len(rows) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rows required"))
	}
	if len(rows) > parse.MaxRows {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("row count exceeds maximum"))
	}

	// Re-validate every row using the parser's row builder. Build synthetic
	// PreviewRows from the supplied DelegateInputs.
	previewRows := make([]*v1.PreviewRow, 0, len(rows))
	for i, in := range rows {
		previewRows = append(previewRows, &v1.PreviewRow{
			RowNumber: int32(i + 1),
			Input:     in,
			Errors:    validateDelegateInput(in),
		})
	}
	// Re-detect same-upload conflicts by dedupe key.
	detectInPlaceConflicts(previewRows)
	for _, r := range previewRows {
		if len(r.Errors) > 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("row %d has errors", r.RowNumber))
		}
		if _, isConflict := r.Match.(*v1.PreviewRow_Conflict); isConflict {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("row %d conflicts with another row", r.RowNumber))
		}
	}

	// Recompute matches against current roster.
	summary := &v1.PreviewSummary{ParsedCount: int32(len(previewRows))}
	computeRosterMatches(previewRows, roster, summary)

	creates, updates, softDeletes := planWrites(previewRows, roster, mode, caller.UserID, preview.ConferenceID, delID)
	totalOps := len(creates) + len(updates) + len(softDeletes)

	if totalOps == 0 {
		// Idempotent no-op commit; still delete the preview cache.
		_ = s.Store.DeleteBulkImportPreview(ctx, uploadID)
		return connect.NewResponse(&v1.UpsertDelegatesBulkResponse{
			Summary: &v1.CommitSummary{},
		}), nil
	}

	// Small import path: single batch, true atomicity.
	if totalOps <= transactBatchSize {
		if err := s.Store.ApplyBulkImportBatch(ctx, creates, updates, softDeletes); err != nil {
			s.log().Error("UpsertDelegatesBulk: apply", "err", err)
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("apply failed"))
		}
		_ = s.Store.DeleteBulkImportPreview(ctx, uploadID)
		s.audit(ctx, domain.AuthAuditEvent{
			UserID:      caller.UserID,
			ActorUserID: caller.UserID,
			Kind:        domain.AuthEventBulkImportCommitted,
			Metadata: map[string]string{
				"delegationId":    delID,
				"mode":            string(mode),
				"createCount":     fmtVersion(len(creates)),
				"updateCount":     fmtVersion(len(updates)),
				"softDeleteCount": fmtVersion(len(softDeletes)),
				"uploadId":        uploadID,
			},
		})
		return connect.NewResponse(&v1.UpsertDelegatesBulkResponse{
			Summary: &v1.CommitSummary{
				CreateCount:     int32(len(creates)),
				UpdateCount:     int32(len(updates)),
				SoftDeleteCount: int32(len(softDeletes)),
			},
		}), nil
	}

	// Large import: persist a BulkImportJob row and run batches sequentially.
	batches := chunkOps(creates, updates, softDeletes, transactBatchSize)
	job := domain.BulkImportJob{
		UploadID:     uploadID,
		UserID:       caller.UserID,
		DelegationID: delID,
		ConferenceID: preview.ConferenceID,
		Mode:         mode,
		TotalBatches: len(batches),
		Status:       domain.BulkImportJobApplying,
	}
	persistedJob, err := s.Store.PutBulkImportJob(ctx, job)
	if err != nil {
		s.log().Error("UpsertDelegatesBulk: put job", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}

	completed, lastErr := s.runBatches(ctx, persistedJob, batches)
	persistedJob.CompletedBatches = completed
	if lastErr != nil {
		persistedJob.Status = domain.BulkImportJobFailed
		persistedJob.LastError = lastErr.Error()
	} else {
		persistedJob.Status = domain.BulkImportJobComplete
	}
	if err := s.Store.UpdateBulkImportJob(ctx, persistedJob); err != nil {
		s.log().Error("UpsertDelegatesBulk: update job", "err", err)
	}
	if lastErr == nil {
		_ = s.Store.DeleteBulkImportPreview(ctx, uploadID)
	}

	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventBulkImportCommitted,
		Metadata: map[string]string{
			"delegationId":     delID,
			"mode":             string(mode),
			"createCount":      fmtVersion(len(creates)),
			"updateCount":      fmtVersion(len(updates)),
			"softDeleteCount":  fmtVersion(len(softDeletes)),
			"uploadId":         uploadID,
			"bulkImportJobId":  persistedJob.ID,
			"totalBatches":     fmtVersion(persistedJob.TotalBatches),
			"completedBatches": fmtVersion(persistedJob.CompletedBatches),
			"status":           string(persistedJob.Status),
		},
	})

	resp := &v1.UpsertDelegatesBulkResponse{
		Summary: &v1.CommitSummary{
			CreateCount:      int32(len(creates)),
			UpdateCount:      int32(len(updates)),
			SoftDeleteCount:  int32(len(softDeletes)),
			BulkImportJobId:  persistedJob.ID,
			TotalBatches:     int32(persistedJob.TotalBatches),
			CompletedBatches: int32(persistedJob.CompletedBatches),
			Status:           string(persistedJob.Status),
			LastError:        persistedJob.LastError,
		},
	}
	if lastErr != nil {
		return connect.NewResponse(resp), connect.NewError(connect.CodeAborted, errors.New("partial failure — resume via ResumeBulkImport"))
	}
	return connect.NewResponse(resp), nil
}

// DeleteBulkImportPreview deletes a cached preview. Idempotent.
func (s *DelegateService) DeleteBulkImportPreview(ctx context.Context, req *connect.Request[v1.DeleteBulkImportPreviewRequest]) (*connect.Response[v1.DeleteBulkImportPreviewResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	uploadID := strings.TrimSpace(req.Msg.GetUploadId())
	if uploadID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("upload_id required"))
	}
	// Verify ownership before deleting; missing rows are treated as success.
	prev, err := s.Store.GetBulkImportPreview(ctx, uploadID)
	if err == nil && prev.UserID != caller.UserID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	if err := s.Store.DeleteBulkImportPreview(ctx, uploadID); err != nil {
		s.log().Error("DeleteBulkImportPreview: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	return connect.NewResponse(&v1.DeleteBulkImportPreviewResponse{}), nil
}

// ResumeBulkImport re-runs the remaining batches of a failed large import.
// See BULK_IMPORT.md §6.4 and §13 (open item — minimal portal affordance).
func (s *DelegateService) ResumeBulkImport(ctx context.Context, req *connect.Request[v1.ResumeBulkImportRequest]) (*connect.Response[v1.ResumeBulkImportResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}
	jobID := strings.TrimSpace(req.Msg.GetBulkImportJobId())
	if jobID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bulk_import_job_id required"))
	}
	job, err := s.Store.GetBulkImportJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("job not found"))
		}
		s.log().Error("ResumeBulkImport: load job", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	if job.UserID != caller.UserID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	if job.Status == domain.BulkImportJobComplete {
		return connect.NewResponse(&v1.ResumeBulkImportResponse{
			Summary: &v1.CommitSummary{
				BulkImportJobId:  job.ID,
				TotalBatches:     int32(job.TotalBatches),
				CompletedBatches: int32(job.CompletedBatches),
				Status:           string(job.Status),
			},
		}), nil
	}

	// Resume requires the cached preview rows. If the preview has expired,
	// the caller must re-upload.
	preview, err := s.Store.GetBulkImportPreview(ctx, job.UploadID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("preview expired — re-upload required"))
		}
		s.log().Error("ResumeBulkImport: load preview", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	previewRows, err := unmarshalPreviewRows(preview.ParsedRowsRaw)
	if err != nil {
		s.log().Error("ResumeBulkImport: decode preview", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("preview decode failed"))
	}
	roster, err := s.Store.ListAllDelegatesByDelegation(ctx, job.DelegationID)
	if err != nil {
		s.log().Error("ResumeBulkImport: list roster", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("store unavailable"))
	}
	creates, updates, softDeletes := planWrites(previewRows, roster, job.Mode, caller.UserID, job.ConferenceID, job.DelegationID)
	batches := chunkOps(creates, updates, softDeletes, transactBatchSize)
	if job.CompletedBatches >= len(batches) {
		job.Status = domain.BulkImportJobComplete
		_ = s.Store.UpdateBulkImportJob(ctx, job)
		_ = s.Store.DeleteBulkImportPreview(ctx, job.UploadID)
		return connect.NewResponse(&v1.ResumeBulkImportResponse{
			Summary: &v1.CommitSummary{
				BulkImportJobId:  job.ID,
				TotalBatches:     int32(job.TotalBatches),
				CompletedBatches: int32(job.CompletedBatches),
				Status:           string(job.Status),
			},
		}), nil
	}
	remaining := batches[job.CompletedBatches:]
	completed, lastErr := s.runBatches(ctx, job, remaining)
	job.CompletedBatches += completed
	if lastErr != nil {
		job.Status = domain.BulkImportJobFailed
		job.LastError = lastErr.Error()
	} else {
		job.Status = domain.BulkImportJobComplete
		job.LastError = ""
	}
	if err := s.Store.UpdateBulkImportJob(ctx, job); err != nil {
		s.log().Error("ResumeBulkImport: update job", "err", err)
	}
	if lastErr == nil {
		_ = s.Store.DeleteBulkImportPreview(ctx, job.UploadID)
	}

	resp := &v1.ResumeBulkImportResponse{
		Summary: &v1.CommitSummary{
			BulkImportJobId:  job.ID,
			TotalBatches:     int32(job.TotalBatches),
			CompletedBatches: int32(job.CompletedBatches),
			Status:           string(job.Status),
			LastError:        job.LastError,
		},
	}
	if lastErr != nil {
		return connect.NewResponse(resp), connect.NewError(connect.CodeAborted, errors.New("partial failure"))
	}
	return connect.NewResponse(resp), nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// runBatches applies the supplied batches sequentially, returning the count
// applied and the first error encountered.
func (s *DelegateService) runBatches(ctx context.Context, _ domain.BulkImportJob, batches []bulkImportBatch) (int, error) {
	for i, b := range batches {
		if err := s.Store.ApplyBulkImportBatch(ctx, b.Creates, b.Updates, b.SoftDeletes); err != nil {
			return i, err
		}
	}
	return len(batches), nil
}

// bulkImportBatch carries one chunk of ops sized to transactBatchSize.
type bulkImportBatch struct {
	Creates     []domain.Delegate
	Updates     []store.DelegateUpdate
	SoftDeletes []store.DelegateSoftDelete
}

// chunkOps splits the full op set into batches of at most maxSize. Creates,
// updates, and soft-deletes are interleaved to keep each batch independent.
func chunkOps(creates []domain.Delegate, updates []store.DelegateUpdate, softDeletes []store.DelegateSoftDelete, maxSize int) []bulkImportBatch {
	all := []func(b *bulkImportBatch){}
	for _, c := range creates {
		c := c
		all = append(all, func(b *bulkImportBatch) { b.Creates = append(b.Creates, c) })
	}
	for _, u := range updates {
		u := u
		all = append(all, func(b *bulkImportBatch) { b.Updates = append(b.Updates, u) })
	}
	for _, d := range softDeletes {
		d := d
		all = append(all, func(b *bulkImportBatch) { b.SoftDeletes = append(b.SoftDeletes, d) })
	}
	var batches []bulkImportBatch
	for i := 0; i < len(all); i += maxSize {
		end := i + maxSize
		if end > len(all) {
			end = len(all)
		}
		batch := bulkImportBatch{}
		for _, fn := range all[i:end] {
			fn(&batch)
		}
		batches = append(batches, batch)
	}
	return batches
}

// computeRosterMatches sets PreviewRow.Match to Create or Update against the
// live roster. Same-upload Conflict matches are preserved if already set.
// Also populates summary's create/update/match-by-* counts and (for full-sync)
// soft-delete count is left to planWrites.
func computeRosterMatches(rows []*v1.PreviewRow, roster []domain.Delegate, summary *v1.PreviewSummary) {
	byKey := make(map[string]domain.Delegate, len(roster))
	for _, d := range roster {
		byKey[delegateDedupeKey(d)] = d
	}
	for _, r := range rows {
		if _, isConflict := r.Match.(*v1.PreviewRow_Conflict); isConflict {
			continue
		}
		key := parse.DedupeKey(r.Input)
		match, ok := byKey[key]
		if !ok {
			r.Match = &v1.PreviewRow_Create{Create: &v1.PreviewRow_CreateMatch{}}
			summary.CreateCount++
			continue
		}
		diff := diffFields(match, r.Input)
		r.Match = &v1.PreviewRow_Update{
			Update: &v1.PreviewRow_UpdateMatch{
				ExistingDelegateId: match.ID,
				Diff:               diff,
			},
		}
		summary.UpdateCount++
		if strings.HasPrefix(key, "email:") {
			summary.MatchByEmail++
		} else if strings.HasPrefix(key, "name:") {
			summary.MatchByName++
		}
	}
}

func delegateDedupeKey(d domain.Delegate) string {
	if em := strings.TrimSpace(strings.ToLower(d.Email)); em != "" {
		return "email:" + em
	}
	name := strings.ToLower(strings.TrimSpace(d.FirstName + " " + d.LastName))
	return "name:" + collapseSpaces(name)
}

func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prev := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prev {
				b.WriteRune(' ')
			}
			prev = true
			continue
		}
		prev = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func diffFields(existing domain.Delegate, in *v1.DelegateInput) map[string]*v1.FieldDiff {
	out := map[string]*v1.FieldDiff{}
	if existing.FirstName != in.FirstName {
		out["firstName"] = &v1.FieldDiff{Old: existing.FirstName, New: in.FirstName}
	}
	if existing.LastName != in.LastName {
		out["lastName"] = &v1.FieldDiff{Old: existing.LastName, New: in.LastName}
	}
	newEmail := strings.ToLower(strings.TrimSpace(in.Email))
	oldEmail := strings.ToLower(strings.TrimSpace(existing.Email))
	if oldEmail != newEmail {
		out["email"] = &v1.FieldDiff{Old: existing.Email, New: in.Email}
	}
	newLevel := string(domainExperienceLevel(in.ExperienceLevel))
	oldLevel := string(existing.ExperienceLevel)
	if oldLevel != newLevel {
		out["experienceLevel"] = &v1.FieldDiff{Old: oldLevel, New: newLevel}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// planWrites enumerates the create/update/soft-delete operations implied by
// the matched rows + mode.
func planWrites(rows []*v1.PreviewRow, roster []domain.Delegate, mode domain.UpsertMode, actorUserID, conferenceID, delegationID string) ([]domain.Delegate, []store.DelegateUpdate, []store.DelegateSoftDelete) {
	var creates []domain.Delegate
	var updates []store.DelegateUpdate
	touched := map[string]bool{}
	for _, r := range rows {
		switch m := r.Match.(type) {
		case *v1.PreviewRow_Create:
			creates = append(creates, domain.Delegate{
				ConferenceID:    conferenceID,
				DelegationID:    delegationID,
				FirstName:       strings.TrimSpace(r.Input.GetFirstName()),
				LastName:        strings.TrimSpace(r.Input.GetLastName()),
				Email:           strings.TrimSpace(strings.ToLower(r.Input.GetEmail())),
				ExperienceLevel: domainExperienceLevel(r.Input.GetExperienceLevel()),
				CreatedBy:       actorUserID,
				UpdatedBy:       actorUserID,
			})
		case *v1.PreviewRow_Update:
			existing := findRoster(roster, m.Update.GetExistingDelegateId())
			if existing == nil {
				continue
			}
			touched[existing.ID] = true
			updates = append(updates, store.DelegateUpdate{
				ID:              existing.ID,
				DelegationID:    existing.DelegationID,
				ConferenceID:    existing.ConferenceID,
				ExpectedVersion: existing.Version,
				FirstName:       strings.TrimSpace(r.Input.GetFirstName()),
				LastName:        strings.TrimSpace(r.Input.GetLastName()),
				Email:           strings.TrimSpace(strings.ToLower(r.Input.GetEmail())),
				ExperienceLevel: domainExperienceLevel(r.Input.GetExperienceLevel()),
				ActorUserID:     actorUserID,
			})
		}
	}
	var softDeletes []store.DelegateSoftDelete
	if mode == domain.UpsertModeFullSync {
		for _, d := range roster {
			if touched[d.ID] {
				continue
			}
			softDeletes = append(softDeletes, store.DelegateSoftDelete{
				ID:              d.ID,
				DelegationID:    d.DelegationID,
				ExpectedVersion: d.Version,
				ActorUserID:     actorUserID,
			})
		}
	}
	return creates, updates, softDeletes
}

func findRoster(roster []domain.Delegate, id string) *domain.Delegate {
	for i := range roster {
		if roster[i].ID == id {
			return &roster[i]
		}
	}
	return nil
}

func domainUpsertMode(m v1.UpsertMode) domain.UpsertMode {
	switch m {
	case v1.UpsertMode_UPSERT_MODE_FULL_SYNC:
		return domain.UpsertModeFullSync
	}
	return domain.UpsertModeAdditive
}

// validateDelegateInput re-runs the parser's per-field checks at commit time.
// Inline edits in the portal may have introduced new errors.
func validateDelegateInput(in *v1.DelegateInput) []*v1.FieldViolation {
	var v []*v1.FieldViolation
	if strings.TrimSpace(in.GetFirstName()) == "" {
		v = append(v, &v1.FieldViolation{Field: "firstName", Message: "firstName must not be empty"})
	}
	if strings.TrimSpace(in.GetLastName()) == "" {
		v = append(v, &v1.FieldViolation{Field: "lastName", Message: "lastName must not be empty"})
	}
	if em := strings.TrimSpace(in.GetEmail()); em != "" {
		if !parse.EmailValid(em) {
			v = append(v, &v1.FieldViolation{Field: "email", Message: "invalid format"})
		}
	}
	return v
}

// detectInPlaceConflicts is a re-run of parse.detectSameUploadConflicts for
// the commit path (where rows arrive as []*v1.DelegateInput). Marks the
// .Match field of each colliding row.
func detectInPlaceConflicts(rows []*v1.PreviewRow) {
	first := map[string]int{}
	for i, r := range rows {
		key := parse.DedupeKey(r.Input)
		if key == "" {
			continue
		}
		if j, seen := first[key]; seen {
			r.Match = &v1.PreviewRow_Conflict{
				Conflict: &v1.PreviewRow_ConflictMatch{
					WithRowNumber: rows[j].RowNumber,
					Reason:        fmt.Sprintf("duplicate of row %d", rows[j].RowNumber),
				},
			}
			continue
		}
		first[key] = i
	}
}

// strippedParseError peels the "invalid argument: " prefix off a wrapped
// parse error so the Connect message reads naturally.
func strippedParseError(err error) string {
	msg := err.Error()
	const prefix = "invalid argument: "
	if strings.HasPrefix(msg, prefix) {
		return msg[len(prefix):]
	}
	return msg
}

// marshalPreviewRows serializes rows as a JSON array of protojson-style
// payloads. Stored opaquely on the BulkImportPreview cache row; only this
// package reads them back.
func marshalPreviewRows(rows []*v1.PreviewRow) ([]byte, error) {
	wire := make([][]byte, 0, len(rows))
	for _, r := range rows {
		b, err := proto.Marshal(r)
		if err != nil {
			return nil, err
		}
		wire = append(wire, b)
	}
	return json.Marshal(wire)
}

func unmarshalPreviewRows(raw []byte) ([]*v1.PreviewRow, error) {
	var wire [][]byte
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	out := make([]*v1.PreviewRow, 0, len(wire))
	for _, b := range wire {
		var r v1.PreviewRow
		if err := proto.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, nil
}

func protoMarshal(m proto.Message) ([]byte, error) { return proto.Marshal(m) }

// bytesReader wraps a []byte in an io.Reader without dragging in bytes.Buffer.
type bytesBuf struct {
	b   []byte
	pos int
}

func (r *bytesBuf) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}

func bytesReader(b []byte) io.Reader { return &bytesBuf{b: b} }
