package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// assignmentItem is the on-the-wire DDB shape for an Assignment row.
//
// Base table: PK = POSITION#<positionId>, SK = DELEGATE#<delegateId> per
// DATA_MODEL.md §6 ("POSITION#<positionId> as PK for Assignment because the
// dominant query is 'who is on this position?'").
//
// GSI1 design choice (v1): single GSI1 schema keyed by DELEGATE#<delegateId>
// for single-row delegate→position lookup (DATA_MODEL.md A1/A12 lines 395).
// The other documented pattern — DELEGATION#<id>→ASSIGNED#... (line 396) — is
// NOT populated on GSI1 in v1. Delegation-side rollups are computed by:
//  1. Listing delegates under the delegation (existing repo).
//  2. Issuing a BatchGetItem keyed by GSI1 lookups (queryable per delegate),
//     or by walking the base table position-by-position.
//
// This keeps each Assignment row in exactly one GSI1 partition, which avoids
// the overhead and complexity of a second sparse index. Acceptable at NUMUN
// scale; revisit if delegation-rollup queries become hot.
type assignmentItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id,omitempty"`

	ConferenceID string `dynamodbav:"conferenceId"`
	DelegateID   string `dynamodbav:"delegateId"`
	PositionID   string `dynamodbav:"positionId"`
	CommitteeID  string `dynamodbav:"committeeId"`
	DelegationID string `dynamodbav:"delegationId"`

	Status     string  `dynamodbav:"status"`
	ProposedAt string  `dynamodbav:"proposedAt,omitempty"`
	ApprovedAt string  `dynamodbav:"approvedAt,omitempty"`
	ApprovedBy string  `dynamodbav:"approvedBy,omitempty"`
	RunID      string  `dynamodbav:"runId,omitempty"`
	Score      float64 `dynamodbav:"score"`
	Reason     string  `dynamodbav:"reason,omitempty"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`

	// GSI1: per-delegate reverse lookup. See file-level comment.
	GSI1PK string `dynamodbav:"GSI1PK,omitempty"`
	GSI1SK string `dynamodbav:"GSI1SK,omitempty"`
}

func assignmentPK(positionID string) string { return "POSITION#" + positionID }
func assignmentSK(delegateID string) string { return "DELEGATE#" + delegateID }
func assignmentGSI1PK(delegateID string) string {
	return "DELEGATE#" + delegateID
}
func assignmentGSI1SK(positionID string) string {
	return "ASSIGNED_TO#" + positionID
}

func assignmentToItem(a domain.Assignment) assignmentItem {
	return assignmentItem{
		PK:           assignmentPK(a.PositionID),
		SK:           assignmentSK(a.DelegateID),
		Entity:       "Assignment",
		ID:           a.ID,
		ConferenceID: a.ConferenceID,
		DelegateID:   a.DelegateID,
		PositionID:   a.PositionID,
		CommitteeID:  a.CommitteeID,
		DelegationID: a.DelegationID,
		Status:       string(a.Status),
		ProposedAt:   formatTimeOrEmpty(a.ProposedAt),
		ApprovedAt:   formatTimeOrEmpty(a.ApprovedAt),
		ApprovedBy:   a.ApprovedBy,
		RunID:        a.RunID,
		Score:        a.Score,
		Reason:       a.Reason,
		IsDeleted:    a.IsDeleted,
		Version:      a.Version,
		CreatedAt:    a.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:    a.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:    a.CreatedBy,
		UpdatedBy:    a.UpdatedBy,
		GSI1PK:       assignmentGSI1PK(a.DelegateID),
		GSI1SK:       assignmentGSI1SK(a.PositionID),
	}
}

func assignmentFromItem(it assignmentItem) domain.Assignment {
	a := domain.Assignment{
		ID:           it.ID,
		ConferenceID: it.ConferenceID,
		DelegateID:   it.DelegateID,
		PositionID:   it.PositionID,
		CommitteeID:  it.CommitteeID,
		DelegationID: it.DelegationID,
		Status:       domain.AssignmentStatus(it.Status),
		ApprovedBy:   it.ApprovedBy,
		RunID:        it.RunID,
		Score:        it.Score,
		Reason:       it.Reason,
		IsDeleted:    it.IsDeleted,
		Version:      it.Version,
		CreatedBy:    it.CreatedBy,
		UpdatedBy:    it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.ProposedAt); err == nil {
		a.ProposedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.ApprovedAt); err == nil {
		a.ApprovedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		a.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		a.UpdatedAt = t
	}
	return a
}

