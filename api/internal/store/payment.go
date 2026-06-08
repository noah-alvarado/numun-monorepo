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

// paymentItem is the on-the-wire DDB shape for a PaymentRecord row.
// PK = DELEGATION#<delegationId>, SK = PAYMENT#<recordedAtRFC3339Nano>#<id>
// per DATA_MODEL.md §6. The SK sorts lexicographically by recordedAt; the id
// tiebreaker keeps two payments recorded in the same nanosecond distinct.
//
// Sign convention (M8): AmountUnits + AmountCents are SIGNED. Charges are
// stored as a negative ledger entry, payments as positive. The parent
// Delegation's balanceDue is derived as `balanceDue = -sum(ledger amounts)`
// (i.e. a $100 charge stored as amount=-100 raises balanceDue from 0 to 100;
// a $40 payment stored as amount=+40 drops balanceDue to 60). The handler
// owns sign assignment based on PaymentKind; the store does pure arithmetic.
type paymentItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID   string `dynamodbav:"conferenceId"`
	DelegationID   string `dynamodbav:"delegationId"`
	AmountCurrency string `dynamodbav:"amountCurrency"`
	AmountUnits    int64  `dynamodbav:"amountUnits"`
	AmountCents    int32  `dynamodbav:"amountCents"`
	Kind           string `dynamodbav:"kind"`
	Method         string `dynamodbav:"method"`
	Reference      string `dynamodbav:"reference,omitempty"`
	Notes          string `dynamodbav:"notes,omitempty"`
	RecordedBy     string `dynamodbav:"recordedBy"`
	RecordedAt     string `dynamodbav:"recordedAt"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`
}

func paymentPK(delegationID string) string { return "DELEGATION#" + delegationID }
func paymentSK(recordedAt time.Time, id string) string {
	return "PAYMENT#" + recordedAt.UTC().Format(time.RFC3339Nano) + "#" + id
}

func paymentToItem(p domain.PaymentRecord) paymentItem {
	return paymentItem{
		PK:             paymentPK(p.DelegationID),
		SK:             paymentSK(p.RecordedAt, p.ID),
		Entity:         "PaymentRecord",
		ID:             p.ID,
		ConferenceID:   p.ConferenceID,
		DelegationID:   p.DelegationID,
		AmountCurrency: p.AmountCurrency,
		AmountUnits:    p.AmountUnits,
		AmountCents:    p.AmountCents,
		Kind:           string(p.Kind),
		Method:         string(p.Method),
		Reference:      p.Reference,
		Notes:          p.Notes,
		RecordedBy:     p.RecordedBy,
		RecordedAt:     p.RecordedAt.UTC().Format(time.RFC3339Nano),
		IsDeleted:      p.IsDeleted,
		Version:        p.Version,
		CreatedAt:      p.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:      p.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:      p.CreatedBy,
		UpdatedBy:      p.UpdatedBy,
	}
}

func paymentFromItem(it paymentItem) domain.PaymentRecord {
	p := domain.PaymentRecord{
		ID:             it.ID,
		ConferenceID:   it.ConferenceID,
		DelegationID:   it.DelegationID,
		AmountCurrency: it.AmountCurrency,
		AmountUnits:    it.AmountUnits,
		AmountCents:    it.AmountCents,
		Kind:           domain.PaymentKind(it.Kind),
		Method:         domain.PaymentMethod(it.Method),
		Reference:      it.Reference,
		Notes:          it.Notes,
		RecordedBy:     it.RecordedBy,
		IsDeleted:      it.IsDeleted,
		Version:        it.Version,
		CreatedBy:      it.CreatedBy,
		UpdatedBy:      it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.RecordedAt); err == nil {
		p.RecordedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		p.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		p.UpdatedAt = t
	}
	return p
}

// addMoney sums two signed (units, cents) money pairs and normalizes the
// result so cents stay in [-99, 99] with the same sign as units (or zero).
// Both inputs are taken as int64 for safe intermediate arithmetic; the
// returned cents narrows back to int32 since |cents| < 100 always fits.
func addMoney(unitsA, centsA, unitsB, centsB int64) (int64, int32) {
	units := unitsA + unitsB
	cents := centsA + centsB
	// Carry overflowing cents into units.
	if cents >= 100 {
		units += cents / 100
		cents = cents % 100
	} else if cents <= -100 {
		units += cents / 100
		cents = cents % 100
	}
	// Reconcile mixed signs (e.g. units=1, cents=-50 → units=0, cents=50).
	if units > 0 && cents < 0 {
		units--
		cents += 100
	} else if units < 0 && cents > 0 {
		units++
		cents -= 100
	}
	return units, int32(cents)
}

