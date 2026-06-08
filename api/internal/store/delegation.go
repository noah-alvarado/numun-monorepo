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

// delegationItem is the on-the-wire DDB shape for a Delegation row.
type delegationItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID string `dynamodbav:"conferenceId"`
	School       string `dynamodbav:"school"`

	AddressStreet     string `dynamodbav:"addressStreet,omitempty"`
	AddressCity       string `dynamodbav:"addressCity,omitempty"`
	AddressState      string `dynamodbav:"addressState,omitempty"`
	AddressPostalCode string `dynamodbav:"addressPostalCode,omitempty"`
	AddressCountry    string `dynamodbav:"addressCountry,omitempty"`

	Status string `dynamodbav:"status"`

	EstimatedTotal                 int `dynamodbav:"estimatedTotal"`
	EstimatedFinanciallyQualifying int `dynamodbav:"estimatedFinanciallyQualifying"`

	PrefTypeCrisis    string `dynamodbav:"prefTypeCrisis,omitempty"`
	PrefTypeNonCrisis string `dynamodbav:"prefTypeNonCrisis,omitempty"`
	PrefSizeSmall     string `dynamodbav:"prefSizeSmall,omitempty"`
	PrefSizeMedium    string `dynamodbav:"prefSizeMedium,omitempty"`
	PrefSizeLarge     string `dynamodbav:"prefSizeLarge,omitempty"`

	BalanceDueUnits int64 `dynamodbav:"balanceDueUnits"`
	BalanceDueCents int32 `dynamodbav:"balanceDueCents"`
	PaidInFull      bool  `dynamodbav:"paidInFull"`

	ApprovedAt string `dynamodbav:"approvedAt,omitempty"`
	ApprovedBy string `dynamodbav:"approvedBy,omitempty"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`

	// GSI2: list delegations by status within a conference.
	GSI2PK string `dynamodbav:"GSI2PK,omitempty"`
	GSI2SK string `dynamodbav:"GSI2SK,omitempty"`
}

func delegationPK(conferenceID string) string { return "CONF#" + conferenceID }
func delegationSK(id string) string           { return "DELEGATION#" + id }
func delegationStatusGSI2PK(conferenceID string, status domain.DelegationStatus) string {
	return "CONF#" + conferenceID + "#DELEGATION_STATUS#" + string(status)
}

func delegationFromItem(it delegationItem) domain.Delegation {
	d := domain.Delegation{
		ID:           it.ID,
		ConferenceID: it.ConferenceID,
		School:       it.School,
		Address: domain.Address{
			Street:     it.AddressStreet,
			City:       it.AddressCity,
			State:      it.AddressState,
			PostalCode: it.AddressPostalCode,
			Country:    it.AddressCountry,
		},
		Status: domain.DelegationStatus(it.Status),
		EstimatedDelegates: domain.EstimatedDelegates{
			Total:                 it.EstimatedTotal,
			FinanciallyQualifying: it.EstimatedFinanciallyQualifying,
		},
		CommitteePreferences: domain.CommitteePreferences{
			TypeCrisis:    domain.Trinary(it.PrefTypeCrisis),
			TypeNonCrisis: domain.Trinary(it.PrefTypeNonCrisis),
			SizeSmall:     domain.Trinary(it.PrefSizeSmall),
			SizeMedium:    domain.Trinary(it.PrefSizeMedium),
			SizeLarge:     domain.Trinary(it.PrefSizeLarge),
		},
		BalanceDueUnits: it.BalanceDueUnits,
		BalanceDueCents: it.BalanceDueCents,
		PaidInFull:      it.PaidInFull,
		ApprovedBy:      it.ApprovedBy,
		IsDeleted:       it.IsDeleted,
		Version:         it.Version,
		CreatedBy:       it.CreatedBy,
		UpdatedBy:       it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.ApprovedAt); err == nil {
		d.ApprovedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		d.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		d.UpdatedAt = t
	}
	return d
}