func prepareNewAssignment(in domain.Assignment) domain.Assignment {
	if in.ID == "" {
		if id, err := uuid.NewV7(); err == nil {
			in.ID = id.String()
		}
	}
	if in.Status == "" {
		in.Status = domain.AssignmentStatusProposed
	}
	now := time.Now().UTC()
	if in.ProposedAt.IsZero() {
		in.ProposedAt = now
	}
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	return in
}

// CreateAssignment inserts a single Assignment row.
func (c *Client) CreateAssignment(ctx context.Context, in domain.Assignment) (domain.Assignment, error) {
	in = prepareNewAssignment(in)
	it := assignmentToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("marshal assignment: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Assignment{}, ErrAlreadyExists
		}
		return domain.Assignment{}, fmt.Errorf("put assignment: %w", err)
	}
	return assignmentFromItem(it), nil
}

// GetAssignment fetches an Assignment by position + delegate ids.
func (c *Client) GetAssignment(ctx context.Context, positionID, delegateID string) (domain.Assignment, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(delegateID)},
		},
	})
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("get assignment: %w", err)
	}
	if out.Item == nil {
		return domain.Assignment{}, ErrNotFound
	}
	var it assignmentItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment: %w", err)
	}
	if it.IsDeleted {
		return domain.Assignment{}, ErrNotFound
	}
	return assignmentFromItem(it), nil
}

// FindAssignmentByID does an id-only lookup.
func (c *Client) FindAssignmentByID(ctx context.Context, id string) (domain.Assignment, error) {
	item, err := c.getLiveByEntityID(ctx, "Assignment", id)
	if err != nil {
		return domain.Assignment{}, err
	}
	var it assignmentItem
	if err := attributevalue.UnmarshalMap(item, &it); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment: %w", err)
	}
	return assignmentFromItem(it), nil
}

// ListAssignmentsByPosition queries base table for assignments under a single
// position.
func (c *Client) ListAssignmentsByPosition(ctx context.Context, positionID string) ([]domain.Assignment, error) {
	var all []domain.Assignment
	var startKey map[string]ddbtypes.AttributeValue
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "DELEGATE#"},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("query assignments: %w", err)
		}
		for _, raw := range out.Items {
			var it assignmentItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("unmarshal assignment: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			all = append(all, assignmentFromItem(it))
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return all, nil
}

// FindAssignmentByDelegate looks up the active assignment for a delegate via
// GSI1 (PK = DELEGATE#<id>).
func (c *Client) FindAssignmentByDelegate(ctx context.Context, delegateID string) (domain.Assignment, error) {
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk AND begins_with(GSI1SK, :sk)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": &ddbtypes.AttributeValueMemberS{Value: assignmentGSI1PK(delegateID)},
			":sk": &ddbtypes.AttributeValueMemberS{Value: "ASSIGNED_TO#"},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("query assignment by delegate: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Assignment{}, ErrNotFound
	}
	// GSI1 is KEYS_ONLY per DATA_MODEL.md §5; re-fetch from base table.
	var stub assignmentItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &stub); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment projection: %w", err)
	}
	get, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: stub.PK},
			"SK": &ddbtypes.AttributeValueMemberS{Value: stub.SK},
		},
	})
	if err != nil {
		return domain.Assignment{}, fmt.Errorf("get assignment after gsi1 lookup: %w", err)
	}
	if get.Item == nil {
		return domain.Assignment{}, ErrNotFound
	}
	var it assignmentItem
	if err := attributevalue.UnmarshalMap(get.Item, &it); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment: %w", err)
	}
	if it.IsDeleted {
		return domain.Assignment{}, ErrNotFound
	}
	return assignmentFromItem(it), nil
}

// ListAllAssignmentsByConference returns every non-deleted Assignment under a
// conference, optionally filtered in-app by committee, delegation, or status.
//
// Since assignments are keyed under POSITION (not CONF), this walks
// committees → positions → assignments. O(committees × positions ×
// assignments) — comfortably <1000 reads at NUMUN scale per the task spec.
func (c *Client) ListAllAssignmentsByConference(ctx context.Context, conferenceID, filterCommitteeID, filterDelegationID string, filterStatus domain.AssignmentStatus) ([]domain.Assignment, error) {
	committees, err := c.ListCommitteesByConference(ctx, conferenceID)
	if err != nil {
		return nil, err
	}
	var all []domain.Assignment
	for _, committee := range committees {
		if filterCommitteeID != "" && committee.ID != filterCommitteeID {
			continue
		}
		positions, err := c.ListPositionsByCommittee(ctx, committee.ID)
		if err != nil {
			return nil, err
		}
		for _, position := range positions {
			rows, err := c.ListAssignmentsByPosition(ctx, position.ID)
			if err != nil {
				return nil, err
			}
			for _, a := range rows {
				if filterDelegationID != "" && a.DelegationID != filterDelegationID {
					continue
				}
				if filterStatus != "" && a.Status != filterStatus {
					continue
				}
				all = append(all, a)
			}
		}
	}
	return all, nil
}

