package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// emailEventItem is the DDB on-the-wire shape for an EmailEvent row.
// Append-only; 1-year TTL via `expiresAt`. See EMAIL.md §8.
type emailEventItem struct {
	PK             string            `dynamodbav:"PK"`
	SK             string            `dynamodbav:"SK"`
	Entity         string            `dynamodbav:"entity"`
	ID             string            `dynamodbav:"id,omitempty"`
	UserID         string            `dynamodbav:"userId,omitempty"`
	RecipientEmail string            `dynamodbav:"recipientEmail"`
	Kind           string            `dynamodbav:"kind"`
	Subject        string            `dynamodbav:"subject,omitempty"`
	SenderAddress  string            `dynamodbav:"senderAddress,omitempty"`
	SESMessageID   string            `dynamodbav:"sesMessageId,omitempty"`
	Status         string            `dynamodbav:"status"`
	FailureReason  string            `dynamodbav:"failureReason,omitempty"`
	ClientToken    string            `dynamodbav:"clientToken,omitempty"`
	SentAt         string            `dynamodbav:"sentAt"`
	ExpiresAt      int64             `dynamodbav:"expiresAt"`
	Metadata       map[string]string `dynamodbav:"metadata,omitempty"`

	// GSI1: worker dedupe lookup by client token; set only when a token exists.
	GSI1PK string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK string `dynamodbav:"GSI1SK,omitempty"`
}

const emailEventTTLDays = 365

func emailTokenGSI1PK(token string) string { return "EMAIL_TOKEN#" + token }

func emailEventPK(userID, recipientEmail string) string {
	if userID != "" {
		return "USER#" + userID
	}
	return "EMAIL_FEEDBACK#" + strings.ToLower(recipientEmail)
}

// RecordEmailEvent appends one EmailEvent row. Best-effort like RecordAuthEvent.
func (c *Client) RecordEmailEvent(ctx context.Context, e domain.EmailEvent) error {
	if e.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("uuid v7: %w", err)
		}
		e.ID = id.String()
	}
	if e.SentAt.IsZero() {
		e.SentAt = time.Now().UTC()
	}
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = e.SentAt.Add(time.Hour * 24 * emailEventTTLDays)
	}
	it := emailEventItem{
		PK:             emailEventPK(e.UserID, e.RecipientEmail),
		SK:             fmt.Sprintf("EMAIL_EVENT#%s#%s", e.SentAt.Format(time.RFC3339Nano), e.ID),
		Entity:         "EmailEvent",
		ID:             e.ID,
		UserID:         e.UserID,
		RecipientEmail: e.RecipientEmail,
		Kind:           string(e.Kind),
		Subject:        e.Subject,
		SenderAddress:  e.SenderAddress,
		SESMessageID:   e.SESMessageID,
		Status:         string(e.Status),
		FailureReason:  e.FailureReason,
		ClientToken:    e.ClientToken,
		SentAt:         e.SentAt.Format(time.RFC3339Nano),
		ExpiresAt:      e.ExpiresAt.Unix(),
		Metadata:       e.Metadata,
	}
	if e.ClientToken != "" {
		it.GSI1PK = emailTokenGSI1PK(e.ClientToken)
		it.GSI1SK = it.SK
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("marshal email event: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("put email event: %w", err)
	}
	return nil
}

// FindEmailEventByClientToken looks up an existing send by its idempotency
// token. Used by the worker to skip redeliveries (EMAIL.md §5.6).
func (c *Client) FindEmailEventByClientToken(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": &ddbtypes.AttributeValueMemberS{Value: emailTokenGSI1PK(token)},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf("query email event by token: %w", err)
	}
	return len(out.Items) > 0, nil
}

// ListSuppressedUsers returns User rows whose emailStatus is bounced or
// complained. EMAIL.md §10.4.
func (c *Client) ListSuppressedUsers(ctx context.Context) ([]domain.User, error) {
	all, err := c.liveUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]domain.User, 0)
	for _, u := range all {
		if u.EmailStatus != "" && u.EmailStatus != domain.EmailStatusOK {
			users = append(users, u)
		}
	}
	return users, nil
}

// FindUserByEmail resolves a userId for a bounce/complaint notification.
// Case-insensitive.
func (c *Client) FindUserByEmail(ctx context.Context, email string) (domain.User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return domain.User{}, ErrNotFound
	}
	all, err := c.liveUsers(ctx)
	if err != nil {
		return domain.User{}, err
	}
	for _, u := range all {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return domain.User{}, ErrNotFound
}
