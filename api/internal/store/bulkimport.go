package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// ── BulkImportPreview ──────────────────────────────────────────────────────
// DATA_MODEL.md §2.17 and BULK_IMPORT.md §11.1. PK = BULK_IMPORT#<id>, SK = META.
// 30-minute TTL via DDB's expiresAt attribute.

type bulkImportPreviewItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	UserID       string `dynamodbav:"userId"`
	DelegationID string `dynamodbav:"delegationId"`
	ConferenceID string `dynamodbav:"conferenceId"`
	SourceType   string `dynamodbav:"sourceType"`
	SourceRef    string `dynamodbav:"sourceRef"`
	TabName      string `dynamodbav:"tabName,omitempty"`
	ParsedRows   []byte `dynamodbav:"parsedRows"`
	Summary      []byte `dynamodbav:"summary"`

	CreatedAt string `dynamodbav:"createdAt"`
	ExpiresAt int64  `dynamodbav:"expiresAt"`
}

const bulkImportPreviewTTL = 30 * time.Minute

func bulkImportPreviewPK(id string) string { return "BULK_IMPORT#" + id }

// PutBulkImportPreview writes a new preview row. Assigns id + timestamps when
// the caller leaves them empty. Returns the resulting domain object.
func (c *Client) PutBulkImportPreview(ctx context.Context, in domain.BulkImportPreview) (domain.BulkImportPreview, error) {
	if in.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return domain.BulkImportPreview{}, fmt.Errorf("uuid v7: %w", err)
		}
		in.ID = id.String()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	if in.ExpiresAt.IsZero() {
		in.ExpiresAt = in.CreatedAt.Add(bulkImportPreviewTTL)
	}

	it := bulkImportPreviewItem{
		PK:           bulkImportPreviewPK(in.ID),
		SK:           "META",
		Entity:       "BulkImportPreview",
		ID:           in.ID,
		UserID:       in.UserID,
		DelegationID: in.DelegationID,
		ConferenceID: in.ConferenceID,
		SourceType:   string(in.SourceType),
		SourceRef:    in.SourceRef,
		TabName:      in.TabName,
		ParsedRows:   in.ParsedRowsRaw,
		Summary:      in.SummaryRaw,
		CreatedAt:    in.CreatedAt.Format(time.RFC3339Nano),
		ExpiresAt:    in.ExpiresAt.Unix(),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.BulkImportPreview{}, fmt.Errorf("marshal preview: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return domain.BulkImportPreview{}, fmt.Errorf("put preview: %w", err)
	}
	return in, nil
}

// GetBulkImportPreview fetches by id. ErrNotFound on missing or TTL-expired
// rows. The TTL attribute is best-effort on DDB's side; the application checks
// expiresAt explicitly so a freshly-expired row reads as not-found.
func (c *Client) GetBulkImportPreview(ctx context.Context, id string) (domain.BulkImportPreview, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: bulkImportPreviewPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: "META"},
		},
	})
	if err != nil {
		return domain.BulkImportPreview{}, fmt.Errorf("get preview: %w", err)
	}
	if out.Item == nil {
		return domain.BulkImportPreview{}, ErrNotFound
	}
	var it bulkImportPreviewItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.BulkImportPreview{}, fmt.Errorf("unmarshal preview: %w", err)
	}
	if time.Now().Unix() >= it.ExpiresAt {
		return domain.BulkImportPreview{}, ErrNotFound
	}
	p := domain.BulkImportPreview{
		ID:            it.ID,
		UserID:        it.UserID,
		DelegationID:  it.DelegationID,
		ConferenceID:  it.ConferenceID,
		SourceType:    domain.BulkImportSourceType(it.SourceType),
		SourceRef:     it.SourceRef,
		TabName:       it.TabName,
		ParsedRowsRaw: it.ParsedRows,
		SummaryRaw:    it.Summary,
		ExpiresAt:     time.Unix(it.ExpiresAt, 0),
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		p.CreatedAt = t
	}
	return p, nil
}

// DeleteBulkImportPreview removes a preview row. Idempotent — missing rows
// are not an error.
func (c *Client) DeleteBulkImportPreview(ctx context.Context, id string) error {
	_, err := c.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: bulkImportPreviewPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: "META"},
		},
	})
	if err != nil {
		return fmt.Errorf("delete preview: %w", err)
	}
	return nil
}