// UpdateAssignmentPatch carries optional fields for partial updates. The
// algorithm output goes through WriteAssignmentBatch; this is the manual-edit
// path.
type UpdateAssignmentPatch struct {
	Score     *float64
	Reason    *string
	UpdatedBy string
}

// UpdateAssignment applies a partial update with optimistic locking.
func (c *Client) UpdateAssignment(ctx context.Context, positionID, delegateID string, expectedVersion int, p UpdateAssignmentPatch) (domain.Assignment, error) {
	now := time.Now().UTC()
	upd := "SET version = :nextVersion, updatedAt = :now"
	exprVals := map[string]ddbtypes.AttributeValue{
		":expected":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
		":nextVersion": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
		":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	if p.Score != nil {
		upd += ", score = :score"
		exprVals[":score"] = &ddbtypes.AttributeValueMemberN{Value: strconv.FormatFloat(*p.Score, 'f', -1, 64)}
	}
	if p.Reason != nil {
		upd += ", reason = :reason"
		exprVals[":reason"] = &ddbtypes.AttributeValueMemberS{Value: *p.Reason}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}
	out, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(delegateID)},
		},
		UpdateExpression:          aws.String(upd),
		ConditionExpression:       aws.String("version = :expected AND isDeleted = :false"),
		ExpressionAttributeValues: exprVals,
		ReturnValues:              ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetAssignment(ctx, positionID, delegateID); errors.Is(gerr, ErrNotFound) {
				return domain.Assignment{}, ErrNotFound
			}
			return domain.Assignment{}, ErrVersionMismatch
		}
		return domain.Assignment{}, fmt.Errorf("update assignment: %w", err)
	}
	var it assignmentItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment: %w", err)
	}
	return assignmentFromItem(it), nil
}

// ApproveAssignment flips proposed → approved with optimistic locking.
func (c *Client) ApproveAssignment(ctx context.Context, positionID, delegateID string, expectedVersion int, actorUserID string) (domain.Assignment, error) {
	now := time.Now().UTC()
	out, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(delegateID)},
		},
		UpdateExpression:    aws.String("SET #st = :approved, approvedAt = :now, approvedBy = :actor, version = :nv, updatedAt = :now, updatedBy = :actor"),
		ConditionExpression: aws.String("version = :ev AND isDeleted = :false AND #st = :proposed"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":approved": &ddbtypes.AttributeValueMemberS{Value: string(domain.AssignmentStatusApproved)},
			":proposed": &ddbtypes.AttributeValueMemberS{Value: string(domain.AssignmentStatusProposed)},
			":now":      &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
			":actor":    &ddbtypes.AttributeValueMemberS{Value: actorUserID},
			":ev":       &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
			":nv":       &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
			":false":    &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		ReturnValues: ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetAssignment(ctx, positionID, delegateID); errors.Is(gerr, ErrNotFound) {
				return domain.Assignment{}, ErrNotFound
			}
			return domain.Assignment{}, ErrVersionMismatch
		}
		return domain.Assignment{}, fmt.Errorf("approve assignment: %w", err)
	}
	var it assignmentItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment: %w", err)
	}
	return assignmentFromItem(it), nil
}

