package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"

	"github.com/numun/numun/api/internal/domain"
)

// delegateItem is the on-the-wire DDB shape for a Delegate row.
// PK = DELEGATION#<delegationId>, SK = DELEGATE#<delegateId> per DATA_MODEL.md §6.
// GSI2 supports listing delegates across a conference by name (consumed by M7).
type delegateItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID    string `dynamodbav:"conferenceId"`
	DelegationID    string `dynamodbav:"delegationId"`
	FirstName       string `dynamodbav:"firstName"`
	LastName        string `dynamodbav:"lastName"`
	Email           string `dynamodbav:"email,omitempty"`
	ExperienceLevel string `dynamodbav:"experienceLevel"`
	CheckedInAt     string `dynamodbav:"checkedInAt,omitempty"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`

	GSI2PK string `dynamodbav:"GSI2PK,omitempty"`
	GSI2SK string `dynamodbav:"GSI2SK,omitempty"`
}

func delegatePK(delegationID string) string { return "DELEGATION#" + delegationID }
func delegateSK(id string) string           { return "DELEGATE#" + id }
func delegateNameGSI2PK(conferenceID string) string {
	return "CONF#" + conferenceID + "#DELEGATE_NAME"
}
func delegateNameGSI2SK(d domain.Delegate) string {
	return strings.ToLower(d.LastName) + "#" + strings.ToLower(d.FirstName) + "#" + d.ID
}

func delegateToItem(d domain.Delegate) delegateItem {
	return delegateItem{
		PK:              delegatePK(d.DelegationID),
		SK:              delegateSK(d.ID),
		Entity:          "Delegate",
		ID:              d.ID,
		ConferenceID:    d.ConferenceID,
		DelegationID:    d.DelegationID,
		FirstName:       d.FirstName,
		LastName:        d.LastName,
		Email:           d.Email,
		ExperienceLevel: string(d.ExperienceLevel),
		CheckedInAt:     formatTimeOrEmpty(d.CheckedInAt),
		IsDeleted:       d.IsDeleted,
		Version:         d.Version,
		CreatedAt:       d.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:       d.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:       d.CreatedBy,
		UpdatedBy:       d.UpdatedBy,
		GSI2PK:          delegateNameGSI2PK(d.ConferenceID),
		GSI2SK:          delegateNameGSI2SK(d),
	}
}

func delegateFromItem(it delegateItem) domain.Delegate {
	d := domain.Delegate{
		ID:              it.ID,
		ConferenceID:    it.ConferenceID,
		DelegationID:    it.DelegationID,
		FirstName:       it.FirstName,
		LastName:        it.LastName,
		Email:           it.Email,
		ExperienceLevel: domain.ExperienceLevel(it.ExperienceLevel),
		IsDeleted:       it.IsDeleted,
		Version:         it.Version,
		CreatedBy:       it.CreatedBy,
		UpdatedBy:       it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CheckedInAt); err == nil {
		d.CheckedInAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		d.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		d.UpdatedAt = t
	}
	return d
}

func prepareNewDelegate(in domain.Delegate) domain.Delegate {
	if in.ID == "" {
		if id, err := uuid.NewV7(); err == nil {
			in.ID = id.String()
		}
	}
	if in.ExperienceLevel == "" {
		in.ExperienceLevel = domain.ExperienceLevelIntermediate
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	return in
}

// CreateDelegate inserts a single Delegate row.
func (c *Client) CreateDelegate(ctx context.Context, in domain.Delegate) (domain.Delegate, error) {
	in = prepareNewDelegate(in)
	it := delegateToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Delegate{}, fmt.Errorf("marshal delegate: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Delegate{}, ErrAlreadyExists
		}
		return domain.Delegate{}, fmt.Errorf("put delegate: %w", err)
	}
	return delegateFromItem(it), nil
}

// GetDelegate fetches a Delegate by delegation + id. ErrNotFound on missing or
// soft-deleted rows.
func (c *Client) GetDelegate(ctx context.Context, delegationID, id string) (domain.Delegate, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: delegatePK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: delegateSK(id)},
		},
	})
	if err != nil {
		return domain.Delegate{}, fmt.Errorf("get delegate: %w", err)
	}
	if out.Item == nil {
		return domain.Delegate{}, ErrNotFound
	}
	var it delegateItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Delegate{}, fmt.Errorf("unmarshal delegate: %w", err)
	}
	if it.IsDeleted {
		return domain.Delegate{}, ErrNotFound
	}
	return delegateFromItem(it), nil
}