// ── BulkImportJob ──────────────────────────────────────────────────────────
// DATA_MODEL.md §2.18 and BULK_IMPORT.md §6.4. 7-day TTL.

type bulkImportJobItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	UploadID         string `dynamodbav:"uploadId"`
	UserID           string `dynamodbav:"userId"`
	DelegationID     string `dynamodbav:"delegationId"`
	ConferenceID     string `dynamodbav:"conferenceId"`
	Mode             string `dynamodbav:"mode"`
	TotalBatches     int    `dynamodbav:"totalBatches"`
	CompletedBatches int    `dynamodbav:"completedBatches"`
	Status           string `dynamodbav:"status"`
	LastError        string `dynamodbav:"lastError,omitempty"`

	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	ExpiresAt int64  `dynamodbav:"expiresAt"`
}

const bulkImportJobTTL = 7 * 24 * time.Hour

func bulkImportJobPK(id string) string { return "BULK_IMPORT_JOB#" + id }

// PutBulkImportJob creates a new job row with status=applying.
func (c *Client) PutBulkImportJob(ctx context.Context, in domain.BulkImportJob) (domain.BulkImportJob, error) {
	if in.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return domain.BulkImportJob{}, fmt.Errorf("uuid v7: %w", err)
		}
		in.ID = id.String()
	}
	now := time.Now().UTC()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	if in.ExpiresAt.IsZero() {
		in.ExpiresAt = in.CreatedAt.Add(bulkImportJobTTL)
	}
	if in.Status == "" {
		in.Status = domain.BulkImportJobApplying
	}

	it := jobToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.BulkImportJob{}, fmt.Errorf("marshal job: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return domain.BulkImportJob{}, fmt.Errorf("put job: %w", err)
	}
	return in, nil
}

// UpdateBulkImportJob writes new status/progress fields. No optimistic lock —
// the commit handler is the only writer.
func (c *Client) UpdateBulkImportJob(ctx context.Context, j domain.BulkImportJob) error {
	j.UpdatedAt = time.Now().UTC()
	it := jobToItem(j)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return nil
}

// GetBulkImportJob fetches a job by id. ErrNotFound on missing rows.
func (c *Client) GetBulkImportJob(ctx context.Context, id string) (domain.BulkImportJob, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: bulkImportJobPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: "META"},
		},
	})
	if err != nil {
		return domain.BulkImportJob{}, fmt.Errorf("get job: %w", err)
	}
	if out.Item == nil {
		return domain.BulkImportJob{}, ErrNotFound
	}
	var it bulkImportJobItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.BulkImportJob{}, fmt.Errorf("unmarshal job: %w", err)
	}
	j := domain.BulkImportJob{
		ID:               it.ID,
		UploadID:         it.UploadID,
		UserID:           it.UserID,
		DelegationID:     it.DelegationID,
		ConferenceID:     it.ConferenceID,
		Mode:             domain.UpsertMode(it.Mode),
		TotalBatches:     it.TotalBatches,
		CompletedBatches: it.CompletedBatches,
		Status:           domain.BulkImportJobStatus(it.Status),
		LastError:        it.LastError,
		ExpiresAt:        time.Unix(it.ExpiresAt, 0),
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		j.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		j.UpdatedAt = t
	}
	return j, nil
}

func jobToItem(j domain.BulkImportJob) bulkImportJobItem {
	return bulkImportJobItem{
		PK:               bulkImportJobPK(j.ID),
		SK:               "META",
		Entity:           "BulkImportJob",
		ID:               j.ID,
		UploadID:         j.UploadID,
		UserID:           j.UserID,
		DelegationID:     j.DelegationID,
		ConferenceID:     j.ConferenceID,
		Mode:             string(j.Mode),
		TotalBatches:     j.TotalBatches,
		CompletedBatches: j.CompletedBatches,
		Status:           string(j.Status),
		LastError:        j.LastError,
		CreatedAt:        j.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:        j.UpdatedAt.Format(time.RFC3339Nano),
		ExpiresAt:        j.ExpiresAt.Unix(),
	}
}

// _ silences unused-import warnings when no error-type is used directly. The
// package uses errors.Is elsewhere in this file when extended.
var _ = errors.New