// UnapproveAssignment flips approved → proposed with optimistic locking and
// clears approvedAt / approvedBy.
func (c *Client) UnapproveAssignment(ctx context.Context, positionID, delegateID string, expectedVersion int, actorUserID string) (domain.Assignment, error) {
	now := time.Now().UTC()
	out, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(delegateID)},
		},
		UpdateExpression:    aws.String("SET #st = :proposed, version = :nv, updatedAt = :now, updatedBy = :actor REMOVE approvedAt, approvedBy"),
		ConditionExpression: aws.String("version = :ev AND isDeleted = :false AND #st = :approved"),
		ExpressionAttributeNames: map[string]string{
			"#st": "status",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":approved": &ddbtypes.AttributeValueMemberS{Value: string(domain.AssignmentStatusApproved)},
			":proposed": &ddbtypes.AttributeValueMemberS{Value: string(domain.AssignmentStatusProposed)},
			":now":      &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
			":actor":    &ddbtypes.AttributeValueMemberS{Value: actorUserID},
			":ev":       &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
			":nv":       &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
			":false":    &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		ReturnValues: ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetAssignment(ctx, positionID, delegateID); errors.Is(gerr, ErrNotFound) {
				return domain.Assignment{}, ErrNotFound
			}
			return domain.Assignment{}, ErrVersionMismatch
		}
		return domain.Assignment{}, fmt.Errorf("unapprove assignment: %w", err)
	}
	var it assignmentItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Assignment{}, fmt.Errorf("unmarshal assignment: %w", err)
	}
	return assignmentFromItem(it), nil
}

// SoftDeleteAssignment marks the row deleted with optimistic locking.
func (c *Client) SoftDeleteAssignment(ctx context.Context, positionID, delegateID string, expectedVersion int, actorUserID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(delegateID)},
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
		return fmt.Errorf("soft-delete assignment: %w", err)
	}
	return nil
}

// MoveAssignments atomically soft-deletes each `from` row (conditioned on its
// version) and creates each `to` row. Manual edits and swaps change a row's
// PK/SK, so they are a delete plus a create; doing both in one transaction
// means a failure leaves every row as it was. A `to` row may reuse the key of
// a soft-deleted tombstone (e.g. swapping back), but never a live row.
//
// Returns ErrVersionMismatch if a `from` row changed or is gone, and
// ErrAlreadyExists if a `to` key holds a live assignment.
func (c *Client) MoveAssignments(ctx context.Context, from, to []domain.Assignment, actorUserID string) ([]domain.Assignment, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	items := make([]ddbtypes.TransactWriteItem, 0, len(from)+len(to))
	for _, a := range from {
		items = append(items, ddbtypes.TransactWriteItem{Update: &ddbtypes.Update{
			TableName:           aws.String(c.Table),
			Key:                 assignmentKeyAV(a.PositionID, a.DelegateID),
			UpdateExpression:    aws.String("SET isDeleted = :true, version = :nv, updatedAt = :now, updatedBy = :actor"),
			ConditionExpression: aws.String("version = :ev AND isDeleted = :false"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":true":  &ddbtypes.AttributeValueMemberBOOL{Value: true},
				":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
				":ev":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(a.Version)},
				":nv":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(a.Version + 1)},
				":now":   &ddbtypes.AttributeValueMemberS{Value: now},
				":actor": &ddbtypes.AttributeValueMemberS{Value: actorUserID},
			},
		}})
	}
	created := make([]domain.Assignment, 0, len(to))
	for _, a := range to {
		a = prepareNewAssignment(a)
		created = append(created, a)
		av, err := attributevalue.MarshalMap(assignmentToItem(a))
		if err != nil {
			return nil, fmt.Errorf("marshal assignment: %w", err)
		}
		items = append(items, ddbtypes.TransactWriteItem{Put: &ddbtypes.Put{
			TableName:           aws.String(c.Table),
			Item:                av,
			ConditionExpression: aws.String("attribute_not_exists(PK) OR isDeleted = :true"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":true": &ddbtypes.AttributeValueMemberBOOL{Value: true},
			},
		}})
	}
	_, err := c.DDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: items})
	if err != nil {
		var txnErr *ddbtypes.TransactionCanceledException
		if errors.As(err, &txnErr) {
			for i, r := range txnErr.CancellationReasons {
				if r.Code == nil || *r.Code != "ConditionalCheckFailed" {
					continue
				}
				if i < len(from) {
					return nil, ErrVersionMismatch
				}
				return nil, ErrAlreadyExists
			}
		}
		return nil, fmt.Errorf("move assignments: %w", err)
	}
	return created, nil
}

