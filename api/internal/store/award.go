package store

import (
	"context"
	"encoding/json"
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

// awardItem is the DDB row shape for an Award. PK = CONF#<conferenceId>,
// SK = AWARD#<awardId> per DATA_MODEL.md §6. Recipients are JSON-marshalled
// into a single string attribute — they're opaque to indexes and the
// list-of-map encoding adds friction to the conditional-update path.
type awardItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID  string `dynamodbav:"conferenceId"`
	Name          string `dynamodbav:"name"`
	Category      string `dynamodbav:"category,omitempty"`
	RecipientsRaw string `dynamodbav:"recipients,omitempty"`
	AwardedAt     string `dynamodbav:"awardedAt,omitempty"`
	AwardedBy     string `dynamodbav:"awardedBy,omitempty"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`
}

func awardPK(conferenceID string) string { return "CONF#" + conferenceID }
func awardSK(id string) string           { return "AWARD#" + id }

func marshalAwardRecipients(recs []domain.AwardRecipient) (string, error) {
	if len(recs) == 0 {
		return "", nil
	}
	b, err := json.Marshal(recs)
	if err != nil {
		return "", fmt.Errorf("marshal recipients: %w", err)
	}
	return string(b), nil
}

func unmarshalAwardRecipients(raw string) ([]domain.AwardRecipient, error) {
	if raw == "" {
		return nil, nil
	}
	var recs []domain.AwardRecipient
	if err := json.Unmarshal([]byte(raw), &recs); err != nil {
		return nil, fmt.Errorf("unmarshal recipients: %w", err)
	}
	return recs, nil
}

func awardToItem(a domain.Award) (awardItem, error) {
	recipientsRaw, err := marshalAwardRecipients(a.Recipients)
	if err != nil {
		return awardItem{}, err
	}
	it := awardItem{
		PK:            awardPK(a.ConferenceID),
		SK:            awardSK(a.ID),
		Entity:        "Award",
		ID:            a.ID,
		ConferenceID:  a.ConferenceID,
		Name:          a.Name,
		Category:      a.Category,
		RecipientsRaw: recipientsRaw,
		AwardedBy:     a.AwardedBy,
		IsDeleted:     a.IsDeleted,
		Version:       a.Version,
		CreatedAt:     a.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:     a.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:     a.CreatedBy,
		UpdatedBy:     a.UpdatedBy,
	}
	if !a.AwardedAt.IsZero() {
		it.AwardedAt = a.AwardedAt.Format(time.RFC3339Nano)
	}
	return it, nil
}

func awardFromItem(it awardItem) (domain.Award, error) {
	recs, err := unmarshalAwardRecipients(it.RecipientsRaw)
	if err != nil {
		return domain.Award{}, err
	}
	a := domain.Award{
		ID:           it.ID,
		ConferenceID: it.ConferenceID,
		Name:         it.Name,
		Category:     it.Category,
		Recipients:   recs,
		AwardedBy:    it.AwardedBy,
		IsDeleted:    it.IsDeleted,
		Version:      it.Version,
		CreatedBy:    it.CreatedBy,
		UpdatedBy:    it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		a.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		a.UpdatedAt = t
	}
	if it.AwardedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, it.AwardedAt); err == nil {
			a.AwardedAt = t
		}
	}
	return a, nil
}

func prepareNewAward(in domain.Award) domain.Award {
	if in.ID == "" {
		if id, err := uuid.NewV7(); err == nil {
			in.ID = id.String()
		}
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.AwardedAt.IsZero() {
		in.AwardedAt = now
	}
	in.Version = 1
	return in
}

// CreateAward inserts a new Award row.
func (c *Client) CreateAward(ctx context.Context, in domain.Award) (domain.Award, error) {
	in = prepareNewAward(in)
	it, err := awardToItem(in)
	if err != nil {
		return domain.Award{}, err
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Award{}, fmt.Errorf("marshal award: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Award{}, ErrAlreadyExists
		}
		return domain.Award{}, fmt.Errorf("put award: %w", err)
	}
	return awardFromItem(it)
}

// GetAward fetches an Award by conference + id. ErrNotFound on missing or
// soft-deleted rows.
func (c *Client) GetAward(ctx context.Context, conferenceID, id string) (domain.Award, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: awardPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: awardSK(id)},
		},
	})
	if err != nil {
		return domain.Award{}, fmt.Errorf("get award: %w", err)
	}
	if out.Item == nil {
		return domain.Award{}, ErrNotFound
	}
	var it awardItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Award{}, fmt.Errorf("unmarshal award: %w", err)
	}
	if it.IsDeleted {
		return domain.Award{}, ErrNotFound
	}
	return awardFromItem(it)
}