// FindDelegateByID does an id-only lookup when the delegationId isn't known
// (e.g., from a scope check). v1 uses a single-row Scan with a FilterExpression.
func (c *Client) FindDelegateByID(ctx context.Context, id string) (domain.Delegate, error) {
	exprNames := map[string]string{"#e": "entity", "#del": "isDeleted"}
	exprVals := map[string]ddbtypes.AttributeValue{
		":entity": &ddbtypes.AttributeValueMemberS{Value: "Delegate"},
		":id":     &ddbtypes.AttributeValueMemberS{Value: id},
		":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:                 aws.String(c.Table),
		FilterExpression:          aws.String("#e = :entity AND id = :id AND #del = :false"),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprVals,
		Limit:                     aws.Int32(1),
	})
	if err != nil {
		return domain.Delegate{}, fmt.Errorf("find delegate: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Delegate{}, ErrNotFound
	}
	var it delegateItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.Delegate{}, fmt.Errorf("unmarshal delegate: %w", err)
	}
	return delegateFromItem(it), nil
}

// ListDelegatesByDelegation returns the paginated set of Delegate rows under a
// delegation. Soft-deleted rows are filtered out post-query.
func (c *Client) ListDelegatesByDelegation(ctx context.Context, delegationID, cursor string, pageSize int32) ([]domain.Delegate, string, error) {
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": &ddbtypes.AttributeValueMemberS{Value: delegatePK(delegationID)},
			":sk": &ddbtypes.AttributeValueMemberS{Value: "DELEGATE#"},
		},
		Limit: aws.Int32(pageSize),
	}
	if cursor != "" {
		key, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode cursor: %w", err)
		}
		in.ExclusiveStartKey = key
	}
	out, err := c.DDB.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("query delegates: %w", err)
	}
	dels := make([]domain.Delegate, 0, len(out.Items))
	for _, raw := range out.Items {
		var it delegateItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal delegate: %w", err)
		}
		if it.IsDeleted {
			continue
		}
		dels = append(dels, delegateFromItem(it))
	}
	next, err := encodeCursor(out.LastEvaluatedKey)
	if err != nil {
		return nil, "", err
	}
	return dels, next, nil
}

