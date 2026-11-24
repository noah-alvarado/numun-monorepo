package store

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"

	"github.com/numun/numun/api/internal/domain"
)

// liveUsers returns every non-deleted User row (a few hundred at NUMUN scale)
// via GSI3; callers filter in memory.
func (c *Client) liveUsers(ctx context.Context) ([]domain.User, error) {
	items, err := c.listLiveByEntity(ctx, "User", false)
	if err != nil {
		return nil, err
	}
	users := make([]domain.User, 0, len(items))
	for _, raw := range items {
		var it userItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal user: %w", err)
		}
		users = append(users, userFromItem(it))
	}
	return users, nil
}

// ListUsersByRole returns all non-deleted User rows with the given role.
// EMAIL.md §7.2 uses this to resolve the staff-admin recipients of the
// new-registration summary.
func (c *Client) ListUsersByRole(ctx context.Context, role domain.Role) ([]domain.User, error) {
	all, err := c.liveUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]domain.User, 0, len(all))
	for _, u := range all {
		if u.Role == role {
			users = append(users, u)
		}
	}
	return users, nil
}