// WriteAssignmentBatch lands a single TransactWriteItems containing up to 25
// creates + 25 deletes (DDB hard cap is 100, but the runtime keeps it conservative).
// Callers chunk larger batches. Used by the algorithm integration handler to
// persist proposal results per DATA_MODEL.md §6 S8.
func (c *Client) WriteAssignmentBatch(ctx context.Context, runID string, toCreate []domain.Assignment, toDelete []domain.Assignment) error {
	if len(toCreate)+len(toDelete) == 0 {
		return nil
	}
	if len(toCreate)+len(toDelete) > 100 {
		return fmt.Errorf("write assignment batch: %d ops exceeds DDB transaction limit", len(toCreate)+len(toDelete))
	}
	items := make([]ddbtypes.TransactWriteItem, 0, len(toCreate)+len(toDelete))
	for _, a := range toCreate {
		a = prepareNewAssignment(a)
		if runID != "" {
			a.RunID = runID
		}
		av, err := attributevalue.MarshalMap(assignmentToItem(a))
		if err != nil {
			return fmt.Errorf("marshal assignment create: %w", err)
		}
		items = append(items, ddbtypes.TransactWriteItem{
			Put: &ddbtypes.Put{
				TableName:           aws.String(c.Table),
				Item:                av,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		})
	}
	for _, a := range toDelete {
		items = append(items, ddbtypes.TransactWriteItem{
			Delete: &ddbtypes.Delete{
				TableName: aws.String(c.Table),
				Key: map[string]ddbtypes.AttributeValue{
					"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(a.PositionID)},
					"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(a.DelegateID)},
				},
			},
		})
	}
	_, err := c.DDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: items,
	})
	if err != nil {
		var txnErr *ddbtypes.TransactionCanceledException
		if errors.As(err, &txnErr) {
			for _, r := range txnErr.CancellationReasons {
				if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
					return ErrAlreadyExists
				}
			}
		}
		return fmt.Errorf("write assignment batch: %w", err)
	}
	return nil
}

// ErrProposalRollbackIncomplete wraps a ReplaceProposedAssignments failure
// whose rollback could not restore every prior proposed row.
var ErrProposalRollbackIncomplete = errors.New("proposal rollback incomplete")

// proposalTxnSize keeps each TransactWriteItems well under DDB's 100-op cap.
const proposalTxnSize = 25

const proposalRollbackWorkers = 8

// proposalRollbackBudget bounds the rollback, which runs detached from the
// request context so a cancelled or timed-out request still restores state.
// Sized so algorithm soft cap (20s) + writes + rollback fit the 29s Lambda.
const proposalRollbackBudget = 6 * time.Second

// ReplaceProposedAssignments swaps a conference's proposed assignments for
// rows (one algorithm run's output) without ever committing a partial set —
// ASSIGNMENT_ALGORITHM.md §1 "no partial output", §7.2.
//
// The set is too large for one DDB transaction, so it goes in two phases:
//  1. Write rows in chunked transactions. A row may overwrite a prior proposed
//     row or a soft-deleted tombstone at the same key, never an approved one.
//  2. Delete prior proposed rows the new set didn't overwrite, conditioned on
//     them still being proposed (an approval made mid-run aborts the swap).
//
// If either phase fails, every prior proposed row is restored and every new
// row at a key that had no prior proposal is removed. If that rollback fails
// too, the returned error wraps ErrProposalRollbackIncomplete.
func (c *Client) ReplaceProposedAssignments(ctx context.Context, conferenceID, runID string, rows []domain.Assignment) error {
	prior, err := c.ListAllAssignmentsByConference(ctx, conferenceID, "", "", domain.AssignmentStatusProposed)
	if err != nil {
		return err
	}
	priorItems := make(map[string]map[string]ddbtypes.AttributeValue, len(prior))
	for _, a := range prior {
		av, err := attributevalue.MarshalMap(assignmentToItem(a))
		if err != nil {
			return fmt.Errorf("marshal prior assignment: %w", err)
		}
		priorItems[assignmentKey(a)] = av
	}

	newKeys := make(map[string]bool, len(rows))
	puts := make([]ddbtypes.TransactWriteItem, 0, len(rows))
	for _, a := range rows {
		a = prepareNewAssignment(a)
		a.RunID = runID
		newKeys[assignmentKey(a)] = true
		av, err := attributevalue.MarshalMap(assignmentToItem(a))
		if err != nil {
			return fmt.Errorf("marshal assignment: %w", err)
		}
		puts = append(puts, ddbtypes.TransactWriteItem{Put: &ddbtypes.Put{
			TableName:           aws.String(c.Table),
			Item:                av,
			ConditionExpression: aws.String("attribute_not_exists(PK) OR isDeleted = :true OR #st = :proposed"),
			ExpressionAttributeNames: map[string]string{
				"#st": "status",
			},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":true":     &ddbtypes.AttributeValueMemberBOOL{Value: true},
				":proposed": &ddbtypes.AttributeValueMemberS{Value: string(domain.AssignmentStatusProposed)},
			},
		}})
	}

	var deletes []ddbtypes.TransactWriteItem
	for _, a := range prior {
		if newKeys[assignmentKey(a)] {
			continue
		}
		deletes = append(deletes, ddbtypes.TransactWriteItem{Delete: &ddbtypes.Delete{
			TableName:           aws.String(c.Table),
			Key:                 assignmentKeyAV(a.PositionID, a.DelegateID),
			ConditionExpression: aws.String("#st = :proposed AND isDeleted = :false"),
			ExpressionAttributeNames: map[string]string{
				"#st": "status",
			},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":false":    &ddbtypes.AttributeValueMemberBOOL{Value: false},
				":proposed": &ddbtypes.AttributeValueMemberS{Value: string(domain.AssignmentStatusProposed)},
			},
		}})
	}

	if err := c.transactInChunks(ctx, puts); err != nil {
		return c.rollbackProposal(ctx, runID, rows, priorItems, fmt.Errorf("write proposed assignments: %w", err))
	}
	if err := c.transactInChunks(ctx, deletes); err != nil {
		return c.rollbackProposal(ctx, runID, rows, priorItems, fmt.Errorf("discard prior proposed assignments: %w", err))
	}
	return nil
}