// ListAllDelegatesByDelegation returns every non-deleted Delegate under a
// delegation. Caller responsibility to handle the unbounded result; used by
// the bulk-import commit path to compute roster matches.
func (c *Client) ListAllDelegatesByDelegation(ctx context.Context, delegationID string) ([]domain.Delegate, error) {
	var all []domain.Delegate
	var startKey map[string]ddbtypes.AttributeValue
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: delegatePK(delegationID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "DELEGATE#"},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("query delegates: %w", err)
		}
		for _, raw := range out.Items {
			var it delegateItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("unmarshal delegate: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			all = append(all, delegateFromItem(it))
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return all, nil
}

// SearchDelegatesByConference returns Delegate rows in `conferenceId` whose
// first or last name (case-insensitive) contains `query`. Query'd via GSI2
// (PK = CONF#<id>#DELEGATE_NAME), filtered in-app. v1 NUMUN scale tops out
// in the low thousands — acceptable to scan-then-filter. Caller clamps
// the result count.
func (c *Client) SearchDelegatesByConference(ctx context.Context, conferenceID, query string, limit int) ([]domain.Delegate, bool, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = 50
	}
	var hits []domain.Delegate
	var startKey map[string]ddbtypes.AttributeValue
	truncated := false
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			IndexName:              aws.String("GSI2"),
			KeyConditionExpression: aws.String("GSI2PK = :pk"),
			FilterExpression:       aws.String("isDeleted = :false"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk":    &ddbtypes.AttributeValueMemberS{Value: delegateNameGSI2PK(conferenceID)},
				":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return nil, false, fmt.Errorf("search delegates: %w", err)
		}
		for _, raw := range out.Items {
			var it delegateItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, false, fmt.Errorf("unmarshal delegate: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			if !nameContains(it.FirstName, it.LastName, q) {
				continue
			}
			hits = append(hits, delegateFromItem(it))
			if len(hits) >= limit {
				truncated = true
				return hits, truncated, nil
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return hits, truncated, nil
}

func nameContains(firstName, lastName, lowerQuery string) bool {
	return strings.Contains(strings.ToLower(firstName), lowerQuery) ||
		strings.Contains(strings.ToLower(lastName), lowerQuery)
}

// UpdateDelegatePatch carries optional fields for partial updates.
type UpdateDelegatePatch struct {
	FirstName       *string
	LastName        *string
	Email           *string
	ExperienceLevel *domain.ExperienceLevel
}

// UpdateDelegate applies a partial update with optimistic locking.
func (c *Client) UpdateDelegate(ctx context.Context, delegationID, id string, patch UpdateDelegatePatch, expectedVersion int, actorUserID string) (domain.Delegate, error) {
	current, err := c.GetDelegate(ctx, delegationID, id)
	if err != nil {
		return domain.Delegate{}, err
	}
	if patch.FirstName != nil {
		current.FirstName = *patch.FirstName
	}
	if patch.LastName != nil {
		current.LastName = *patch.LastName
	}
	if patch.Email != nil {
		current.Email = *patch.Email
	}
	if patch.ExperienceLevel != nil {
		current.ExperienceLevel = *patch.ExperienceLevel
	}
	current.UpdatedAt = time.Now().UTC()
	current.UpdatedBy = actorUserID
	current.Version = expectedVersion + 1

	it := delegateToItem(current)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Delegate{}, fmt.Errorf("marshal delegate: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
		ExpressionAttributeNames: map[string]string{
			"#v": "version",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":ev": &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expectedVersion)},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Delegate{}, ErrVersionMismatch
		}
		return domain.Delegate{}, fmt.Errorf("update delegate: %w", err)
	}
	return delegateFromItem(it), nil
}

// SoftDeleteDelegate sets isDeleted=true with optimistic locking.
func (c *Client) SoftDeleteDelegate(ctx context.Context, delegationID, id string, expectedVersion int, actorUserID string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: delegatePK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: delegateSK(id)},
		},
		UpdateExpression:    aws.String("SET #del = :true, #v = :nv, #u = :now, #ub = :ub"),
		ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
		ExpressionAttributeNames: map[string]string{
			"#del": "isDeleted",
			"#v":   "version",
			"#u":   "updatedAt",
			"#ub":  "updatedBy",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":true": &ddbtypes.AttributeValueMemberBOOL{Value: true},
			":ev":   &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expectedVersion)},
			":nv":   &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", expectedVersion+1)},
			":now":  &ddbtypes.AttributeValueMemberS{Value: now},
			":ub":   &ddbtypes.AttributeValueMemberS{Value: actorUserID},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return ErrVersionMismatch
		}
		return fmt.Errorf("soft-delete delegate: %w", err)
	}
	return nil
}

// DelegateUpdate carries the resolved fields for a bulk-import update op.
// ExpectedVersion enforces optimistic locking against concurrent edits.
type DelegateUpdate struct {
	ID              string
	DelegationID    string
	ConferenceID    string
	ExpectedVersion int
	FirstName       string
	LastName        string
	Email           string
	ExperienceLevel domain.ExperienceLevel
	ActorUserID     string
}

// DelegateSoftDelete carries the inputs to a single soft-delete in a bulk
// commit batch.
type DelegateSoftDelete struct {
	ID              string
	DelegationID    string
	ExpectedVersion int
	ActorUserID     string
}

