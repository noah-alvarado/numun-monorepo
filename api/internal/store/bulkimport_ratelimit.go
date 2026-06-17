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

// BulkImportRateLimitKind distinguishes the two bulk-import operations that
// each carry their own hourly cap (10/hr each, per BULK_IMPORT.md §8.2).
type BulkImportRateLimitKind string

const (
	BulkImportRLPreview BulkImportRateLimitKind = "preview"
	BulkImportRLCommit  BulkImportRateLimitKind = "commit"
)

// bulkImportCapPerHour is the cap enforced by IncrBulkImportHourlyCounter for
// each kind. Matches BULK_IMPORT.md §8.2.
const bulkImportCapPerHour = 10

// ErrBulkImportRateLimitExceeded is returned by IncrBulkImportHourlyCounter
// when the caller has hit the per-kind hourly cap.
var ErrBulkImportRateLimitExceeded = errors.New("bulk-import hourly cap reached")

// IncrBulkImportHourlyCounter atomically increments the per-user hourly
// counter for the given kind. Returns ErrBulkImportRateLimitExceeded when the
// post-increment count would exceed the documented cap.
//
// Key shape: PK = `USER#<userId>#BULK_IMPORT_HOUR#<floor(unix/3600)>#<kind>`,
// SK = `META`. TTL set to two hours after window start so the row is dropped
// once expired. Window collapses on the hour boundary — a caller who spends
// 10 attempts at 12:59 has a fresh 10 starting at 13:00. Acceptable: the
// purpose is misuse protection, not perfectly smooth distribution.
func (c *Client) IncrBulkImportHourlyCounter(ctx context.Context, userID string, kind BulkImportRateLimitKind) error {
	now := time.Now().UTC()
	hour := now.Unix() / 3600
	expires := time.Unix((hour+2)*3600, 0).Unix()
	pk := fmt.Sprintf("USER#%s#BULK_IMPORT_HOUR#%d#%s", userID, hour, kind)

	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: pk},
			"SK": &ddbtypes.AttributeValueMemberS{Value: "META"},
		},
		// Allow when count is absent or under the cap. The post-increment
		// value lands at most at the cap, so the cap-th attempt succeeds and
		// the cap+1-th fails.
		ConditionExpression: aws.String("attribute_not_exists(#c) OR #c < :cap"),
		UpdateExpression:    aws.String("ADD #c :one SET expiresAt = :exp, entity = :ent"),
		ExpressionAttributeNames: map[string]string{
			"#c": "count",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":one": &ddbtypes.AttributeValueMemberN{Value: "1"},
			":cap": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", bulkImportCapPerHour)},
			":exp": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expires)},
			":ent": &ddbtypes.AttributeValueMemberS{Value: "BulkImportRateLimit"},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrBulkImportRateLimitExceeded
		}
		return fmt.Errorf("incr bulk-import counter: %w", err)
	}
	return nil
}
