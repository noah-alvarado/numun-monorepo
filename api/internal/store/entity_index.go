package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// gsiEntityID indexes every row on its `entity` and `id` attributes
// (KEYS_ONLY). DATA_MODEL.md §5.
const gsiEntityID = "GSI3"

// batchGetLimit is DynamoDB's BatchGetItem cap per request.
const batchGetLimit = 100

const maxBatchGetRetries = 5

func isDeletedItem(item map[string]ddbtypes.AttributeValue) bool {
	v, ok := item["isDeleted"].(*ddbtypes.AttributeValueMemberBOOL)
	return ok && v.Value
}

func baseKey(item map[string]ddbtypes.AttributeValue) map[string]ddbtypes.AttributeValue {
	return map[string]ddbtypes.AttributeValue{"PK": item["PK"], "SK": item["SK"]}
}

// getLiveByEntityID resolves a row by entity type and id via GSI3, then reads
// it from the base table with a consistent read. Soft-deleted rows are
// reported as ErrNotFound.
func (c *Client) getLiveByEntityID(ctx context.Context, entity, id string) (map[string]ddbtypes.AttributeValue, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String(gsiEntityID),
		KeyConditionExpression: aws.String("#e = :e AND #id = :id"),
		ExpressionAttributeNames: map[string]string{
			"#e":  "entity",
			"#id": "id",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e":  &ddbtypes.AttributeValueMemberS{Value: entity},
			":id": &ddbtypes.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("query %s by id: %w", entity, err)
	}
	for _, keys := range out.Items {
		got, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:      aws.String(c.Table),
			Key:            baseKey(keys),
			ConsistentRead: aws.Bool(true),
		})
		if err != nil {
			return nil, fmt.Errorf("get %s: %w", entity, err)
		}
		if got.Item != nil && !isDeletedItem(got.Item) {
			return got.Item, nil
		}
	}
	return nil, ErrNotFound
}

// listLiveByEntity returns every non-deleted row of one entity type, ordered
// by id (UUIDv7, so creation order) ascending or descending. Intended for
// small collections (users, conferences).
func (c *Client) listLiveByEntity(ctx context.Context, entity string, newestFirst bool) ([]map[string]ddbtypes.AttributeValue, error) {
	var keys []map[string]ddbtypes.AttributeValue
	var order []string
	in := &dynamodb.QueryInput{
		TableName:                aws.String(c.Table),
		IndexName:                aws.String(gsiEntityID),
		KeyConditionExpression:   aws.String("#e = :e"),
		ExpressionAttributeNames: map[string]string{"#e": "entity"},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e": &ddbtypes.AttributeValueMemberS{Value: entity},
		},
		ScanIndexForward: aws.Bool(!newestFirst),
	}
	for {
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("query %s: %w", entity, err)
		}
		for _, k := range out.Items {
			keys = append(keys, baseKey(k))
			if s, ok := k["id"].(*ddbtypes.AttributeValueMemberS); ok {
				order = append(order, s.Value)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}

	byID := make(map[string]map[string]ddbtypes.AttributeValue, len(keys))
	for start := 0; start < len(keys); start += batchGetLimit {
		pending := keys[start:min(start+batchGetLimit, len(keys))]
		for attempt := 0; len(pending) > 0; attempt++ {
			if attempt > 0 {
				if attempt > maxBatchGetRetries {
					return nil, fmt.Errorf("batch get %s: %d keys still unprocessed", entity, len(pending))
				}
				time.Sleep(time.Duration(attempt*attempt) * 25 * time.Millisecond)
			}
			out, err := c.DDB.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
				RequestItems: map[string]ddbtypes.KeysAndAttributes{
					c.Table: {Keys: pending, ConsistentRead: aws.Bool(true)},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("batch get %s: %w", entity, err)
			}
			for _, item := range out.Responses[c.Table] {
				if isDeletedItem(item) {
					continue
				}
				if s, ok := item["id"].(*ddbtypes.AttributeValueMemberS); ok {
					byID[s.Value] = item
				}
			}
			pending = out.UnprocessedKeys[c.Table].Keys
		}
	}

	// BatchGetItem returns rows in arbitrary order; restore the index order.
	items := make([]map[string]ddbtypes.AttributeValue, 0, len(byID))
	for _, id := range order {
		if item, ok := byID[id]; ok {
			items = append(items, item)
			delete(byID, id)
		}
	}
	return items, nil
}
