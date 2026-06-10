package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// announcementItem is the on-the-wire DDB shape for an Announcement row.
type announcementItem struct {
	PK             string `dynamodbav:"PK"`
	SK             string `dynamodbav:"SK"`
	Entity         string `dynamodbav:"entity"`
	ID             string `dynamodbav:"id"`
	ConferenceID   string `dynamodbav:"conferenceId,omitempty"`
	Subject        string `dynamodbav:"subject"`
	BodyHTML       string `dynamodbav:"bodyHtml"`
	BodyText       string `dynamodbav:"bodyText"`
	AudienceFilter string `dynamodbav:"audienceFilter,omitempty"`
	SentBy         string `dynamodbav:"sentBy"`
	SentAt         string `dynamodbav:"sentAt"`
	RecipientCount int    `dynamodbav:"recipientCount"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`
}

func announcementPK() string         { return "ANNOUNCEMENT#all" }
func announcementSK(id string) string { return "ANNOUNCEMENT#" + id }

// CreateAnnouncement persists the announcement record. The actual SES sends
// happen asynchronously via the worker queue (EMAIL.md §5.3).
func (c *Client) CreateAnnouncement(ctx context.Context, a domain.Announcement) (domain.Announcement, error) {
	if a.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return a, fmt.Errorf("uuid v7: %w", err)
		}
		a.ID = id.String()
	}
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	a.Version = 1
	if a.SentAt.IsZero() {
		a.SentAt = now
	}
	it := announcementItem{
		PK:             announcementPK(),
		SK:             announcementSK(a.ID),
		Entity:         "Announcement",
		ID:             a.ID,
		ConferenceID:   a.ConferenceID,
		Subject:        a.Subject,
		BodyHTML:       a.BodyHTML,
		BodyText:       a.BodyText,
		AudienceFilter: a.AudienceFilter,
		SentBy:         a.SentBy,
		SentAt:         a.SentAt.Format(time.RFC3339Nano),
		RecipientCount: a.RecipientCount,
		Version:        1,
		CreatedAt:      now.Format(time.RFC3339Nano),
		UpdatedAt:      now.Format(time.RFC3339Nano),
		CreatedBy:      a.CreatedBy,
		UpdatedBy:      a.CreatedBy,
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return a, fmt.Errorf("marshal announcement: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return a, fmt.Errorf("put announcement: %w", err)
	}
	return a, nil
}

// GetAnnouncement returns one row by id.
func (c *Client) GetAnnouncement(ctx context.Context, id string) (domain.Announcement, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: announcementPK()},
			"SK": &ddbtypes.AttributeValueMemberS{Value: announcementSK(id)},
		},
	})
	if err != nil {
		return domain.Announcement{}, fmt.Errorf("get announcement: %w", err)
	}
	if out.Item == nil {
		return domain.Announcement{}, ErrNotFound
	}
	var it announcementItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Announcement{}, fmt.Errorf("unmarshal announcement: %w", err)
	}
	if it.IsDeleted {
		return domain.Announcement{}, ErrNotFound
	}
	return announcementFromItem(it), nil
}

// ListAnnouncements returns all announcements newest-first.
func (c *Client) ListAnnouncements(ctx context.Context, cursor string, pageSize int32) ([]domain.Announcement, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		KeyConditionExpression: aws.String("PK = :pk"),
		FilterExpression:       aws.String("isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":    &ddbtypes.AttributeValueMemberS{Value: announcementPK()},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		ScanIndexForward: aws.Bool(false), // newest first (UUIDv7-ordered SK)
		Limit:            aws.Int32(pageSize),
	}
	if cursor != "" {
		start, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode cursor: %w", err)
		}
		in.ExclusiveStartKey = start
	}
	out, err := c.DDB.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("list announcements: %w", err)
	}
	items := make([]domain.Announcement, 0, len(out.Items))
	for _, raw := range out.Items {
		var it announcementItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal announcement: %w", err)
		}
		items = append(items, announcementFromItem(it))
	}
	var next string
	if len(out.LastEvaluatedKey) > 0 {
		next, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("encode cursor: %w", err)
		}
	}
	return items, next, nil
}

func announcementFromItem(it announcementItem) domain.Announcement {
	a := domain.Announcement{
		ID:             it.ID,
		ConferenceID:   it.ConferenceID,
		Subject:        it.Subject,
		BodyHTML:       it.BodyHTML,
		BodyText:       it.BodyText,
		AudienceFilter: it.AudienceFilter,
		SentBy:         it.SentBy,
		RecipientCount: it.RecipientCount,
		IsDeleted:      it.IsDeleted,
		Version:        it.Version,
		CreatedBy:      it.CreatedBy,
		UpdatedBy:      it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.SentAt); err == nil {
		a.SentAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		a.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		a.UpdatedAt = t
	}
	return a
}
