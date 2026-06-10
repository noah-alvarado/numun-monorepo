package store

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/numun/numun/api/internal/domain"
)

// ListUsersByRole returns all non-deleted User rows with the given role.
// Scan-based; acceptable at v1 NUMUN scale (~100 users). EMAIL.md §7.2 uses
// this to resolve the staff-admin recipients of the new-registration summary.
func (c *Client) ListUsersByRole(ctx context.Context, role domain.Role) ([]domain.User, error) {
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("entity = :e AND #role = :r AND isDeleted = :false"),
		ExpressionAttributeNames: map[string]string{
			"#role": "role",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e":     &ddbtypes.AttributeValueMemberS{Value: "User"},
			":r":     &ddbtypes.AttributeValueMemberS{Value: string(role)},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scan users by role: %w", err)
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