func (c *Client) transactInChunks(ctx context.Context, ops []ddbtypes.TransactWriteItem) error {
	for i := 0; i < len(ops); i += proposalTxnSize {
		end := min(i+proposalTxnSize, len(ops))
		if _, err := c.DDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
			TransactItems: ops[i:end],
		}); err != nil {
			return fmt.Errorf("ops %d–%d: %w", i, end-1, err)
		}
	}
	return nil
}

// rollbackProposal undoes a partially applied ReplaceProposedAssignments.
// Each op is conditional so it only touches rows this run changed: prior rows
// are restored only where they are now missing or carry runID, and new rows
// are deleted only where they carry runID.
func (c *Client) rollbackProposal(ctx context.Context, runID string, rows []domain.Assignment, priorItems map[string]map[string]ddbtypes.AttributeValue, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), proposalRollbackBudget)
	defer cancel()

	ownedByRun := map[string]ddbtypes.AttributeValue{
		":run": &ddbtypes.AttributeValueMemberS{Value: runID},
	}
	var ops []func() error
	for _, item := range priorItems {
		ops = append(ops, func() error {
			_, err := c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
				TableName:                 aws.String(c.Table),
				Item:                      item,
				ConditionExpression:       aws.String("attribute_not_exists(PK) OR runId = :run"),
				ExpressionAttributeValues: ownedByRun,
			})
			return err
		})
	}
	for _, a := range rows {
		if _, ok := priorItems[assignmentKey(a)]; ok {
			continue
		}
		ops = append(ops, func() error {
			_, err := c.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
				TableName:                 aws.String(c.Table),
				Key:                       assignmentKeyAV(a.PositionID, a.DelegateID),
				ConditionExpression:       aws.String("runId = :run"),
				ExpressionAttributeValues: ownedByRun,
			})
			return err
		})
	}

	var (
		mu     sync.Mutex
		failed []error
		wg     sync.WaitGroup
	)
	work := make(chan func() error)
	for range proposalRollbackWorkers {
		wg.Go(func() {
			for op := range work {
				err := op()
				var ccf *ddbtypes.ConditionalCheckFailedException
				if err != nil && !errors.As(err, &ccf) {
					mu.Lock()
					failed = append(failed, err)
					mu.Unlock()
				}
			}
		})
	}
	for _, op := range ops {
		work <- op
	}
	close(work)
	wg.Wait()

	if len(failed) > 0 {
		return fmt.Errorf("%w (%d of %d ops failed, first: %v): %w", ErrProposalRollbackIncomplete, len(failed), len(ops), failed[0], cause)
	}
	return cause
}

func assignmentKey(a domain.Assignment) string {
	return assignmentPK(a.PositionID) + "|" + assignmentSK(a.DelegateID)
}

func assignmentKeyAV(positionID, delegateID string) map[string]ddbtypes.AttributeValue {
	return map[string]ddbtypes.AttributeValue{
		"PK": &ddbtypes.AttributeValueMemberS{Value: assignmentPK(positionID)},
		"SK": &ddbtypes.AttributeValueMemberS{Value: assignmentSK(delegateID)},
	}
}
