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

// conferenceItem is the on-the-wire DDB shape for a Conference row.
type conferenceItem struct {
	PK            string            `dynamodbav:"PK"`
	SK            string            `dynamodbav:"SK"`
	Entity        string            `dynamodbav:"entity"`
	ID            string            `dynamodbav:"id"`
	Name          string            `dynamodbav:"name"`
	EditionNumber int               `dynamodbav:"editionNumber"`
	Year          int               `dynamodbav:"year"`
	StartsAt      string            `dynamodbav:"startsAt"`
	EndsAt        string            `dynamodbav:"endsAt"`
	Status        string            `dynamodbav:"status"`
	Metadata      map[string]string `dynamodbav:"metadata,omitempty"`
	IsDeleted     bool              `dynamodbav:"isDeleted"`
	Version       int               `dynamodbav:"version"`
	CreatedAt     string            `dynamodbav:"createdAt"`
	UpdatedAt     string            `dynamodbav:"updatedAt"`
	CreatedBy     string            `dynamodbav:"createdBy,omitempty"`
	UpdatedBy     string            `dynamodbav:"updatedBy,omitempty"`
}

func conferencePK(id string) string { return "CONF#" + id }

const conferenceSK = "META"

func conferenceFromItem(it conferenceItem) domain.Conference {
	c := domain.Conference{
		ID:            it.ID,
		Name:          it.Name,
		EditionNumber: it.EditionNumber,
		Year:          it.Year,
		Status:        domain.ConferenceStatus(it.Status),
		Metadata:      it.Metadata,
		IsDeleted:     it.IsDeleted,
		Version:       it.Version,
		CreatedBy:     it.CreatedBy,
		UpdatedBy:     it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.StartsAt); err == nil {
		c.StartsAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.EndsAt); err == nil {
		c.EndsAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		c.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		c.UpdatedAt = t
	}
	return c
}

// CreateConference inserts a new Conference row. Server assigns the UUIDv7
// id if the caller leaves it empty.
func (c *Client) CreateConference(ctx context.Context, in domain.Conference) (domain.Conference, error) {
	if in.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return domain.Conference{}, fmt.Errorf("uuid v7: %w", err)
		}
		in.ID = id.String()
	}
	if in.Status == "" {
		in.Status = domain.ConferenceStatusDraft
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1

	it := conferenceItem{
		PK:            conferencePK(in.ID),
		SK:            conferenceSK,
		Entity:        "Conference",
		ID:            in.ID,
		Name:          in.Name,
		EditionNumber: in.EditionNumber,
		Year:          in.Year,
		StartsAt:      in.StartsAt.Format(time.RFC3339Nano),
		EndsAt:        in.EndsAt.Format(time.RFC3339Nano),
		Status:        string(in.Status),
		Metadata:      in.Metadata,
		IsDeleted:     false,
		Version:       1,
		CreatedAt:     now.Format(time.RFC3339Nano),
		UpdatedAt:     now.Format(time.RFC3339Nano),
		CreatedBy:     in.CreatedBy,
		UpdatedBy:     in.CreatedBy,
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Conference{}, fmt.Errorf("marshal conference: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Conference{}, ErrAlreadyExists
		}
		return domain.Conference{}, fmt.Errorf("put conference: %w", err)
	}
	return conferenceFromItem(it), nil
}

// GetConference fetches a Conference by id. Returns ErrNotFound when missing
// or soft-deleted.
func (c *Client) GetConference(ctx context.Context, id string) (domain.Conference, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: conferencePK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: conferenceSK},
		},
	})
	if err != nil {
		return domain.Conference{}, fmt.Errorf("get conference: %w", err)
	}
	if out.Item == nil {
		return domain.Conference{}, ErrNotFound
	}
	var it conferenceItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Conference{}, fmt.Errorf("unmarshal conference: %w", err)
	}
	if it.IsDeleted {
		return domain.Conference{}, ErrNotFound
	}
	return conferenceFromItem(it), nil
}

// UpdateConferencePatch carries optional fields for partial updates.
type UpdateConferencePatch struct {
	Name          *string
	EditionNumber *int
	Year          *int
	StartsAt      *time.Time
	EndsAt        *time.Time
	Status        *domain.ConferenceStatus
	Metadata      *map[string]string
	UpdatedBy     string
}

