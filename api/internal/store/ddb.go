// Package store implements the DynamoDB single-table data layer.
//
// One *Client wraps the DDB client + table name; every repository is a method
// receiver on that client so calling code only needs to thread one handle.
package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Client holds the DynamoDB client + the resolved table name. Construct one
// per process at startup and share across handlers.
//
// S3 + UploadsBucket are populated when UPLOADS_BUCKET_NAME is set; they
// power PresignBulkDelegatesUpload (BULK_IMPORT.md §7.1). Tests that don't
// touch uploads can leave UPLOADS_BUCKET_NAME unset and the fields stay nil.
type Client struct {
	DDB           *dynamodb.Client
	Table         string
	S3            *s3.Client
	UploadsBucket string
}

// New builds a Client from the ambient AWS config. When AWS_ENDPOINT_URL_DYNAMODB
// is set (the `make dev` path), the SDK auto-routes calls there.
//
// DDB_TABLE_NAME is required — there is no default. Per-env templates
// (infra/api/template.yaml, scripts/sam-env-vars.json, docker-compose.yml)
// inject the env-qualified name (e.g. `numun-test`). Failing fast here keeps
// a misconfigured deploy from silently writing to the wrong table.
func New(ctx context.Context) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	ddb := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		if ep := os.Getenv("AWS_ENDPOINT_URL_DYNAMODB"); ep != "" {
			o.BaseEndpoint = aws.String(ep)
		}
	})
	tbl := os.Getenv("DDB_TABLE_NAME")
	if tbl == "" {
		return nil, fmt.Errorf("store: DDB_TABLE_NAME env var is required")
	}
	c := &Client{DDB: ddb, Table: tbl}
	if bucket := os.Getenv("UPLOADS_BUCKET_NAME"); bucket != "" {
		c.S3 = s3.NewFromConfig(cfg, func(o *s3.Options) {
			if ep := os.Getenv("AWS_ENDPOINT_URL_S3"); ep != "" {
				o.BaseEndpoint = aws.String(ep)
				o.UsePathStyle = true // LocalStack / MinIO compatibility.
			}
		})
		c.UploadsBucket = bucket
	}
	return c, nil
}

// ErrNotFound is returned by repositories when the requested row does not
// exist or is soft-deleted. Handlers translate this to connect.CodeNotFound.
var ErrNotFound = errors.New("store: not found")

// ErrVersionMismatch is returned by repositories when an optimistic-lock
// conditional check fails. Handlers translate this to connect.CodeAborted.
var ErrVersionMismatch = errors.New("store: version mismatch")

// ErrAlreadyExists is returned by repositories when a create call fails the
// `attribute_not_exists(PK)` precondition.
var ErrAlreadyExists = errors.New("store: already exists")

// ErrInvariantViolation is returned when a write would violate a domain-level
// invariant — e.g., removing the last lead advisor from a Delegation. Handlers
// translate this to failed_precondition.
var ErrInvariantViolation = errors.New("store: invariant violation")

// ErrMultipleActiveConferences is returned by FindActiveConference when more
// than one row matches the active-status filter. Handlers translate this to
// failed_precondition per API.md §10.1b.
var ErrMultipleActiveConferences = errors.New("store: multiple active conferences")

// cursorPayload is the JSON shape we base64-encode for opaque pagination
// cursors. Keep it minimal — the DDB ExclusiveStartKey is just a map of
// attribute names to scalar values, so we round-trip the string subset.
type cursorPayload map[string]string

// encodeCursor turns a DDB LastEvaluatedKey into an opaque base64 string the
// portal can pass back on the next page. Only string-valued keys (PK/SK/GSI*)
// are supported — every entity in our schema uses string keys, so this is
// sufficient.
func encodeCursor(key map[string]ddbtypes.AttributeValue) (string, error) {
	if len(key) == 0 {
		return "", nil
	}
	payload := make(cursorPayload, len(key))
	for k, v := range key {
		s, ok := v.(*ddbtypes.AttributeValueMemberS)
		if !ok {
			return "", fmt.Errorf("cursor: non-string key %q", k)
		}
		payload[k] = s.Value
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(raw), nil
}

// decodeCursor reverses encodeCursor.
func decodeCursor(cursor string) (map[string]ddbtypes.AttributeValue, error) {
	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, err
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	out := make(map[string]ddbtypes.AttributeValue, len(payload))
	for k, v := range payload {
		out[k] = &ddbtypes.AttributeValueMemberS{Value: v}
	}
	return out, nil
}
