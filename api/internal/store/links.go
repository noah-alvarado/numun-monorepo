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

	"github.com/numun/numun/api/internal/domain"
)

// ── DelegationAdvisor (User ↔ Delegation, role=lead/secondary) ───────────────

type advisorItem struct {
	PK           string `dynamodbav:"PK"`
	SK           string `dynamodbav:"SK"`
	Entity       string `dynamodbav:"entity"`
	UserID       string `dynamodbav:"userId"`
	DelegationID string `dynamodbav:"delegationId"`
	ConferenceID string `dynamodbav:"conferenceId"`
	Role         string `dynamodbav:"role"`
	IsDeleted    bool   `dynamodbav:"isDeleted"`
	Version      int    `dynamodbav:"version"`
	CreatedAt    string `dynamodbav:"createdAt"`
	UpdatedAt    string `dynamodbav:"updatedAt"`
	CreatedBy    string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy    string `dynamodbav:"updatedBy,omitempty"`
	// GSI1: USER#<userId> → ADVISES#<conferenceId>#<delegationId>
	GSI1PK string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK string `dynamodbav:"GSI1SK,omitempty"`
}

func advisorPK(delegationID string) string { return "DELEGATION#" + delegationID }
func advisorSK(userID string) string       { return "ADVISOR#" + userID }
func advisorGSI1PK(userID string) string   { return "USER#" + userID }
func advisorGSI1SK(conferenceID, delegationID string) string {
	return "ADVISES#" + conferenceID + "#" + delegationID
}

func advisorToItem(a domain.DelegationAdvisor) advisorItem {
	return advisorItem{
		PK:           advisorPK(a.DelegationID),
		SK:           advisorSK(a.UserID),
		Entity:       "DelegationAdvisor",
		UserID:       a.UserID,
		DelegationID: a.DelegationID,
		ConferenceID: a.ConferenceID,
		Role:         string(a.Role),
		IsDeleted:    a.IsDeleted,
		Version:      a.Version,
		CreatedAt:    a.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:    a.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:    a.CreatedBy,
		UpdatedBy:    a.UpdatedBy,
		GSI1PK:       advisorGSI1PK(a.UserID),
		GSI1SK:       advisorGSI1SK(a.ConferenceID, a.DelegationID),
	}
}

func advisorFromItem(it advisorItem) domain.DelegationAdvisor {
	a := domain.DelegationAdvisor{
		UserID:       it.UserID,
		DelegationID: it.DelegationID,
		ConferenceID: it.ConferenceID,
		Role:         domain.AdvisorRole(it.Role),
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
	return a
}

// AddAdvisor creates a DelegationAdvisor link. Returns ErrAlreadyExists if the
// link already exists. Does not enforce the at-least-one-lead invariant on its
// own; the handler composes business rules.
func (c *Client) AddAdvisor(ctx context.Context, a domain.DelegationAdvisor) (domain.DelegationAdvisor, error) {
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	a.Version = 1
	if a.Role == "" {
		a.Role = domain.AdvisorRoleSecondary
	}
	it := advisorToItem(a)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.DelegationAdvisor{}, fmt.Errorf("marshal advisor: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.DelegationAdvisor{}, ErrAlreadyExists
		}
		return domain.DelegationAdvisor{}, fmt.Errorf("put advisor: %w", err)
	}
	return advisorFromItem(it), nil
}

// GetAdvisor fetches a single DelegationAdvisor link.
func (c *Client) GetAdvisor(ctx context.Context, delegationID, userID string) (domain.DelegationAdvisor, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: advisorPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: advisorSK(userID)},
		},
	})
	if err != nil {
		return domain.DelegationAdvisor{}, fmt.Errorf("get advisor: %w", err)
	}
	if out.Item == nil {
		return domain.DelegationAdvisor{}, ErrNotFound
	}
	var it advisorItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.DelegationAdvisor{}, fmt.Errorf("unmarshal advisor: %w", err)
	}
	if it.IsDeleted {
		return domain.DelegationAdvisor{}, ErrNotFound
	}
	return advisorFromItem(it), nil
}