// CreateDelegation inserts a new Delegation row. Server assigns the UUIDv7 id
// if the caller leaves it empty. Default status is `pending`.
//
// CreateDelegationWithLead is the transactional variant for the advisor
// self-service path that also writes a DelegationAdvisor(LEAD) link in one
// TransactWriteItems call.
func (c *Client) CreateDelegation(ctx context.Context, in domain.Delegation) (domain.Delegation, error) {
	in = prepareNewDelegation(in)
	it := delegationToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.Delegation{}, fmt.Errorf("marshal delegation: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.Delegation{}, ErrAlreadyExists
		}
		return domain.Delegation{}, fmt.Errorf("put delegation: %w", err)
	}
	return delegationFromItem(it), nil
}

// CreateDelegationWithLead inserts a Delegation + its initial lead
// DelegationAdvisor link in a single TransactWriteItems. Used by advisor
// self-service create per API.md §10.5.
func (c *Client) CreateDelegationWithLead(ctx context.Context, d domain.Delegation, leadUserID, actorUserID string) (domain.Delegation, domain.DelegationAdvisor, error) {
	d = prepareNewDelegation(d)
	d.CreatedBy = actorUserID
	d.UpdatedBy = actorUserID
	delItem := delegationToItem(d)
	delAV, err := attributevalue.MarshalMap(delItem)
	if err != nil {
		return domain.Delegation{}, domain.DelegationAdvisor{}, fmt.Errorf("marshal delegation: %w", err)
	}

	now := time.Now().UTC()
	link := domain.DelegationAdvisor{
		UserID:       leadUserID,
		DelegationID: d.ID,
		ConferenceID: d.ConferenceID,
		Role:         domain.AdvisorRoleLead,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
		CreatedBy:    actorUserID,
		UpdatedBy:    actorUserID,
	}
	linkItem := advisorToItem(link)
	linkAV, err := attributevalue.MarshalMap(linkItem)
	if err != nil {
		return domain.Delegation{}, domain.DelegationAdvisor{}, fmt.Errorf("marshal advisor: %w", err)
	}

	_, err = c.DDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName:           aws.String(c.Table),
				Item:                delAV,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			}},
			{Put: &ddbtypes.Put{
				TableName:           aws.String(c.Table),
				Item:                linkAV,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			}},
		},
	})
	if err != nil {
		var txnErr *ddbtypes.TransactionCanceledException
		if errors.As(err, &txnErr) {
			for _, r := range txnErr.CancellationReasons {
				if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
					return domain.Delegation{}, domain.DelegationAdvisor{}, ErrAlreadyExists
				}
			}
		}
		return domain.Delegation{}, domain.DelegationAdvisor{}, fmt.Errorf("txn create delegation+lead: %w", err)
	}
	return delegationFromItem(delItem), advisorFromItem(linkItem), nil
}

func prepareNewDelegation(in domain.Delegation) domain.Delegation {
	if in.ID == "" {
		id, err := uuid.NewV7()
		if err == nil {
			in.ID = id.String()
		}
	}
	if in.Status == "" {
		in.Status = domain.DelegationStatusPending
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	return in
}

func delegationToItem(d domain.Delegation) delegationItem {
	return delegationItem{
		PK:                             delegationPK(d.ConferenceID),
		SK:                             delegationSK(d.ID),
		Entity:                         "Delegation",
		ID:                             d.ID,
		ConferenceID:                   d.ConferenceID,
		School:                         d.School,
		AddressStreet:                  d.Address.Street,
		AddressCity:                    d.Address.City,
		AddressState:                   d.Address.State,
		AddressPostalCode:              d.Address.PostalCode,
		AddressCountry:                 d.Address.Country,
		Status:                         string(d.Status),
		EstimatedTotal:                 d.EstimatedDelegates.Total,
		EstimatedFinanciallyQualifying: d.EstimatedDelegates.FinanciallyQualifying,
		PrefTypeCrisis:                 string(d.CommitteePreferences.TypeCrisis),
		PrefTypeNonCrisis:              string(d.CommitteePreferences.TypeNonCrisis),
		PrefSizeSmall:                  string(d.CommitteePreferences.SizeSmall),
		PrefSizeMedium:                 string(d.CommitteePreferences.SizeMedium),
		PrefSizeLarge:                  string(d.CommitteePreferences.SizeLarge),
		BalanceDueUnits:                d.BalanceDueUnits,
		BalanceDueCents:                d.BalanceDueCents,
		PaidInFull:                     d.PaidInFull,
		ApprovedAt:                     formatTimeOrEmpty(d.ApprovedAt),
		ApprovedBy:                     d.ApprovedBy,
		IsDeleted:                      d.IsDeleted,
		Version:                        d.Version,
		CreatedAt:                      d.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:                      d.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:                      d.CreatedBy,
		UpdatedBy:                      d.UpdatedBy,
		GSI2PK:                         delegationStatusGSI2PK(d.ConferenceID, d.Status),
		GSI2SK:                         d.ID,
	}
}

func formatTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}