// UpdateConference applies a patch under the standard optimistic-lock
// precondition.
func (c *Client) UpdateConference(ctx context.Context, id string, expectedVersion int, p UpdateConferencePatch) (domain.Conference, error) {
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
		exprNames["#nm"] = "name"
		upd += ", #nm = :name"
		exprVals[":name"] = &ddbtypes.AttributeValueMemberS{Value: *p.Name}
	}
	if p.EditionNumber != nil {
		upd += ", editionNumber = :edition"
		exprVals[":edition"] = &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(*p.EditionNumber)}
	}
	if p.Year != nil {
		upd += ", #yr = :year"
		exprNames["#yr"] = "year"
		exprVals[":year"] = &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(*p.Year)}
	}
	if p.StartsAt != nil {
		upd += ", startsAt = :startsAt"
		exprVals[":startsAt"] = &ddbtypes.AttributeValueMemberS{Value: p.StartsAt.Format(time.RFC3339Nano)}
	}
	if p.EndsAt != nil {
		upd += ", endsAt = :endsAt"
		exprVals[":endsAt"] = &ddbtypes.AttributeValueMemberS{Value: p.EndsAt.Format(time.RFC3339Nano)}
	}
	if p.Status != nil {
		upd += ", #st = :status"
		exprNames["#st"] = "status"
		exprVals[":status"] = &ddbtypes.AttributeValueMemberS{Value: string(*p.Status)}
	}
	if p.Metadata != nil {
		mAV, err := attributevalue.MarshalMap(*p.Metadata)
		if err != nil {
			return domain.Conference{}, fmt.Errorf("marshal metadata: %w", err)
		}
		upd += ", metadata = :metadata"
		exprVals[":metadata"] = &ddbtypes.AttributeValueMemberM{Value: mAV}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: conferencePK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: conferenceSK},
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
			if _, gerr := c.GetConference(ctx, id); errors.Is(gerr, ErrNotFound) {
				return domain.Conference{}, ErrNotFound
			}
			return domain.Conference{}, ErrVersionMismatch
		}
		return domain.Conference{}, fmt.Errorf("update conference: %w", err)
	}
	var it conferenceItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Conference{}, fmt.Errorf("unmarshal conference: %w", err)
	}
	return conferenceFromItem(it), nil
}

// ListConferences returns conferences (newest-first by id) for admin listings.
// Implementation note: DDB has no native global "all conferences" query without
// a separate aggregate row. We Scan with a filter on `entity = "Conference"` —
// acceptable at NUMUN scale (<25 conferences over a decade) and rare enough
// that the cost is negligible. If volume changes, introduce a sparse GSI on
// `entity = "Conference"` with a sort key by `createdAt` desc.
func (c *Client) ListConferences(ctx context.Context, status domain.ConferenceStatus, cursor string, pageSize int32) ([]domain.Conference, string, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500
	}
	exprNames := map[string]string{"#e": "entity", "#del": "isDeleted"}
	exprVals := map[string]ddbtypes.AttributeValue{
		":entity": &ddbtypes.AttributeValueMemberS{Value: "Conference"},
		":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	filter := "#e = :entity AND #del = :false"
	if status != "" {
		exprNames["#st"] = "status"
		exprVals[":status"] = &ddbtypes.AttributeValueMemberS{Value: string(status)}
		filter += " AND #st = :status"
	}
	in := &dynamodb.ScanInput{
		TableName:                 aws.String(c.Table),
		FilterExpression:          aws.String(filter),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprVals,
		Limit:                     aws.Int32(pageSize),
	}
	if cursor != "" {
		start, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode cursor: %w", err)
		}
		in.ExclusiveStartKey = start
	}
	out, err := c.DDB.Scan(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("scan conferences: %w", err)
	}
	confs := make([]domain.Conference, 0, len(out.Items))
	for _, raw := range out.Items {
		var it conferenceItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal conference: %w", err)
		}
		confs = append(confs, conferenceFromItem(it))
	}
	var next string
	if len(out.LastEvaluatedKey) > 0 {
		next, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("encode cursor: %w", err)
		}
	}
	return confs, next, nil
}

// FindActiveConference returns the unique conference whose status is
// open-for-registration or in-progress. ErrNotFound when there is none.
// Returns a sentinel error (ErrMultipleActiveConferences) when more than one
// matches — the handler surfaces this as failed_precondition per API.md
// §10.1b.
func (c *Client) FindActiveConference(ctx context.Context) (domain.Conference, error) {
	exprNames := map[string]string{"#e": "entity", "#st": "status", "#del": "isDeleted"}
	exprVals := map[string]ddbtypes.AttributeValue{
		":entity": &ddbtypes.AttributeValueMemberS{Value: "Conference"},
		":open":   &ddbtypes.AttributeValueMemberS{Value: string(domain.ConferenceStatusOpenForRegistration)},
		":inProg": &ddbtypes.AttributeValueMemberS{Value: string(domain.ConferenceStatusInProgress)},
		":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	in := &dynamodb.ScanInput{
		TableName:                 aws.String(c.Table),
		FilterExpression:          aws.String("#e = :entity AND #del = :false AND (#st = :open OR #st = :inProg)"),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprVals,
		Limit:                     aws.Int32(10),
	}
	out, err := c.DDB.Scan(ctx, in)
	if err != nil {
		return domain.Conference{}, fmt.Errorf("scan active conference: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Conference{}, ErrNotFound
	}
	if len(out.Items) > 1 {
		return domain.Conference{}, ErrMultipleActiveConferences
	}
	var it conferenceItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.Conference{}, fmt.Errorf("unmarshal conference: %w", err)
	}
	return conferenceFromItem(it), nil
}
