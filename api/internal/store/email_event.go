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
	ID             string            `dynamodbav:"id"`
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
}

const emailEventTTLDays = 365

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
//
// Implemented as a Scan with a filter — keep cheap for v1 where redelivery is
// rare. If volume grows, add a GSI keyed on clientToken.
func (c *Client) FindEmailEventByClientToken(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("entity = :e AND clientToken = :ct"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e":  &ddbtypes.AttributeValueMemberS{Value: "EmailEvent"},
			":ct": &ddbtypes.AttributeValueMemberS{Value: token},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return false, fmt.Errorf("scan email events: %w", err)
	}
	return len(out.Items) > 0, nil
}

// ListSuppressedUsers returns User rows whose emailStatus is bounced or
// complained. EMAIL.md §10.4 — Scan-based at v1 NUMUN scale (~100 users).
func (c *Client) ListSuppressedUsers(ctx context.Context) ([]domain.User, error) {
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("entity = :e AND emailStatus <> :ok AND isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e":     &ddbtypes.AttributeValueMemberS{Value: "User"},
			":ok":    &ddbtypes.AttributeValueMemberS{Value: string(domain.EmailStatusOK)},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scan suppressed users: %w", err)
	}
	users := make([]domain.User, 0, len(out.Items))
	for _, raw := range out.Items {
		var it userItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal user: %w", err)
		}
		users = append(users, userFromItem(it))
	}
	return users, nil
}

// FindUserByEmail does a Scan over User rows. Used by the bounce/complaint
// Lambda to resolve a userId for a feedback notification. Acceptable at NUMUN
// scale; revisit with a GSI if it becomes hot.
func (c *Client) FindUserByEmail(ctx context.Context, email string) (domain.User, error) {
	emailLower := strings.ToLower(strings.TrimSpace(email))
	if emailLower == "" {
		return domain.User{}, ErrNotFound
	}
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("entity = :e AND #email = :addr AND isDeleted = :false"),
		ExpressionAttributeNames: map[string]string{
			"#email": "email",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e":     &ddbtypes.AttributeValueMemberS{Value: "User"},
			":addr":  &ddbtypes.AttributeValueMemberS{Value: emailLower},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("scan user by email: %w", err)
	}
	if len(out.Items) == 0 {
		// Fall back to case-sensitive match in case stored email isn't lowercased.
		out2, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String(c.Table),
			FilterExpression: aws.String("entity = :e AND #email = :addr AND isDeleted = :false"),
			ExpressionAttributeNames: map[string]string{
				"#email": "email",
			},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":e":     &ddbtypes.AttributeValueMemberS{Value: "User"},
				":addr":  &ddbtypes.AttributeValueMemberS{Value: strings.TrimSpace(email)},
				":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
			},
			Limit: aws.Int32(1),
		})
		if err != nil {
			return domain.User{}, fmt.Errorf("scan user by email: %w", err)
		}
		if len(out2.Items) == 0 {
			return domain.User{}, ErrNotFound
		}
		out = out2
	}
	var it userItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.User{}, fmt.Errorf("unmarshal user: %w", err)
	}
	return userFromItem(it), nil
}