// ApplyBulkImportBatch applies one TransactWriteItems batch of creates +
// updates + soft-deletes. Atomic — either all writes succeed or none do.
// DDB caps a single TransactWriteItems at 100 ops; the handler chunks below
// that. See BULK_IMPORT.md §6.4.
func (c *Client) ApplyBulkImportBatch(ctx context.Context, creates []domain.Delegate, updates []DelegateUpdate, softDeletes []DelegateSoftDelete) error {
	if len(creates)+len(updates)+len(softDeletes) == 0 {
		return nil
	}
	items := make([]ddbtypes.TransactWriteItem, 0, len(creates)+len(updates)+len(softDeletes))
	now := time.Now().UTC()

	for _, d := range creates {
		d = prepareNewDelegate(d)
		d.CreatedAt = now
		d.UpdatedAt = now
		av, err := attributevalue.MarshalMap(delegateToItem(d))
		if err != nil {
			return fmt.Errorf("marshal delegate create: %w", err)
		}
		items = append(items, ddbtypes.TransactWriteItem{
			Put: &ddbtypes.Put{
				TableName:           aws.String(c.Table),
				Item:                av,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		})
	}

	for _, u := range updates {
		items = append(items, ddbtypes.TransactWriteItem{
			Update: &ddbtypes.Update{
				TableName: aws.String(c.Table),
				Key: map[string]ddbtypes.AttributeValue{
					"PK": &ddbtypes.AttributeValueMemberS{Value: delegatePK(u.DelegationID)},
					"SK": &ddbtypes.AttributeValueMemberS{Value: delegateSK(u.ID)},
				},
				UpdateExpression:    aws.String("SET firstName = :fn, lastName = :ln, email = :em, experienceLevel = :el, #v = :nv, updatedAt = :now, updatedBy = :ub, GSI2SK = :gsk"),
				ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
				ExpressionAttributeNames: map[string]string{
					"#v": "version",
				},
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
					":fn":  &ddbtypes.AttributeValueMemberS{Value: u.FirstName},
					":ln":  &ddbtypes.AttributeValueMemberS{Value: u.LastName},
					":em":  &ddbtypes.AttributeValueMemberS{Value: u.Email},
					":el":  &ddbtypes.AttributeValueMemberS{Value: string(u.ExperienceLevel)},
					":ev":  &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", u.ExpectedVersion)},
					":nv":  &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", u.ExpectedVersion+1)},
					":now": &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
					":ub":  &ddbtypes.AttributeValueMemberS{Value: u.ActorUserID},
					":gsk": &ddbtypes.AttributeValueMemberS{Value: bulkUpdateGSI2SK(u)},
				},
			},
		})
	}

	for _, d := range softDeletes {
		items = append(items, ddbtypes.TransactWriteItem{
			Update: &ddbtypes.Update{
				TableName: aws.String(c.Table),
				Key: map[string]ddbtypes.AttributeValue{
					"PK": &ddbtypes.AttributeValueMemberS{Value: delegatePK(d.DelegationID)},
					"SK": &ddbtypes.AttributeValueMemberS{Value: delegateSK(d.ID)},
				},
				UpdateExpression:    aws.String("SET isDeleted = :true, #v = :nv, updatedAt = :now, updatedBy = :ub"),
				ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
				ExpressionAttributeNames: map[string]string{
					"#v": "version",
				},
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
					":true": &ddbtypes.AttributeValueMemberBOOL{Value: true},
					":ev":   &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", d.ExpectedVersion)},
					":nv":   &ddbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", d.ExpectedVersion+1)},
					":now":  &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
					":ub":   &ddbtypes.AttributeValueMemberS{Value: d.ActorUserID},
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
					return ErrVersionMismatch
				}
			}
		}
		return fmt.Errorf("bulk import batch: %w", err)
	}
	return nil
}

func bulkUpdateGSI2SK(u DelegateUpdate) string {
	return strings.ToLower(u.LastName) + "#" + strings.ToLower(u.FirstName) + "#" + u.ID
}

// CheckInDelegate stamps checkedInAt=now (idempotent — does nothing if already set).
func (c *Client) CheckInDelegate(ctx context.Context, delegationID, id string, actorUserID string) (domain.Delegate, error) {
	now := time.Now().UTC()
	current, err := c.GetDelegate(ctx, delegationID, id)
	if err != nil {
		return domain.Delegate{}, err
	}
	if !current.CheckedInAt.IsZero() {
		return current, nil
	}
	current.CheckedInAt = now
	current.UpdatedAt = now
	current.UpdatedBy = actorUserID
	current.Version++

	it := delegateToItem(current)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Delegate{}, fmt.Errorf("marshal delegate: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return domain.Delegate{}, fmt.Errorf("check-in delegate: %w", err)
	}
	return delegateFromItem(it), nil
}