// FindAwardByID does an id-only lookup when the conferenceId isn't known.
func (c *Client) FindAwardByID(ctx context.Context, id string) (domain.Award, error) {
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("#e = :entity AND id = :id AND #del = :false"),
		ExpressionAttributeNames: map[string]string{
			"#e":   "entity",
			"#del": "isDeleted",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":entity": &ddbtypes.AttributeValueMemberS{Value: "Award"},
			":id":     &ddbtypes.AttributeValueMemberS{Value: id},
			":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.Award{}, fmt.Errorf("find award: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Award{}, ErrNotFound
	}
	var it awardItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.Award{}, fmt.Errorf("unmarshal award: %w", err)
	}
	return awardFromItem(it)
}

// ListAwardsByConference returns every non-deleted Award under a conference.
// Walks pagination internally — NUMUN scale is at most a few hundred awards.
func (c *Client) ListAwardsByConference(ctx context.Context, conferenceID string) ([]domain.Award, error) {
	var all []domain.Award
	var startKey map[string]ddbtypes.AttributeValue
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: awardPK(conferenceID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "AWARD#"},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("query awards: %w", err)
		}
		for _, raw := range out.Items {
			var it awardItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("unmarshal award: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			a, err := awardFromItem(it)
			if err != nil {
				return nil, err
			}
			all = append(all, a)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return all, nil
}

// UpdateAwardPatch carries optional fields for a partial update. RecipientsSet
// distinguishes "leave recipients alone" (false) from "replace with the
// provided list" (true) — required because a nil slice and an explicit empty
// slice would otherwise be indistinguishable, and the empty-replacement
// case is illegal (validate in the handler).
type UpdateAwardPatch struct {
	Name          *string
	Category      *string
	Recipients    []domain.AwardRecipient
	RecipientsSet bool
	UpdatedBy     string
}

// UpdateAward applies a partial update with optimistic locking.
func (c *Client) UpdateAward(ctx context.Context, conferenceID, id string, expectedVersion int, p UpdateAwardPatch) (domain.Award, error) {
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
	if p.Category != nil {
		upd += ", category = :category"
		exprVals[":category"] = &ddbtypes.AttributeValueMemberS{Value: *p.Category}
	}
	if p.RecipientsSet {
		raw, err := marshalAwardRecipients(p.Recipients)
		if err != nil {
			return domain.Award{}, err
		}
		upd += ", recipients = :recipients"
		exprVals[":recipients"] = &ddbtypes.AttributeValueMemberS{Value: raw}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: awardPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: awardSK(id)},
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
			if _, gerr := c.GetAward(ctx, conferenceID, id); errors.Is(gerr, ErrNotFound) {
				return domain.Award{}, ErrNotFound
			}
			return domain.Award{}, ErrVersionMismatch
		}
		return domain.Award{}, fmt.Errorf("update award: %w", err)
	}
	var it awardItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Award{}, fmt.Errorf("unmarshal award: %w", err)
	}
	return awardFromItem(it)
}

// SoftDeleteAward marks the row deleted with optimistic locking.
func (c *Client) SoftDeleteAward(ctx context.Context, conferenceID, id string, expectedVersion int, actorUserID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: awardPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: awardSK(id)},
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
		return fmt.Errorf("soft-delete award: %w", err)
	}
	return nil
}