// GetDelegation fetches a Delegation by conference + id. ErrNotFound on
// missing or soft-deleted rows.
func (c *Client) GetDelegation(ctx context.Context, conferenceID, id string) (domain.Delegation, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: delegationPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: delegationSK(id)},
		},
	})
	if err != nil {
		return domain.Delegation{}, fmt.Errorf("get delegation: %w", err)
	}
	if out.Item == nil {
		return domain.Delegation{}, ErrNotFound
	}
	var it delegationItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Delegation{}, fmt.Errorf("unmarshal delegation: %w", err)
	}
	if it.IsDeleted {
		return domain.Delegation{}, ErrNotFound
	}
	return delegationFromItem(it), nil
}

// FindDelegationByID does an id-only lookup when the conferenceId isn't known
// (e.g., from a scope check). Walks the GSI on the delegation id is not
// available; instead we query the link tables which carry both ids. For
// simplicity v1 takes a Scan with FilterExpression on `id`. Acceptable at
// scale; revisit if it becomes hot.
//
// Returns ErrNotFound when no row matches.
func (c *Client) FindDelegationByID(ctx context.Context, id string) (domain.Delegation, error) {
	exprNames := map[string]string{"#e": "entity", "#del": "isDeleted"}
	exprVals := map[string]ddbtypes.AttributeValue{
		":entity": &ddbtypes.AttributeValueMemberS{Value: "Delegation"},
		":id":     &ddbtypes.AttributeValueMemberS{Value: id},
		":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	in := &dynamodb.ScanInput{
		TableName:                 aws.String(c.Table),
		FilterExpression:          aws.String("#e = :entity AND id = :id AND #del = :false"),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprVals,
		Limit:                     aws.Int32(1),
	}
	out, err := c.DDB.Scan(ctx, in)
	if err != nil {
		return domain.Delegation{}, fmt.Errorf("find delegation: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.Delegation{}, ErrNotFound
	}
	var it delegationItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.Delegation{}, fmt.Errorf("unmarshal delegation: %w", err)
	}
	return delegationFromItem(it), nil
}

// UpdateDelegationPatch carries optional fields for partial updates.
type UpdateDelegationPatch struct {
	School               *string
	Address              *domain.Address
	EstimatedDelegates   *domain.EstimatedDelegates
	CommitteePreferences *domain.CommitteePreferences
	UpdatedBy            string
}