// ListAdvisorsByDelegation returns every advisor row under a delegation
// (soft-deleted filtered). Used by the handlers and the at-least-one-lead
// invariant enforcement.
func (c *Client) ListAdvisorsByDelegation(ctx context.Context, delegationID string) ([]domain.DelegationAdvisor, error) {
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		FilterExpression:       aws.String("isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":     &ddbtypes.AttributeValueMemberS{Value: advisorPK(delegationID)},
			":prefix": &ddbtypes.AttributeValueMemberS{Value: "ADVISOR#"},
			":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list advisors: %w", err)
	}
	advisors := make([]domain.DelegationAdvisor, 0, len(out.Items))
	for _, raw := range out.Items {
		var it advisorItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal advisor: %w", err)
		}
		advisors = append(advisors, advisorFromItem(it))
	}
	return advisors, nil
}

// ListAdvisorshipsByUser returns the DelegationAdvisor links for a user via
// GSI1 (USER#<id> + ADVISES#). Used by scope helpers.
func (c *Client) ListAdvisorshipsByUser(ctx context.Context, userID string) ([]domain.DelegationAdvisor, error) {
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk AND begins_with(GSI1SK, :prefix)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":     &ddbtypes.AttributeValueMemberS{Value: advisorGSI1PK(userID)},
			":prefix": &ddbtypes.AttributeValueMemberS{Value: "ADVISES#"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list advisorships: %w", err)
	}
	// GSI1 is KEYS_ONLY; resolve to base-table rows.
	keys := make([]map[string]ddbtypes.AttributeValue, 0, len(out.Items))
	for _, raw := range out.Items {
		pk, ok1 := raw["PK"].(*ddbtypes.AttributeValueMemberS)
		sk, ok2 := raw["SK"].(*ddbtypes.AttributeValueMemberS)
		if !ok1 || !ok2 {
			continue
		}
		keys = append(keys, map[string]ddbtypes.AttributeValue{
			"PK": pk,
			"SK": sk,
		})
	}
	if len(keys) == 0 {
		return nil, nil
	}
	got, err := c.DDB.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			c.Table: {Keys: keys},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("batch get advisors: %w", err)
	}
	advisors := make([]domain.DelegationAdvisor, 0, len(got.Responses[c.Table]))
	for _, raw := range got.Responses[c.Table] {
		var it advisorItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal advisor: %w", err)
		}
		if it.IsDeleted {
			continue
		}
		advisors = append(advisors, advisorFromItem(it))
	}
	return advisors, nil
}

// SetAdvisorRole flips a single advisor's role under the standard
// optimistic-lock precondition. Lead-uniqueness invariants are composed by the
// handler around this primitive.
func (c *Client) SetAdvisorRole(ctx context.Context, delegationID, userID string, expectedVersion int, role domain.AdvisorRole, actorUserID string) (domain.DelegationAdvisor, error) {
	now := time.Now().UTC()
	out, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: advisorPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: advisorSK(userID)},
		},
		UpdateExpression:    aws.String("SET version = :nextVersion, updatedAt = :now, #r = :role, updatedBy = :actor"),
		ConditionExpression: aws.String("version = :expected AND isDeleted = :false"),
		ExpressionAttributeNames: map[string]string{
			"#r": "role",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":expected":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
			":nextVersion": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
			":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
			":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
			":role":        &ddbtypes.AttributeValueMemberS{Value: string(role)},
			":actor":       &ddbtypes.AttributeValueMemberS{Value: actorUserID},
		},
		ReturnValues: ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetAdvisor(ctx, delegationID, userID); errors.Is(gerr, ErrNotFound) {
				return domain.DelegationAdvisor{}, ErrNotFound
			}
			return domain.DelegationAdvisor{}, ErrVersionMismatch
		}
		return domain.DelegationAdvisor{}, fmt.Errorf("set advisor role: %w", err)
	}
	var it advisorItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.DelegationAdvisor{}, fmt.Errorf("unmarshal advisor: %w", err)
	}
	return advisorFromItem(it), nil
}

