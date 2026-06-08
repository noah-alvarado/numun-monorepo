package handlers_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	"github.com/numun/numun/api/internal/auth"
	"github.com/numun/numun/api/internal/domain"
	v1 "github.com/numun/numun/api/internal/gen/numun/v1"
	"github.com/numun/numun/api/internal/handlers"
	"github.com/numun/numun/api/internal/store"
)

// uploadsCallerCtx attaches a synthetic advisor caller — UploadService.Presign
// only reads caller.UserID, so we can run input-validation tests without a
// full middleware stack.
func uploadsCallerCtx() context.Context {
	return auth.WithCaller(context.Background(), auth.Caller{
		UserID: "user-test",
		Role:   domain.RoleAdvisor,
	})
}

func newUploadsRequest(filename, ct string, size int64) *connect.Request[v1.PresignRequest] {
	return connect.NewRequest(&v1.PresignRequest{
		Purpose:     v1.UploadPurpose_UPLOAD_PURPOSE_BULK_DELEGATES,
		Filename:    filename,
		ContentType: ct,
		SizeBytes:   size,
	})
}

// emptyStore returns a store.Client with no S3/bucket configured. The
// validation paths must reject before touching the store; the size-OK path
// will reach the store and surface CodeUnavailable, which the test asserts.
func emptyStore() *store.Client { return &store.Client{} }

func TestUploadsPresign_Unauthenticated(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(context.Background(), newUploadsRequest("roster.csv", store.ContentTypeCSV, 1024))
	if err == nil {
		t.Fatal("expected error")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeUnauthenticated {
		t.Fatalf("want CodeUnauthenticated, got %v", err)
	}
}

func TestUploadsPresign_WrongPurpose(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	req := connect.NewRequest(&v1.PresignRequest{
		Purpose:     v1.UploadPurpose_UPLOAD_PURPOSE_UNSPECIFIED,
		Filename:    "roster.csv",
		ContentType: store.ContentTypeCSV,
		SizeBytes:   1024,
	})
	_, err := svc.Presign(uploadsCallerCtx(), req)
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestUploadsPresign_SizeTooBig(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.csv", store.ContentTypeCSV, 6*1024*1024))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestUploadsPresign_NonPositiveSize(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.csv", store.ContentTypeCSV, 0))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestUploadsPresign_RejectsLegacyXLS(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.xls", store.ContentTypeXLSX, 1024))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestUploadsPresign_ContentTypeMismatch(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	// .csv name with xlsx content_type → mismatch.
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.csv", store.ContentTypeXLSX, 1024))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
	// .xlsx name with csv content_type → mismatch.
	_, err = svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.xlsx", store.ContentTypeCSV, 1024))
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestUploadsPresign_UnknownExtension(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.txt", store.ContentTypeCSV, 1024))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

func TestUploadsPresign_EmptyFilename(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("", store.ContentTypeCSV, 1024))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("want CodeInvalidArgument, got %v", err)
	}
}

// TestUploadsPresign_HappyPathReachesStore proves the validation gate lets a
// well-formed request through. Because the test process has no S3 bucket
// configured, the storage call returns ErrUploadsBucketNotConfigured which
// the handler translates to CodeUnavailable. The point is to verify the
// validation layer doesn't reject the input.
func TestUploadsPresign_HappyPathReachesStore(t *testing.T) {
	svc := &handlers.UploadService{Store: emptyStore()}
	_, err := svc.Presign(uploadsCallerCtx(), newUploadsRequest("roster.csv", store.ContentTypeCSV, 1024))
	if err == nil {
		t.Fatal("expected CodeUnavailable from unconfigured store, got nil")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeUnavailable {
		t.Fatalf("want CodeUnavailable, got %v", err)
	}
}
