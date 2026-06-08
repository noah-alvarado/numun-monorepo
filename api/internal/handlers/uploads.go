// scope-check: skip — Presign is per-user, not scoped to a specific entity.

package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/store"
)

// UploadService implements numunv1connect.UploadServiceHandler. It owns the
// S3 PUT-presign flow for bulk-delegate roster uploads (BULK_IMPORT.md §7.1).
type UploadService struct {
	Store  *store.Client
	Logger *slog.Logger
}

// bulkDelegatesMaxBytes mirrors store.bulkDelegatesMaxBytes so the handler can
// reject oversize requests before paying for the S3 round-trip. Kept in lockstep
// with BULK_IMPORT.md §2.2.
const bulkDelegatesMaxBytes int64 = 5 * 1024 * 1024

// Presign mints a 10-minute presigned PUT URL for a bulk-delegate upload.
// Validates extension/content-type/size before delegating to the storage
// layer. Audits a best-effort bulk_delegates_presigned event.
func (s *UploadService) Presign(ctx context.Context, req *connect.Request[v1.PresignRequest]) (*connect.Response[v1.PresignResponse], error) {
	caller, ok := auth.FromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("no caller"))
	}

	if req.Msg.GetPurpose() != v1.UploadPurpose_UPLOAD_PURPOSE_BULK_DELEGATES {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported upload purpose"))
	}

	contentType := strings.TrimSpace(req.Msg.GetContentType())
	filename := strings.TrimSpace(req.Msg.GetFilename())
	sizeBytes := req.Msg.GetSizeBytes()

	if filename == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filename required"))
	}
	if sizeBytes <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("size_bytes must be > 0"))
	}
	if sizeBytes > bulkDelegatesMaxBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("size_bytes exceeds limit of %d", bulkDelegatesMaxBytes))
	}
	if err := validateBulkDelegatesFilename(filename, contentType); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	presigned, err := s.Store.PresignBulkDelegatesUpload(ctx, caller.UserID, contentType, sizeBytes)
	if err != nil {
		if errors.Is(err, store.ErrUnsupportedContentType) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported content type"))
		}
		s.log().Error("Presign: store", "err", err)
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("upload presign unavailable"))
	}

	s.audit(ctx, domain.AuthAuditEvent{
		UserID:      caller.UserID,
		ActorUserID: caller.UserID,
		Kind:        domain.AuthEventBulkDelegatesPresign,
		Metadata: map[string]string{
			"uploadKey":   presigned.UploadKey,
			"contentType": contentType,
			"sizeBytes":   fmt.Sprintf("%d", sizeBytes),
		},
	})

	return connect.NewResponse(&v1.PresignResponse{
		Url:       presigned.URL,
		UploadKey: presigned.UploadKey,
		Headers:   presigned.Headers,
		ExpiresAt: timestamppb.New(presigned.ExpiresAt),
	}), nil
}

// validateBulkDelegatesFilename rejects extensions that don't match the
// declared content type. `.xls` is explicitly rejected — the parser supports
// only the OOXML `.xlsx` flavour.
func validateBulkDelegatesFilename(filename, contentType string) error {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".xls") && !strings.HasSuffix(lower, ".xlsx"):
		return errors.New("legacy .xls is not supported; export to .xlsx")
	case strings.HasSuffix(lower, ".csv"):
		if contentType != store.ContentTypeCSV {
			return errors.New("filename extension does not match content_type")
		}
	case strings.HasSuffix(lower, ".xlsx"):
		if contentType != store.ContentTypeXLSX {
			return errors.New("filename extension does not match content_type")
		}
	default:
		return errors.New("filename must end in .csv or .xlsx")
	}
	return nil
}

func (s *UploadService) audit(ctx context.Context, e domain.AuthAuditEvent) {
	if s.Store == nil {
		return
	}
	if err := s.Store.RecordAuthEvent(ctx, e); err != nil {
		s.log().Warn("audit write failed", "kind", e.Kind, "err", err)
	}
}

func (s *UploadService) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