func (c *Client) UpdateDelegation(ctx context.Context, conferenceID, id string, expectedVersion int, p UpdateDelegationPatch) (domain.Delegation, error) {
	now := time.Now().UTC()
	upd := "SET version = :nextVersion, updatedAt = :now"
	exprNames := map[string]string{}
	exprVals := map[string]ddbtypes.AttributeValue{
		":expected":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
		":nextVersion": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
		":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	if p.School != nil {
		upd += ", school = :school"
		exprVals[":school"] = &ddbtypes.AttributeValueMemberS{Value: *p.School}
	}
	if p.Address != nil {
		upd += ", addressStreet = :street, addressCity = :city, addressState = :state, addressPostalCode = :postal, addressCountry = :country"
		exprVals[":street"] = &ddbtypes.AttributeValueMemberS{Value: p.Address.Street}
		exprVals[":city"] = &ddbtypes.AttributeValueMemberS{Value: p.Address.City}
		exprVals[":state"] = &ddbtypes.AttributeValueMemberS{Value: p.Address.State}
		exprVals[":postal"] = &ddbtypes.AttributeValueMemberS{Value: p.Address.PostalCode}
		exprVals[":country"] = &ddbtypes.AttributeValueMemberS{Value: p.Address.Country}
	}
	if p.EstimatedDelegates != nil {
		upd += ", estimatedTotal = :estTotal, estimatedFinanciallyQualifying = :estFQ"
		exprVals[":estTotal"] = &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(p.EstimatedDelegates.Total)}
		exprVals[":estFQ"] = &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(p.EstimatedDelegates.FinanciallyQualifying)}
	}
	if p.CommitteePreferences != nil {
		upd += ", prefTypeCrisis = :ptC, prefTypeNonCrisis = :ptNC, prefSizeSmall = :psS, prefSizeMedium = :psM, prefSizeLarge = :psL"
		exprVals[":ptC"] = &ddbtypes.AttributeValueMemberS{Value: string(p.CommitteePreferences.TypeCrisis)}
		exprVals[":ptNC"] = &ddbtypes.AttributeValueMemberS{Value: string(p.CommitteePreferences.TypeNonCrisis)}
		exprVals[":psS"] = &ddbtypes.AttributeValueMemberS{Value: string(p.CommitteePreferences.SizeSmall)}
		exprVals[":psM"] = &ddbtypes.AttributeValueMemberS{Value: string(p.CommitteePreferences.SizeMedium)}
		exprVals[":psL"] = &ddbtypes.AttributeValueMemberS{Value: string(p.CommitteePreferences.SizeLarge)}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :updatedBy"
		exprVals[":updatedBy"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: delegationPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: delegationSK(id)},
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
			if _, gerr := c.GetDelegation(ctx, conferenceID, id); errors.Is(gerr, ErrNotFound) {
				return domain.Delegation{}, ErrNotFound
			}
			return domain.Delegation{}, ErrVersionMismatch
		}
		return domain.Delegation{}, fmt.Errorf("update delegation: %w", err)
	}
	var it delegationItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Delegation{}, fmt.Errorf("unmarshal delegation: %w", err)
	}
	return delegationFromItem(it), nil
}

// SetDelegationStatus flips the status field and updates GSI2 in the same
// UpdateItem. Used by Approve / Reject. Sets approvedAt + approvedBy when
// status == approved.
func (c *Client) SetDelegationStatus(ctx context.Context, conferenceID, id string, expectedVersion int, status domain.DelegationStatus, actorUserID string) (domain.Delegation, error) {
	now := time.Now().UTC()
	upd := "SET version = :nextVersion, updatedAt = :now, #st = :status, GSI2PK = :gsi2pk, GSI2SK = :gsi2sk, updatedBy = :actor"
	exprNames := map[string]string{"#st": "status"}
	exprVals := map[string]ddbtypes.AttributeValue{
		":expected":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
		":nextVersion": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
		":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
		":status":      &ddbtypes.AttributeValueMemberS{Value: string(status)},
		":gsi2pk":      &ddbtypes.AttributeValueMemberS{Value: delegationStatusGSI2PK(conferenceID, status)},
		":gsi2sk":      &ddbtypes.AttributeValueMemberS{Value: id},
		":actor":       &ddbtypes.AttributeValueMemberS{Value: actorUserID},
	}
	if status == domain.DelegationStatusApproved {
		upd += ", approvedAt = :now, approvedBy = :actor"
	}
	in := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: delegationPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: delegationSK(id)},
		},
		UpdateExpression:          aws.String(upd),
		ConditionExpression:       aws.String("version = :expected AND isDeleted = :false"),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprVals,
		ReturnValues:              ddbtypes.ReturnValueAllNew,
	}
	out, err := c.DDB.UpdateItem(ctx, in)
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetDelegation(ctx, conferenceID, id); errors.Is(gerr, ErrNotFound) {
				return domain.Delegation{}, ErrNotFound
			}
			return domain.Delegation{}, ErrVersionMismatch
		}
		return domain.Delegation{}, fmt.Errorf("set delegation status: %w", err)
	}
	var it delegationItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.Delegation{}, fmt.Errorf("unmarshal delegation: %w", err)
	}
	return delegationFromItem(it), nil
}

