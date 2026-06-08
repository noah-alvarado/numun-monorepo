package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// positionItem is the on-the-wire DDB shape for a Position row.
// PK = COMMITTEE#<committeeId>, SK = POSITION#<positionId> per DATA_MODEL.md §6.
type positionItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID   string `dynamodbav:"conferenceId"`
	CommitteeID    string `dynamodbav:"committeeId"`
	Name           string `dynamodbav:"name"`
	MaxDelegates   int    `dynamodbav:"maxDelegates"`
	DualDelegation bool   `dynamodbav:"dualDelegation"`
	PrestigeTier   string `dynamodbav:"prestigeTier"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`
}

func positionPK(committeeID string) string { return "COMMITTEE#" + committeeID }
func positionSK(id string) string          { return "POSITION#" + id }

func positionToItem(p domain.Position) positionItem {
	return positionItem{
		PK:             positionPK(p.CommitteeID),
		SK:             positionSK(p.ID),
		Entity:         "Position",
		ID:             p.ID,
		ConferenceID:   p.ConferenceID,
		CommitteeID:    p.CommitteeID,
		Name:           p.Name,
		MaxDelegates:   p.MaxDelegates,
		DualDelegation: p.DualDelegation,
		PrestigeTier:   string(p.PrestigeTier),
		IsDeleted:      p.IsDeleted,
		Version:        p.Version,
		CreatedAt:      p.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      p.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:      p.CreatedBy,
		UpdatedBy:      p.UpdatedBy,
	}
}

func positionFromItem(it positionItem) domain.Position {
	p := domain.Position{
		ID:             it.ID,
		ConferenceID:   it.ConferenceID,
		CommitteeID:    it.CommitteeID,
		Name:           it.Name,
		MaxDelegates:   it.MaxDelegates,
		DualDelegation: it.DualDelegation,
		PrestigeTier:   domain.PrestigeTier(it.PrestigeTier),
		IsDeleted:      it.IsDeleted,
		Version:        it.Version,
		CreatedBy:      it.CreatedBy,
		UpdatedBy:      it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		p.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		p.UpdatedAt = t
	}
	return p
}

func prepareNewPosition(in domain.Position) domain.Position {
	if in.ID == "" {
		if id, err := uuid.NewV7(); err == nil {
			in.ID = id.String()
		}
	}
	if in.MaxDelegates == 0 {
		in.MaxDelegates = 1
	}
	if in.PrestigeTier == "" {
		in.PrestigeTier = domain.PrestigeTierStandard
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	return in
}

// CreatePosition inserts a new Position row.
func (c *Client) CreatePosition(ctx context.Context, in domain.Position) (domain.Position, error) {
	in = prepareNewPosition(in)
	it := positionToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Position{}, fmt.Errorf("marshal position: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Position{}, ErrAlreadyExists
		}
		return domain.Position{}, fmt.Errorf("put position: %w", err)
	}
	return positionFromItem(it), nil
}

// GetPosition fetches a Position by committee + id.
func (c *Client) GetPosition(ctx context.Context, committeeID, id string) (domain.Position, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: positionPK(committeeID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: positionSK(id)},
		},
	})
	if err != nil {
		return domain.Position{}, fmt.Errorf("get position: %w", err)
	}
	if out.Item == nil {
		return domain.Position{}, ErrNotFound
	}
	var it positionItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Position{}, fmt.Errorf("unmarshal position: %w", err)
	}
	if it.IsDeleted {
		return domain.Position{}, ErrNotFound
	}
	return positionFromItem(it), nil
}

// FindPositionByID does an id-only Scan lookup; used by cross-aggregate scope
// checks where only the position id is available.
func (c *Client) FindPositionByID(ctx context.Context, id string) (domain.Position, error) {
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("#e = :entity AND id = :id AND #del = :false"),
		ExpressionAttributeNames: map[string]string{
			"#e":   "entity",
			"#del": "isDeleted",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":entity": &ddbtypes.AttributeValueMemberS{Value: "Position"},
			":id":     &ddbtypes.AttributeValueMemberS{Value: id},
			":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.Position{}, fmt.Errorf("find position: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Position{}, ErrNotFound
	}
	var it positionItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.Position{}, fmt.Errorf("unmarshal position: %w", err)
	}
	return positionFromItem(it), nil
}