// RecordPayment writes a new PaymentRecord and updates the parent Delegation's
// denormalized balanceDue / paidInFull caches in a single TransactWriteItems
// call per DATA_MODEL.md §6 S4.
//
// The caller passes a domain.PaymentRecord whose AmountUnits/AmountCents are
// already signed (charges negative, payments positive); the store applies the
// pure arithmetic `balanceDue_new = balanceDue_old - amount_signed`.
//
// Race-window caveat: optimistic locking on the Delegation row requires
// reading its current version before the transaction so we can assert
// `version = :ev` inside the txn. Between the read and the TransactWrite
// another writer can bump the version; the conditional check then fails and
// we return ErrVersionMismatch. Callers should retry.
func (c *Client) RecordPayment(ctx context.Context, in domain.PaymentRecord) (domain.PaymentRecord, error) {
	if in.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return domain.PaymentRecord{}, fmt.Errorf("uuid v7: %w", err)
		}
		in.ID = id.String()
	}
	now := time.Now().UTC()
	if in.RecordedAt.IsZero() {
		in.RecordedAt = now
	}
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	if in.AmountCurrency == "" {
		in.AmountCurrency = "USD"
	}
	// Normalize the signed amount so storage stays in canonical form.
	u, ce := addMoney(in.AmountUnits, int64(in.AmountCents), 0, 0)
	in.AmountUnits, in.AmountCents = u, ce

	// Read the current Delegation so we can OCC its version + compute the new
	// balance. Small race window between this read and the TransactWrite —
	// the conditional check on `version = :ev` keeps the txn atomic.
	del, err := c.FindDelegationByID(ctx, in.DelegationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.PaymentRecord{}, ErrNotFound
		}
		return domain.PaymentRecord{}, fmt.Errorf("find delegation: %w", err)
	}

	// balanceDue_new = balanceDue_old - amount_signed
	newUnits, newCents := addMoney(del.BalanceDueUnits, int64(del.BalanceDueCents), -in.AmountUnits, -int64(in.AmountCents))
	paidInFull := newUnits == 0 && newCents == 0

	payAV, err := attributevalue.MarshalMap(paymentToItem(in))
	if err != nil {
		return domain.PaymentRecord{}, fmt.Errorf("marshal payment: %w", err)
	}

	_, err = c.DDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName:           aws.String(c.Table),
				Item:                payAV,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			}},
			{Update: &ddbtypes.Update{
				TableName: aws.String(c.Table),
				Key: map[string]ddbtypes.AttributeValue{
					"PK": &ddbtypes.AttributeValueMemberS{Value: delegationPK(del.ConferenceID)},
					"SK": &ddbtypes.AttributeValueMemberS{Value: delegationSK(del.ID)},
				},
				UpdateExpression:    aws.String("SET balanceDueUnits = :u, balanceDueCents = :c, paidInFull = :pif, #v = :nv, updatedAt = :now, updatedBy = :ub"),
				ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev AND isDeleted = :false"),
				ExpressionAttributeNames: map[string]string{
					"#v": "version",
				},
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
					":u":     &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(newUnits, 10)},
					":c":     &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(int64(newCents), 10)},
					":pif":   &ddbtypes.AttributeValueMemberBOOL{Value: paidInFull},
					":ev":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(del.Version)},
					":nv":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(del.Version + 1)},
					":now":   &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
					":ub":    &ddbtypes.AttributeValueMemberS{Value: in.RecordedBy},
					":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
				},
			}},
		},
	})
	if err != nil {
		var txnErr *ddbtypes.TransactionCanceledException
		if errors.As(err, &txnErr) {
			for i, r := range txnErr.CancellationReasons {
				if r.Code != nil && *r.Code == "ConditionalCheckFailed" {
					// Index 0 = Put on PaymentRecord (duplicate id; treat as
					// already-exists). Index 1 = Update on Delegation (version
					// mismatch or missing). Distinguish by re-reading.
					if i == 0 {
						return domain.PaymentRecord{}, ErrAlreadyExists
					}
					if _, gerr := c.GetDelegation(ctx, del.ConferenceID, del.ID); errors.Is(gerr, ErrNotFound) {
						return domain.PaymentRecord{}, ErrNotFound
					}
					return domain.PaymentRecord{}, ErrVersionMismatch
				}
			}
		}
		return domain.PaymentRecord{}, fmt.Errorf("record payment txn: %w", err)
	}
	return in, nil
}