// SoftDeleteAdvisor flips isDeleted=true (DATA_MODEL.md §7.3). Hard delete is
// not exposed.
func (c *Client) SoftDeleteAdvisor(ctx context.Context, delegationID, userID string) error {
	now := time.Now().UTC()
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: advisorPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: advisorSK(userID)},
		},
		UpdateExpression:    aws.String("SET isDeleted = :true, updatedAt = :now REMOVE GSI1PK, GSI1SK"),
		ConditionExpression: aws.String("attribute_exists(PK) AND isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":true":  &ddbtypes.AttributeValueMemberBOOL{Value: true},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
			":now":   &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrNotFound
		}
		return fmt.Errorf("soft delete advisor: %w", err)
	}
	return nil
}

// ── StaffDelegationAssignment (User ↔ Delegation oversight) ──────────────────

type staffDelegationItem struct {
	PK           string `dynamodbav:"PK"`
	SK           string `dynamodbav:"SK"`
	Entity       string `dynamodbav:"entity"`
	UserID       string `dynamodbav:"userId"`
	DelegationID string `dynamodbav:"delegationId"`
	ConferenceID string `dynamodbav:"conferenceId"`
	IsDeleted    bool   `dynamodbav:"isDeleted"`
	Version      int    `dynamodbav:"version"`
	CreatedAt    string `dynamodbav:"createdAt"`
	UpdatedAt    string `dynamodbav:"updatedAt"`
	CreatedBy    string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy    string `dynamodbav:"updatedBy,omitempty"`
	GSI1PK       string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK       string `dynamodbav:"GSI1SK,omitempty"`
}

func staffDelegationPK(delegationID string) string { return "DELEGATION#" + delegationID }
func staffDelegationSK(userID string) string       { return "STAFF#" + userID }
func staffDelegationGSI1SK(conferenceID, delegationID string) string {
	return "OVERSEES#" + conferenceID + "#" + delegationID
}

// AssignStaffer creates a StaffDelegationAssignment.
func (c *Client) AssignStaffer(ctx context.Context, a domain.StaffDelegationAssignment) error {
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	a.Version = 1
	it := staffDelegationItem{
		PK:           staffDelegationPK(a.DelegationID),
		SK:           staffDelegationSK(a.UserID),
		Entity:       "StaffDelegationAssignment",
		UserID:       a.UserID,
		DelegationID: a.DelegationID,
		ConferenceID: a.ConferenceID,
		IsDeleted:    false,
		Version:      1,
		CreatedAt:    now.Format(time.RFC3339Nano),
		UpdatedAt:    now.Format(time.RFC3339Nano),
		CreatedBy:    a.CreatedBy,
		UpdatedBy:    a.CreatedBy,
		GSI1PK:       "USER#" + a.UserID,
		GSI1SK:       staffDelegationGSI1SK(a.ConferenceID, a.DelegationID),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("marshal staff-delegation: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("put staff-delegation: %w", err)
	}
	return nil
}

// UnassignStaffer soft-deletes a StaffDelegationAssignment.
func (c *Client) UnassignStaffer(ctx context.Context, delegationID, userID string) error {
	now := time.Now().UTC()
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: staffDelegationPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: staffDelegationSK(userID)},
		},
		UpdateExpression:    aws.String("SET isDeleted = :true, updatedAt = :now REMOVE GSI1PK, GSI1SK"),
		ConditionExpression: aws.String("attribute_exists(PK) AND isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":true":  &ddbtypes.AttributeValueMemberBOOL{Value: true},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
			":now":   &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrNotFound
		}
		return fmt.Errorf("unassign staff: %w", err)
	}
	return nil
}