// ListPositionsByCommittee returns every non-deleted Position under a
// committee. Walks pagination internally.
func (c *Client) ListPositionsByCommittee(ctx context.Context, committeeID string) ([]domain.Position, error) {
	var all []domain.Position
	var startKey map[string]ddbtypes.AttributeValue
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: positionPK(committeeID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "POSITION#"},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("query positions: %w", err)
		}
		for _, raw := range out.Items {
			var it positionItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("unmarshal position: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			all = append(all, positionFromItem(it))
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return all, nil
}

// UpdatePositionPatch carries optional fields for partial updates.
type UpdatePositionPatch struct {
	Name           *string
	MaxDelegates   *int
	DualDelegation *bool
	PrestigeTier   *domain.PrestigeTier
	UpdatedBy      string
}

// UpdatePosition applies a partial update with optimistic locking.
func (c *Client) UpdatePosition(ctx context.Context, committeeID, id string, expectedVersion int, p UpdatePositionPatch) (domain.Position, error) {
	now := time.Now().UTC()
	upd := "SET version = :nextVersion, updatedAt = :now"
	exprNames := map[string]string{}
	exprVals := map[string]ddbtypes.AttributeValue{
		":expected":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
		":nextVersion": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
		":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	if p.Name != nil {
		upd += ", #n = :name"
		exprNames["#n"] = "name"
		exprVals[":name"] = &ddbtypes.AttributeValueMemberS{Value: *p.Name}
	}
	if p.MaxDelegates != nil {
		upd += ", maxDelegates = :max"
		exprVals[":max"] = &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(*p.MaxDelegates)}
	}
	if p.DualDelegation != nil {
		upd += ", dualDelegation = :dual"
		exprVals[":dual"] = &ddbtypes.AttributeValueMemberBOOL{Value: *p.DualDelegation}
	}
	if p.PrestigeTier != nil {
		upd += ", prestigeTier = :tier"
		exprVals[":tier"] = &ddbtypes.AttributeValueMemberS{Value: string(*p.PrestigeTier)}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: positionPK(committeeID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: positionSK(id)},
		},
		UpdateExpression:          aws.String(upd),
		ConditionExpression:       aws.String("version = :expected AND isDeleted = :false"),
		ExpressionAttributeValues: exprVals,
		ReturnValues:              ddbtypes.ReturnValueAllNew,
	}
	if len(exprNames) > 0 {
		in.ExpressionAttributeNames = exprNames
	}
	out, err := c.DDB.UpdateItem(ctx, in)
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetPosition(ctx, committeeID, id); errors.Is(gerr, ErrNotFound) {
				return domain.Position{}, ErrNotFound
			}
			return domain.Position{}, ErrVersionMismatch
		}
		return domain.Position{}, fmt.Errorf("update position: %w", err)
	}
	var it positionItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Position{}, fmt.Errorf("unmarshal position: %w", err)
	}
	return positionFromItem(it), nil
}

// SoftDeletePosition marks the row deleted with optimistic locking.
func (c *Client) SoftDeletePosition(ctx context.Context, committeeID, id string, expectedVersion int, actorUserID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: positionPK(committeeID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: positionSK(id)},
		},
		UpdateExpression:    aws.String("SET #del = :true, #v = :nv, updatedAt = :now, updatedBy = :ub"),
		ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
		ExpressionAttributeNames: map[string]string{
			"#del": "isDeleted",
			"#v":   "version",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":true": &ddbtypes.AttributeValueMemberBOOL{Value: true},
			":ev":   &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
			":nv":   &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
			":now":  &ddbtypes.AttributeValueMemberS{Value: now},
			":ub":   &ddbtypes.AttributeValueMemberS{Value: actorUserID},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrVersionMismatch
		}
		return fmt.Errorf("soft-delete position: %w", err)
	}
	return nil
}