// GetPayment fetches a PaymentRecord by composite (delegationID, paymentID).
// Because the SK encodes recordedAt, we can't reconstruct it from id alone;
// instead we Query the delegation's PAYMENT# prefix and filter for the id
// in-app. Acceptable at NUMUN scale (≤ few dozen payments per delegation).
func (c *Client) GetPayment(ctx context.Context, delegationID, paymentID string) (domain.PaymentRecord, error) {
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: paymentPK(delegationID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "PAYMENT#"},
			},
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return domain.PaymentRecord{}, fmt.Errorf("query payments: %w", err)
		}
		for _, raw := range out.Items {
			var it paymentItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return domain.PaymentRecord{}, fmt.Errorf("unmarshal payment: %w", err)
			}
			if it.ID == paymentID {
				if it.IsDeleted {
					return domain.PaymentRecord{}, ErrNotFound
				}
				return paymentFromItem(it), nil
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return domain.PaymentRecord{}, ErrNotFound
}

// FindPaymentByID does an id-only Scan lookup; used by the scope helper that
// only knows the payment id from the request. Mirrors FindDelegationByID.
func (c *Client) FindPaymentByID(ctx context.Context, id string) (domain.PaymentRecord, error) {
	exprNames := map[string]string{"#e": "entity", "#del": "isDeleted"}
	exprVals := map[string]ddbtypes.AttributeValue{
		":entity": &ddbtypes.AttributeValueMemberS{Value: "PaymentRecord"},
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
		return domain.PaymentRecord{}, fmt.Errorf("find payment: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.PaymentRecord{}, ErrNotFound
	}
	var it paymentItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.PaymentRecord{}, fmt.Errorf("unmarshal payment: %w", err)
	}
	return paymentFromItem(it), nil
}

// ListPaymentsByDelegation returns the paginated set of PaymentRecord rows
// under a delegation, sorted newest-first (descending SK).
func (c *Client) ListPaymentsByDelegation(ctx context.Context, delegationID, cursor string, pageSize int32) ([]domain.PaymentRecord, string, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	if pageSize > 500 {
		pageSize = 500
	}
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk": &ddbtypes.AttributeValueMemberS{Value: paymentPK(delegationID)},
			":sk": &ddbtypes.AttributeValueMemberS{Value: "PAYMENT#"},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(pageSize),
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
		return nil, "", fmt.Errorf("query payments: %w", err)
	}
	rows := make([]domain.PaymentRecord, 0, len(out.Items))
	for _, raw := range out.Items {
		var it paymentItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal payment: %w", err)
		}
		if it.IsDeleted {
			continue
		}
		rows = append(rows, paymentFromItem(it))
	}
	next, err := encodeCursor(out.LastEvaluatedKey)
	if err != nil {
		return nil, "", err
	}
	return rows, next, nil
}

// ListAllPaymentsByDelegation returns every non-deleted PaymentRecord under a
// delegation, newest-first. Iterates until LastEvaluatedKey is empty. Used by
// the CSV export path; OK at NUMUN scale.
func (c *Client) ListAllPaymentsByDelegation(ctx context.Context, delegationID string) ([]domain.PaymentRecord, error) {
	var all []domain.PaymentRecord
	var startKey map[string]ddbtypes.AttributeValue
	for {
		out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: paymentPK(delegationID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "PAYMENT#"},
			},
			ScanIndexForward:  aws.Bool(false),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, fmt.Errorf("query payments: %w", err)
		}
		for _, raw := range out.Items {
			var it paymentItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return nil, fmt.Errorf("unmarshal payment: %w", err)
			}
			if it.IsDeleted {
				continue
			}
			all = append(all, paymentFromItem(it))
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return all, nil
}

// UpdatePaymentPatch carries optional fields for partial updates. Only Notes
// and Reference are patchable — changes to amount/kind/method must be
// recorded as new ledger entries so the audit trail stays intact. See
// payments.proto comments.
type UpdatePaymentPatch struct {
	Reference *string
	Notes     *string
	UpdatedBy string
}

// UpdatePayment applies a partial update with optimistic locking. The
// composite key needs the full SK; callers resolve it via FindPaymentByID
// first and pass it in.
func (c *Client) UpdatePayment(ctx context.Context, delegationID, paymentSK, id string, expectedVersion int, p UpdatePaymentPatch) (domain.PaymentRecord, error) {
	now := time.Now().UTC()
	upd := "SET version = :nextVersion, updatedAt = :now"
	exprVals := map[string]ddbtypes.AttributeValue{
		":expected":    &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
		":nextVersion": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
		":now":         &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
		":false":       &ddbtypes.AttributeValueMemberBOOL{Value: false},
	}
	if p.Reference != nil {
		upd += ", reference = :ref"
		exprVals[":ref"] = &ddbtypes.AttributeValueMemberS{Value: *p.Reference}
	}
	if p.Notes != nil {
		upd += ", notes = :notes"
		exprVals[":notes"] = &ddbtypes.AttributeValueMemberS{Value: *p.Notes}
	}
	if p.UpdatedBy != "" {
		upd += ", updatedBy = :ub"
		exprVals[":ub"] = &ddbtypes.AttributeValueMemberS{Value: p.UpdatedBy}
	}

	out, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: paymentPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: paymentSK},
		},
		UpdateExpression:          aws.String(upd),
		ConditionExpression:       aws.String("version = :expected AND isDeleted = :false"),
		ExpressionAttributeValues: exprVals,
		ReturnValues:              ddbtypes.ReturnValueAllNew,
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			if _, gerr := c.GetPayment(ctx, delegationID, id); errors.Is(gerr, ErrNotFound) {
				return domain.PaymentRecord{}, ErrNotFound
			}
			return domain.PaymentRecord{}, ErrVersionMismatch
		}
		return domain.PaymentRecord{}, fmt.Errorf("update payment: %w", err)
	}
	var it paymentItem
	if err := attributevalue.UnmarshalMap(out.Attributes, &it); err != nil {
		return domain.PaymentRecord{}, fmt.Errorf("unmarshal payment: %w", err)
	}
	return paymentFromItem(it), nil
}

