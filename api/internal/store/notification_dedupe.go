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

	"github.com/numun/numun/api/internal/domain"
)

// notificationDedupeItem is the EMAIL.md §7.1 dedupe row. Conditional Put
// with `attribute_not_exists(PK)` ensures only the first writer per window
// wins; the TTL drops the row when the window closes.
type notificationDedupeItem struct {
	PK              string `dynamodbav:"PK"`
	SK              string `dynamodbav:"SK"`
	Entity          string `dynamodbav:"entity"`
	Kind            string `dynamodbav:"kind"`
	ScopeID         string `dynamodbav:"scopeId"`
	WindowStartedAt string `dynamodbav:"windowStartedAt"`
	ExpiresAt       int64  `dynamodbav:"expiresAt"`
}

func dedupePK(kind domain.NotificationDedupeKind, scopeID string) string {
	return fmt.Sprintf("NOTIFY_DEDUPE#%s#%s", string(kind), scopeID)
}

// AcquireNotificationDedupe attempts a first-writer-wins PutItem. Returns
// `true` when this writer opens the window; `false` when an existing row
// already covers it.
func (c *Client) AcquireNotificationDedupe(ctx context.Context, kind domain.NotificationDedupeKind, scopeID string, windowDuration time.Duration) (bool, time.Time, error) {
	now := time.Now().UTC()
	expires := now.Add(windowDuration)
	it := notificationDedupeItem{
		PK:              dedupePK(kind, scopeID),
		SK:              "META",
		Entity:          "NotificationDedupe",
		Kind:            string(kind),
		ScopeID:         scopeID,
		WindowStartedAt: now.Format(time.RFC3339Nano),
		ExpiresAt:       expires.Unix(),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("marshal dedupe: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return false, time.Time{}, nil
		}
		return false, time.Time{}, fmt.Errorf("put dedupe: %w", err)
	}
	return true, now, nil
}