// ListStaffOversightsByUser returns the staff-delegation links keyed off the
// user via GSI1 (USER#<id> + OVERSEES#).
func (c *Client) ListStaffOversightsByUser(ctx context.Context, userID string) ([]domain.StaffDelegationAssignment, error) {
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk AND begins_with(GSI1SK, :prefix)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":     &ddbtypes.AttributeValueMemberS{Value: "USER#" + userID},
			":prefix": &ddbtypes.AttributeValueMemberS{Value: "OVERSEES#"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list staff oversights: %w", err)
	}
	keys := make([]map[string]ddbtypes.AttributeValue, 0, len(out.Items))
	for _, raw := range out.Items {
		pk, ok1 := raw["PK"].(*ddbtypes.AttributeValueMemberS)
		sk, ok2 := raw["SK"].(*ddbtypes.AttributeValueMemberS)
		if !ok1 || !ok2 {
			continue
		}
		keys = append(keys, map[string]ddbtypes.AttributeValue{"PK": pk, "SK": sk})
	}
	if len(keys) == 0 {
		return nil, nil
	}
	got, err := c.DDB.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			c.Table: {Keys: keys},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("batch get staff oversights: %w", err)
	}
	rows := make([]domain.StaffDelegationAssignment, 0, len(got.Responses[c.Table]))
	for _, raw := range got.Responses[c.Table] {
		var it staffDelegationItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal staff-delegation: %w", err)
		}
		if it.IsDeleted {
			continue
		}
		rows = append(rows, domain.StaffDelegationAssignment{
			UserID:       it.UserID,
			DelegationID: it.DelegationID,
			ConferenceID: it.ConferenceID,
			Version:      it.Version,
		})
	}
	return rows, nil
}

// GetStaffDelegationAssignment is a direct lookup used by scope helpers.
func (c *Client) GetStaffDelegationAssignment(ctx context.Context, delegationID, userID string) (domain.StaffDelegationAssignment, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: staffDelegationPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: staffDelegationSK(userID)},
		},
	})
	if err != nil {
		return domain.StaffDelegationAssignment{}, fmt.Errorf("get staff-delegation: %w", err)
	}
	if out.Item == nil {
		return domain.StaffDelegationAssignment{}, ErrNotFound
	}
	var it staffDelegationItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.StaffDelegationAssignment{}, fmt.Errorf("unmarshal staff-delegation: %w", err)
	}
	if it.IsDeleted {
		return domain.StaffDelegationAssignment{}, ErrNotFound
	}
	return domain.StaffDelegationAssignment{
		UserID:       it.UserID,
		DelegationID: it.DelegationID,
		ConferenceID: it.ConferenceID,
		Version:      it.Version,
	}, nil
}

// ── StaffCommitteeAssignment (User ↔ Committee oversight) ────────────────────

type staffCommitteeItem struct {
	PK           string `dynamodbav:"PK"`
	SK           string `dynamodbav:"SK"`
	Entity       string `dynamodbav:"entity"`
	UserID       string `dynamodbav:"userId"`
	CommitteeID  string `dynamodbav:"committeeId"`
	ConferenceID string `dynamodbav:"conferenceId"`
	IsDeleted    bool   `dynamodbav:"isDeleted"`
	Version      int    `dynamodbav:"version"`
	CreatedAt    string `dynamodbav:"createdAt"`
	UpdatedAt    string `dynamodbav:"updatedAt"`
	CreatedBy    string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy    string `dynamodbav:"updatedBy,omitempty"`
	GSI1PK       string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK       string `dynamodbav:"GSI1SK,omitempty"`
}

func staffCommitteePK(committeeID string) string { return "COMMITTEE#" + committeeID }
func staffCommitteeSK(userID string) string      { return "STAFF#" + userID }
func staffCommitteeGSI1SK(conferenceID, committeeID string) string {
	return "CHAIRS#" + conferenceID + "#" + committeeID
}

