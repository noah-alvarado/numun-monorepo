package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// authAuditEventItem is the on-the-wire DDB shape for an AuthAuditEvent row.
// Append-only; not version-locked, not soft-deleted (DATA_MODEL.md §2.16).
type authAuditEventItem struct {
	PK          string            `dynamodbav:"PK"`
	SK          string            `dynamodbav:"SK"`
	Entity      string            `dynamodbav:"entity"`
	ID          string            `dynamodbav:"id"`
	UserID      string            `dynamodbav:"userId"`
	ActorUserID string            `dynamodbav:"actorUserId,omitempty"`
	Kind        string            `dynamodbav:"kind"`
	IP          string            `dynamodbav:"ip,omitempty"`
	UserAgent   string            `dynamodbav:"userAgent,omitempty"`
	OccurredAt  string            `dynamodbav:"occurredAt"`
	Metadata    map[string]string `dynamodbav:"metadata,omitempty"`
	ExpiresAt   int64             `dynamodbav:"expiresAt"`
}

const auditTTLDays = 365

// RecordAuthEvent writes an AuthAuditEvent row. Best-effort: callers should
// log but not fail the user-facing operation if this write errors.
func (c *Client) RecordAuthEvent(ctx context.Context, e domain.AuthAuditEvent) error {
	if e.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("uuid v7: %w", err)
		}
		e.ID = id.String()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = e.OccurredAt.Add(time.Hour * 24 * auditTTLDays)
	}
	if e.ActorUserID == "" {
		e.ActorUserID = e.UserID
	}

	it := authAuditEventItem{
		// AUTH.md §13.2: keyed under USER#<userId> so a single Query returns
		// a user's auth history. When the subject is unknown (sign_in_failed
		// before we resolve a userId), the caller passes a synthetic
		// identifier; the row still indexes under that.
		PK:          "USER#" + e.UserID,
		SK:          fmt.Sprintf("AUTH_EVENT#%s#%s", e.OccurredAt.Format(time.RFC3339Nano), e.ID),
		Entity:      "AuthAuditEvent",
		ID:          e.ID,
		UserID:      e.UserID,
		ActorUserID: e.ActorUserID,
		Kind:        string(e.Kind),
		IP:          e.IP,
		UserAgent:   e.UserAgent,
		OccurredAt:  e.OccurredAt.Format(time.RFC3339Nano),
		Metadata:    e.Metadata,
		ExpiresAt:   e.ExpiresAt.Unix(),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("marshal audit event: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("put audit event: %w", err)
	}
	return nil
}
