package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/numun/numun/api/internal/domain"
)

// userItem is the on-the-wire DDB shape for a User row. Keep this separate
// from domain.User so the storage layout stays decoupled from the domain.
type userItem struct {
	PK                 string `dynamodbav:"PK"`
	SK                 string `dynamodbav:"SK"`
	Entity             string `dynamodbav:"entity"`
	ID                 string `dynamodbav:"id"`
	Role               string `dynamodbav:"role"`
	Email              string `dynamodbav:"email"`
	Name               string `dynamodbav:"name"`
	Phone              string `dynamodbav:"phone"`
	EmailStatus        string `dynamodbav:"emailStatus"`
	AnnouncementsOptIn bool   `dynamodbav:"announcementsOptIn"`
	// DismissedAwardIDs stored as a DDB String Set (SS) so the AddDismissedAward
	// ADD update is naturally idempotent. M11.
	DismissedAwardIDs []string          `dynamodbav:"dismissedAwardIds,stringset,omitempty"`
	IsDeleted         bool              `dynamodbav:"isDeleted"`
	Version           int               `dynamodbav:"version"`
	CreatedAt         string            `dynamodbav:"createdAt"`
	UpdatedAt         string            `dynamodbav:"updatedAt"`
	CreatedBy         string            `dynamodbav:"createdBy,omitempty"`
	UpdatedBy         string            `dynamodbav:"updatedBy,omitempty"`
	Extra             map[string]string `dynamodbav:"-"`
}

func userPK(id string) string { return "USER#" + id }

const userSK = "PROFILE"

func userFromItem(it userItem) domain.User {
	u := domain.User{
		ID:                 it.ID,
		Role:               domain.Role(it.Role),
		Email:              it.Email,
		Name:               it.Name,
		Phone:              it.Phone,
		EmailStatus:        domain.EmailStatus(it.EmailStatus),
		AnnouncementsOptIn: it.AnnouncementsOptIn,
		DismissedAwardIDs:  it.DismissedAwardIDs,
		IsDeleted:          it.IsDeleted,
		Version:            it.Version,
		CreatedBy:          it.CreatedBy,
		UpdatedBy:          it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		u.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		u.UpdatedAt = t
	}
	return u
}

// CreateUser inserts a new User row, failing if a row with the same id already
// exists. Used by the Cognito post-confirmation Lambda and InviteStaff.
func (c *Client) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	now := time.Now().UTC()
	u.CreatedAt = now
	u.UpdatedAt = now
	u.Version = 1
	if u.EmailStatus == "" {
		u.EmailStatus = domain.EmailStatusOK
	}
	it := userItem{
		PK:                 userPK(u.ID),
		SK:                 userSK,
		Entity:             "User",
		ID:                 u.ID,
		Role:               string(u.Role),
		Email:              u.Email,
		Name:               u.Name,
		Phone:              u.Phone,
		EmailStatus:        string(u.EmailStatus),
		AnnouncementsOptIn: u.AnnouncementsOptIn,
		IsDeleted:          false,
		Version:            1,
		CreatedAt:          now.Format(time.RFC3339Nano),
		UpdatedAt:          now.Format(time.RFC3339Nano),
		CreatedBy:          u.CreatedBy,
		UpdatedBy:          u.CreatedBy,
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.User{}, fmt.Errorf("marshal user: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.User{}, ErrAlreadyExists
		}
		return domain.User{}, fmt.Errorf("put user: %w", err)
	}
	return userFromItem(it), nil
}

// GetUser fetches a User by id. Returns ErrNotFound when missing or
// soft-deleted.
func (c *Client) GetUser(ctx context.Context, id string) (domain.User, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: userPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: userSK},
		},
	})
	if err != nil {
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}
	if out.Item == nil {
		return domain.User{}, ErrNotFound
	}
	var it userItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.User{}, fmt.Errorf("unmarshal user: %w", err)
	}
	if it.IsDeleted {
		return domain.User{}, ErrNotFound
	}
	return userFromItem(it), nil
}

