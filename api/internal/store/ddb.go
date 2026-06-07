// Package store implements the DynamoDB single-table data layer.
//
// One *Client wraps the DDB client + table name; every repository is a method
// receiver on that client so calling code only needs to thread one handle.
package store

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const defaultTableName = "numun-prod"

// Client holds the DynamoDB client + the resolved table name. Construct one
// per process at startup and share across handlers.
type Client struct {
	DDB   *dynamodb.Client
	Table string
}

// New builds a Client from the ambient AWS config. When AWS_ENDPOINT_URL_DYNAMODB
// is set (the `make dev` path), the SDK auto-routes calls there.
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
		tbl = defaultTableName
	}
	return &Client{DDB: ddb, Table: tbl}, nil
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
