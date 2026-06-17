package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ErrIdempotencyInFlight is returned by AcquireIdempotencyLock when an earlier
// call with the same Idempotency-Key is still within its in-flight TTL.
var ErrIdempotencyInFlight = errors.New("duplicate request in flight")

// IdempotencyLockTTL is the in-flight-lock window. 60 seconds covers the
// human double-click and most flaky-network retries; later retries are
// allowed through (see API.md §8).
const IdempotencyLockTTL = 60 * time.Second

// AcquireIdempotencyLock writes a short-TTL marker on `IDEMPOTENCY#<key>` via a
// conditional PutItem. Returns ErrIdempotencyInFlight when a prior writer
// holds the lock. The lock is not released on success — it expires naturally
// via the table's TTL attribute, which keeps the implementation cheap (no
// second write on the success path).
func (c *Client) AcquireIdempotencyLock(ctx context.Context, key, userID string) error {
	now := time.Now().UTC()
	expires := now.Add(IdempotencyLockTTL).Unix()

	_, err := c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item: map[string]ddbtypes.AttributeValue{
			"PK":         &ddbtypes.AttributeValueMemberS{Value: "IDEMPOTENCY#" + key},
			"SK":         &ddbtypes.AttributeValueMemberS{Value: "META"},
			"entity":     &ddbtypes.AttributeValueMemberS{Value: "IdempotencyLock"},
			"userId":     &ddbtypes.AttributeValueMemberS{Value: userID},
			"acquiredAt": &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
			"expiresAt":  &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expires)},
		},
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrIdempotencyInFlight
		}
		return fmt.Errorf("acquire idempotency lock: %w", err)
	}
	return nil
}