// ListDelegationsByConference returns delegations within a conference using
// the base-table Query (PK = CONF#<id>, SK begins_with DELEGATION#).
// Soft-deleted rows are filtered server-side.
func (c *Client) ListDelegationsByConference(ctx context.Context, conferenceID, cursor string, pageSize int32) ([]domain.Delegation, string, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500
	}
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		FilterExpression:       aws.String("isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":     &ddbtypes.AttributeValueMemberS{Value: delegationPK(conferenceID)},
			":prefix": &ddbtypes.AttributeValueMemberS{Value: "DELEGATION#"},
			":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(pageSize),
	}
	if cursor != "" {
		start, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode cursor: %w", err)
		}
		in.ExclusiveStartKey = start
	}
	out, err := c.DDB.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("list delegations: %w", err)
	}
	dels := make([]domain.Delegation, 0, len(out.Items))
	for _, raw := range out.Items {
		var it delegationItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal delegation: %w", err)
		}
		dels = append(dels, delegationFromItem(it))
	}
	var next string
	if len(out.LastEvaluatedKey) > 0 {
		next, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("encode cursor: %w", err)
		}
	}
	return dels, next, nil
}

// ListDelegationsByStatus returns delegations within a conference whose status
// matches via GSI2 (PK = CONF#<id>#DELEGATION_STATUS#<status>).
func (c *Client) ListDelegationsByStatus(ctx context.Context, conferenceID string, status domain.DelegationStatus, cursor string, pageSize int32) ([]domain.Delegation, string, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500
	}
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI2"),
		KeyConditionExpression: aws.String("GSI2PK = :pk"),
		FilterExpression:       aws.String("isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":    &ddbtypes.AttributeValueMemberS{Value: delegationStatusGSI2PK(conferenceID, status)},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(pageSize),
	}
	if cursor != "" {
		start, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode cursor: %w", err)
		}
		in.ExclusiveStartKey = start
	}
	out, err := c.DDB.Query(ctx, in)
	if err != nil {
		return nil, "", fmt.Errorf("list delegations by status: %w", err)
	}
	// GSI2 is INCLUDE-projected per DATA_MODEL.md §5; for the full delegation
	// we batch-get from the base table. Cheaper than projecting everything.
	keys := make([]map[string]ddbtypes.AttributeValue, 0, len(out.Items))
	for _, raw := range out.Items {
		var it delegationItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal delegation projection: %w", err)
		}
		keys = append(keys, map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: it.PK},
			"SK": &ddbtypes.AttributeValueMemberS{Value: it.SK},
		})
	}
	dels, err := c.batchGetDelegations(ctx, keys)
	if err != nil {
		return nil, "", err
	}
	var next string
	if len(out.LastEvaluatedKey) > 0 {
		next, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("encode cursor: %w", err)
		}
	}
	return dels, next, nil
}

func (c *Client) batchGetDelegations(ctx context.Context, keys []map[string]ddbtypes.AttributeValue) ([]domain.Delegation, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	out, err := c.DDB.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			c.Table: {Keys: keys},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("batch get delegations: %w", err)
	}
	rows := out.Responses[c.Table]
	dels := make([]domain.Delegation, 0, len(rows))
	for _, raw := range rows {
		var it delegationItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, fmt.Errorf("unmarshal delegation: %w", err)
		}
		if it.IsDeleted {
			continue
		}
		dels = append(dels, delegationFromItem(it))
	}
	return dels, nil
}

// BatchGetDelegationsByKeys exposes batchGetDelegations for the scope helpers
// that resolve a user's advisor / staffer links to full Delegation rows.
func (c *Client) BatchGetDelegationsByKeys(ctx context.Context, conferenceID string, ids []string) ([]domain.Delegation, error) {
	keys := make([]map[string]ddbtypes.AttributeValue, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: delegationPK(conferenceID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: delegationSK(id)},
		})
	}
	return c.batchGetDelegations(ctx, keys)
}