// AssignStafferToCommittee creates a StaffCommitteeAssignment.
func (c *Client) AssignStafferToCommittee(ctx context.Context, a domain.StaffCommitteeAssignment) error {
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	a.Version = 1
	it := staffCommitteeItem{
		PK:           staffCommitteePK(a.CommitteeID),
		SK:           staffCommitteeSK(a.UserID),
		Entity:       "StaffCommitteeAssignment",
		UserID:       a.UserID,
		CommitteeID:  a.CommitteeID,
		ConferenceID: a.ConferenceID,
		IsDeleted:    false,
		Version:      1,
		CreatedAt:    now.Format(time.RFC3339Nano),
		UpdatedAt:    now.Format(time.RFC3339Nano),
		CreatedBy:    a.CreatedBy,
		UpdatedBy:    a.CreatedBy,
		GSI1PK:       "USER#" + a.UserID,
		GSI1SK:       staffCommitteeGSI1SK(a.ConferenceID, a.CommitteeID),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("marshal staff-committee: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("put staff-committee: %w", err)
	}
	return nil
}

// UnassignStafferFromCommittee soft-deletes a StaffCommitteeAssignment.
func (c *Client) UnassignStafferFromCommittee(ctx context.Context, committeeID, userID string) error {
	now := time.Now().UTC()
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: staffCommitteePK(committeeID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: staffCommitteeSK(userID)},
		},
		UpdateExpression:    aws.String("SET isDeleted = :true, updatedAt = :now REMOVE GSI1PK, GSI1SK"),
		ConditionExpression: aws.String("attribute_exists(PK) AND isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":true":  &ddbtypes.AttributeValueMemberBOOL{Value: true},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
			":now":   &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrNotFound
		}
		return fmt.Errorf("unassign staff committee: %w", err)
	}
	return nil
}

// ListStaffCommitteesByUser returns the committee links for a user via GSI1.
func (c *Client) ListStaffCommitteesByUser(ctx context.Context, userID string) ([]domain.StaffCommitteeAssignment, error) {
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk AND begins_with(GSI1SK, :prefix)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":     &ddbtypes.AttributeValueMemberS{Value: "USER#" + userID},
			":prefix": &ddbtypes.AttributeValueMemberS{Value: "CHAIRS#"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list staff committees: %w", err)
	}
	keys := make([]map[string]ddbtypes.AttributeValue, 0, len(out.Items))
	for _, raw := range out.Items {
		pk, ok1 := raw["PK"].(*ddbtypes.AttributeValueMemberS)
		sk, ok2 := raw["SK"].(*ddbtypes.AttributeValueMemberS)
		if !ok1 || !ok2 {
			continue
		}
		keys = append(keys, map[string]ddbtypes.AttributeValue{"PK": pk, "SK": sk})
	}
	if len(keys) == 0 {
		return nil, nil
	}
	got, err := c.DDB.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			c.Table: {Keys: keys},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("batch get staff committees: %w", err)
	}
	rows := make([]domain.StaffCommitteeAssignment, 0, len(got.Responses[c.Table]))
	for _, raw := range got.Responses[c.Table] {
		var it staffCommitteeItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal staff-committee: %w", err)
		}
		if it.IsDeleted {
			continue
		}
		rows = append(rows, domain.StaffCommitteeAssignment{
			UserID:       it.UserID,
			CommitteeID:  it.CommitteeID,
			ConferenceID: it.ConferenceID,
			Version:      it.Version,
		})
	}
	return rows, nil
}

// GetStaffCommitteeAssignment is a direct lookup used by scope helpers.
func (c *Client) GetStaffCommitteeAssignment(ctx context.Context, committeeID, userID string) (domain.StaffCommitteeAssignment, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: staffCommitteePK(committeeID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: staffCommitteeSK(userID)},
		},
	})
	if err != nil {
		return domain.StaffCommitteeAssignment{}, fmt.Errorf("get staff-committee: %w", err)
	}
	if out.Item == nil {
		return domain.StaffCommitteeAssignment{}, ErrNotFound
	}
	var it staffCommitteeItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.StaffCommitteeAssignment{}, fmt.Errorf("unmarshal staff-committee: %w", err)
	}
	if it.IsDeleted {
		return domain.StaffCommitteeAssignment{}, ErrNotFound
	}
	return domain.StaffCommitteeAssignment{
		UserID:       it.UserID,
		CommitteeID:  it.CommitteeID,
		ConferenceID: it.ConferenceID,
		Version:      it.Version,
	}, nil
}
