package store

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type scanAPI interface {
	Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// scanFirst returns up to max items matching in.FilterExpression, following LastEvaluatedKey.
// DynamoDB applies Limit before the filter, so a Limit-bounded filtered Scan can miss rows
// that exist; any Limit on in is ignored.
func scanFirst(ctx context.Context, api scanAPI, in *dynamodb.ScanInput, max int) ([]map[string]ddbtypes.AttributeValue, error) {
	req := *in
	req.Limit = nil
	var items []map[string]ddbtypes.AttributeValue
	for {
		out, err := api.Scan(ctx, &req)
		if err != nil {
			return nil, err
		}
		for _, it := range out.Items {
			items = append(items, it)
			if len(items) >= max {
				return items, nil
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return items, nil
		}
		req.ExclusiveStartKey = out.LastEvaluatedKey
	}
}