// SoftDeletePayment marks a PaymentRecord deleted with OCC AND reverses its
// balance impact on the parent Delegation in a single TransactWriteItems.
// Without the reversal the denormalized balanceDue cache would drift away
// from the (post-delete) sum of the ledger.
//
// Race-window caveat: same as RecordPayment — we read the current payment +
// delegation versions before the txn so the conditional check can enforce
// OCC. Concurrent writers between read and txn yield ErrVersionMismatch.
func (c *Client) SoftDeletePayment(ctx context.Context, delegationID, paymentSK string, expectedVersion int, actorUserID string) error {
	// Resolve the payment so we know the signed amount to reverse + the
	// parent delegation's conferenceId and current version.
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: paymentPK(delegationID)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: paymentSK},
		},
	})
	if err != nil {
		return fmt.Errorf("get payment: %w", err)
	}
	if out.Item == nil {
		return ErrNotFound
	}
	var it paymentItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return fmt.Errorf("unmarshal payment: %w", err)
	}
	if it.IsDeleted {
		return ErrNotFound
	}

	del, err := c.GetDelegation(ctx, it.ConferenceID, it.DelegationID)
	if err != nil {
		return fmt.Errorf("get delegation: %w", err)
	}

	// Reversal: deleting a ledger entry with signed amount A returns
	// balanceDue from (B - A) back to B. So newBalance = oldBalance + A.
	newUnits, newCents := addMoney(del.BalanceDueUnits, int64(del.BalanceDueCents), it.AmountUnits, int64(it.AmountCents))
	paidInFull := newUnits == 0 && newCents == 0

	now := time.Now().UTC()
	_, err = c.DDB.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Update: &ddbtypes.Update{
				TableName: aws.String(c.Table),
				Key: map[string]ddbtypes.AttributeValue{
					"PK": &ddbtypes.AttributeValueMemberS{Value: paymentPK(delegationID)},
					"SK": &ddbtypes.AttributeValueMemberS{Value: paymentSK},
				},
				UpdateExpression:    aws.String("SET isDeleted = :true, #v = :nv, updatedAt = :now, updatedBy = :ub"),
				ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
				ExpressionAttributeNames: map[string]string{
					"#v": "version",
				},
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
					":true": &ddbtypes.AttributeValueMemberBOOL{Value: true},
					":ev":   &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
					":nv":   &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion + 1)},
					":now":  &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
					":ub":   &ddbtypes.AttributeValueMemberS{Value: actorUserID},
				},
			}},
			{Update: &ddbtypes.Update{
				TableName: aws.String(c.Table),
				Key: map[string]ddbtypes.AttributeValue{
					"PK": &ddbtypes.AttributeValueMemberS{Value: delegationPK(del.ConferenceID)},
					"SK": &ddbtypes.AttributeValueMemberS{Value: delegationSK(del.ID)},
				},
				UpdateExpression:    aws.String("SET balanceDueUnits = :u, balanceDueCents = :c, paidInFull = :pif, #v = :dnv, updatedAt = :now, updatedBy = :ub"),
				ConditionExpression: aws.String("attribute_exists(PK) AND #v = :dev AND isDeleted = :false"),
				ExpressionAttributeNames: map[string]string{
					"#v": "version",
				},
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
					":u":     &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(newUnits, 10)},
					":c":     &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(int64(newCents), 10)},
					":pif":   &ddbtypes.AttributeValueMemberBOOL{Value: paidInFull},
					":dev":   &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(del.Version)},
					":dnv":   &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(del.Version + 1)},
					":now":   &ddbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339Nano)},
					":ub":    &ddbtypes.AttributeValueMemberS{Value: actorUserID},
					":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
				},
			}},
		},
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
		return fmt.Errorf("soft-delete payment txn: %w", err)
	}
	return nil
}
