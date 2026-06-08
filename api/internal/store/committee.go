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

// committeeItem is the on-the-wire DDB shape for a Committee row.
// PK = CONF#<conferenceId>, SK = COMMITTEE#<committeeId> per DATA_MODEL.md §6.
type committeeItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID       string `dynamodbav:"conferenceId"`
	Name               string `dynamodbav:"name"`
	Type               string `dynamodbav:"type"`
	Size               string `dynamodbav:"size"`
	BackgroundGuideRef string `dynamodbav:"backgroundGuideRef,omitempty"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`
}

func committeePK(conferenceID string) string { return "CONF#" + conferenceID }
func committeeSK(id string) string           { return "COMMITTEE#" + id }

func committeeToItem(c domain.Committee) committeeItem {
	return committeeItem{
		PK:                 committeePK(c.ConferenceID),
		SK:                 committeeSK(c.ID),
		Entity:             "Committee",
		ID:                 c.ID,
		ConferenceID:       c.ConferenceID,
		Name:               c.Name,
		Type:               string(c.Type),
		Size:               string(c.Size),
		BackgroundGuideRef: c.BackgroundGuideRef,
		IsDeleted:          c.IsDeleted,
		Version:            c.Version,
		CreatedAt:          c.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:          c.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:          c.CreatedBy,
		UpdatedBy:          c.UpdatedBy,
	}
}

func committeeFromItem(it committeeItem) domain.Committee {
	c := domain.Committee{
		ID:                 it.ID,
		ConferenceID:       it.ConferenceID,
		Name:               it.Name,
		Type:               domain.CommitteeType(it.Type),
		Size:               domain.CommitteeSize(it.Size),
		BackgroundGuideRef: it.BackgroundGuideRef,
		IsDeleted:          it.IsDeleted,
		Version:            it.Version,
		CreatedBy:          it.CreatedBy,
		UpdatedBy:          it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		c.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		c.UpdatedAt = t
	}
	return c
}

func prepareNewCommittee(in domain.Committee) domain.Committee {
	if in.ID == "" {
		if id, err := uuid.NewV7(); err == nil {
			in.ID = id.String()
		}
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	return in
}

// CreateCommittee inserts a new Committee row.
func (c *Client) CreateCommittee(ctx context.Context, in domain.Committee) (domain.Committee, error) {
	in = prepareNewCommittee(in)
	it := committeeToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Committee{}, fmt.Errorf("marshal committee: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Committee{}, ErrAlreadyExists
		}
		return domain.Committee{}, fmt.Errorf("put committee: %w", err)
	}
	return committeeFromItem(it), nil
}

// GetCommittee fetches a Committee by conference + id. ErrNotFound on missing
// or soft-deleted rows.
func (c *Client) GetCommittee(ctx context.Context, conferenceID, id string) (domain.Committee, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: committeePK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: committeeSK(id)},
		},
	})
	if err != nil {
		return domain.Committee{}, fmt.Errorf("get committee: %w", err)
	}
	if out.Item == nil {
		return domain.Committee{}, ErrNotFound
	}
	var it committeeItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Committee{}, fmt.Errorf("unmarshal committee: %w", err)
	}
	if it.IsDeleted {
		return domain.Committee{}, ErrNotFound
	}
	return committeeFromItem(it), nil
}

// FindCommitteeByID does an id-only lookup when the conferenceId isn't known.
// v1 uses a single-row Scan with a FilterExpression.
func (c *Client) FindCommitteeByID(ctx context.Context, id string) (domain.Committee, error) {
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("#e = :entity AND id = :id AND #del = :false"),
		ExpressionAttributeNames: map[string]string{
			"#e":   "entity",
			"#del": "isDeleted",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":entity": &ddbtypes.AttributeValueMemberS{Value: "Committee"},
			":id":     &ddbtypes.AttributeValueMemberS{Value: id},
			":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.Committee{}, fmt.Errorf("find committee: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Committee{}, ErrNotFound
	}
	var it committeeItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.Committee{}, fmt.Errorf("unmarshal committee: %w", err)
	}
	return committeeFromItem(it), nil
}

// ListCommitteesByConference returns every non-deleted Committee under a
// conference. Walks pagination internally — NUMUN scale is ~30 committees per
// conference.
func (c *Client) ListCommitteesByConference(ctx context.Context, conferenceID string) ([]domain.Committee, error) {
	var all []domain.Committee
	var startKey map[string]ddbtypes.AttributeValue
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: committeePK(conferenceID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "COMMITTEE#"},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("query committees: %w", err)
		}
		for _, raw := range out.Items {
			var it committeeItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("unmarshal committee: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			all = append(all, committeeFromItem(it))
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return all, nil
}

// UpdateCommitteePatch carries optional fields for partial updates.
type UpdateCommitteePatch struct {
	Name               *string
	Type               *domain.CommitteeType
	Size               *domain.CommitteeSize
	BackgroundGuideRef *string
	UpdatedBy          string
}

// UpdateCommittee applies a partial update with optimistic locking.
func (c *Client) UpdateCommittee(ctx context.Context, conferenceID, id string, expectedVersion int, p UpdateCommitteePatch) (domain.Committee, error) {
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
	if p.Type != nil {
		upd += ", #t = :type"
		exprNames["#t"] = "type"
		exprVals[":type"] = &ddbtypes.AttributeValueMemberS{Value: string(*p.Type)}
	}
	if p.Size != nil {
		upd += ", #s = :size"
		exprNames["#s"] = "size"
		exprVals[":size"] = &ddbtypes.AttributeValueMemberS{Value: string(*p.Size)}
	}
	if p.BackgroundGuideRef != nil {
		upd += ", backgroundGuideRef = :bgr"
		exprVals[":bgr"] = &ddbtypes.AttributeValueMemberS{Value: *p.BackgroundGuideRef}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: committeePK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: committeeSK(id)},
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
			if _, gerr := c.GetCommittee(ctx, conferenceID, id); errors.Is(gerr, ErrNotFound) {
				return domain.Committee{}, ErrNotFound
			}
			return domain.Committee{}, ErrVersionMismatch
		}
		return domain.Committee{}, fmt.Errorf("update committee: %w", err)
	}
	var it committeeItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Committee{}, fmt.Errorf("unmarshal committee: %w", err)
	}
	return committeeFromItem(it), nil
}

// SoftDeleteCommittee marks the row deleted with optimistic locking.
func (c *Client) SoftDeleteCommittee(ctx context.Context, conferenceID, id string, expectedVersion int, actorUserID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: committeePK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: committeeSK(id)},
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
		return fmt.Errorf("soft-delete committee: %w", err)
	}
	return nil
}