// UpdateUserPatch carries the fields a caller can mutate via the standard
// optimistic-lock path. Nil pointers mean "leave alone".
type UpdateUserPatch struct {
	Name               *string
	Phone              *string
	EmailStatus        *domain.EmailStatus
	AnnouncementsOptIn *bool
	Role               *domain.Role
	UpdatedBy          string
}

// UpdateUser applies a patch using the standard
// `version = :expected AND isDeleted = :false` precondition. Returns the
// updated row.
func (c *Client) UpdateUser(ctx context.Context, id string, expectedVersion int, p UpdateUserPatch) (domain.User, error) {
	now := time.Now().UTC()
	upd := "SET version = :nextVersion, updatedAt = :now"
	exprNames := map[string]string{}
	exprVals := map[string]ddbtypes.AttributeValue{
		":expected":    &ddbtypes.AttributeValueMemberN{Value: itoa(expectedVersion)},
		":nextVersion": &ddbtypes.AttributeValueMemberN{Value: itoa(expectedVersion + 1)},
		":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	if p.Name != nil {
		exprNames["#nm"] = "name"
		upd += ", #nm = :name"
		exprVals[":name"] = &ddbtypes.AttributeValueMemberS{Value: *p.Name}
	}
	if p.Phone != nil {
		upd += ", phone = :phone"
		exprVals[":phone"] = &ddbtypes.AttributeValueMemberS{Value: *p.Phone}
	}
	if p.EmailStatus != nil {
		upd += ", emailStatus = :emailStatus"
		exprVals[":emailStatus"] = &ddbtypes.AttributeValueMemberS{Value: string(*p.EmailStatus)}
	}
	if p.AnnouncementsOptIn != nil {
		upd += ", announcementsOptIn = :aoi"
		exprVals[":aoi"] = &ddbtypes.AttributeValueMemberBOOL{Value: *p.AnnouncementsOptIn}
	}
	if p.Role != nil {
		upd += ", #role = :role"
		exprNames["#role"] = "role"
		exprVals[":role"] = &ddbtypes.AttributeValueMemberS{Value: string(*p.Role)}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: userPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: userSK},
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
			// Disambiguate: missing/deleted vs. version mismatch — issue a
			// follow-up Get so the caller can return the right code.
			if _, gerr := c.GetUser(ctx, id); errors.Is(gerr, ErrNotFound) {
				return domain.User{}, ErrNotFound
			}
			return domain.User{}, ErrVersionMismatch
		}
		return domain.User{}, fmt.Errorf("update user: %w", err)
	}
	var it userItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.User{}, fmt.Errorf("unmarshal user: %w", err)
	}
	return userFromItem(it), nil
}

// AddDismissedAward appends awardID to the User's dismissedAwardIds set.
// DDB's ADD on a String Set is naturally idempotent — calling it twice with
// the same value is a no-op. Returns the refreshed User. M11.
func (c *Client) AddDismissedAward(ctx context.Context, userID, awardID string) (domain.User, error) {
	out, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: userPK(userID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: userSK},
		},
		UpdateExpression:    aws.String("ADD dismissedAwardIds :a SET updatedAt = :now"),
		ConditionExpression: aws.String("attribute_exists(PK) AND isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":a":     &ddbtypes.AttributeValueMemberSS{Value: []string{awardID}},
			":now":   &ddbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		ReturnValues: ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.User{}, ErrNotFound
		}
		return domain.User{}, fmt.Errorf("add dismissed award: %w", err)
	}
	var it userItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.User{}, fmt.Errorf("unmarshal user: %w", err)
	}
	return userFromItem(it), nil
}

func itoa(n int) string {
	// Hand-rolled to avoid pulling strconv import noise into every file; keep
	// allocation pattern obvious for the conditional-update path.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
