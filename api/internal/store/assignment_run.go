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

// assignmentRunItem is the on-the-wire DDB shape for an AssignmentRun row.
// PK = CONF#<conferenceId>, SK = ASSIGNMENT_RUN#<triggeredAt>#<id> per
// DATA_MODEL.md §6. GSI2 carries an in-flight lookup keyed by
// CONF#<conferenceId>#ASSIGNMENT_RUN_STATUS#<status>.
type assignmentRunItem struct {
	PK     string `dynamodbav:"PK"`
	SK     string `dynamodbav:"SK"`
	Entity string `dynamodbav:"entity"`
	ID     string `dynamodbav:"id"`

	ConferenceID    string  `dynamodbav:"conferenceId"`
	Seed            uint64  `dynamodbav:"seed"`
	RunOrdinal      int     `dynamodbav:"runOrdinal"`
	IsCanonical     bool    `dynamodbav:"isCanonical"`
	TriggeredBy     string  `dynamodbav:"triggeredBy,omitempty"`
	TriggeredAt     string  `dynamodbav:"triggeredAt"`
	CompletedAt     string  `dynamodbav:"completedAt,omitempty"`
	Status          string  `dynamodbav:"status"`
	Objective       float64 `dynamodbav:"objective"`
	AssignmentCount int     `dynamodbav:"assignmentCount"`
	InputsHash      string  `dynamodbav:"inputsHash,omitempty"`
	Diagnostics     string  `dynamodbav:"diagnostics,omitempty"`

	IsDeleted bool   `dynamodbav:"isDeleted"`
	Version   int    `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"createdAt"`
	UpdatedAt string `dynamodbav:"updatedAt"`
	CreatedBy string `dynamodbav:"createdBy,omitempty"`
	UpdatedBy string `dynamodbav:"updatedBy,omitempty"`

	GSI2PK string `dynamodbav:"GSI2PK,omitempty"`
	GSI2SK string `dynamodbav:"GSI2SK,omitempty"`
}

func assignmentRunPK(conferenceID string) string { return "CONF#" + conferenceID }
func assignmentRunSK(triggeredAt time.Time, id string) string {
	return "ASSIGNMENT_RUN#" + triggeredAt.UTC().Format(time.RFC3339Nano) + "#" + id
}
func assignmentRunGSI2PK(conferenceID string, status domain.AssignmentRunStatus) string {
	return "CONF#" + conferenceID + "#ASSIGNMENT_RUN_STATUS#" + string(status)
}
func assignmentRunGSI2SK(triggeredAt time.Time, id string) string {
	return triggeredAt.UTC().Format(time.RFC3339Nano) + "#" + id
}

func assignmentRunToItem(r domain.AssignmentRun) assignmentRunItem {
	return assignmentRunItem{
		PK:              assignmentRunPK(r.ConferenceID),
		SK:              assignmentRunSK(r.TriggeredAt, r.ID),
		Entity:          "AssignmentRun",
		ID:              r.ID,
		ConferenceID:    r.ConferenceID,
		Seed:            r.Seed,
		RunOrdinal:      r.RunOrdinal,
		IsCanonical:     r.IsCanonical,
		TriggeredBy:     r.TriggeredBy,
		TriggeredAt:     r.TriggeredAt.Format(time.RFC3339Nano),
		CompletedAt:     formatTimeOrEmpty(r.CompletedAt),
		Status:          string(r.Status),
		Objective:       r.Objective,
		AssignmentCount: r.AssignmentCount,
		InputsHash:      r.InputsHash,
		Diagnostics:     r.Diagnostics,
		IsDeleted:       r.IsDeleted,
		Version:         r.Version,
		CreatedAt:       r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:       r.UpdatedAt.Format(time.RFC3339Nano),
		CreatedBy:       r.CreatedBy,
		UpdatedBy:       r.UpdatedBy,
		GSI2PK:          assignmentRunGSI2PK(r.ConferenceID, r.Status),
		GSI2SK:          assignmentRunGSI2SK(r.TriggeredAt, r.ID),
	}
}

func assignmentRunFromItem(it assignmentRunItem) domain.AssignmentRun {
	r := domain.AssignmentRun{
		ID:              it.ID,
		ConferenceID:    it.ConferenceID,
		Seed:            it.Seed,
		RunOrdinal:      it.RunOrdinal,
		IsCanonical:     it.IsCanonical,
		TriggeredBy:     it.TriggeredBy,
		Status:          domain.AssignmentRunStatus(it.Status),
		Objective:       it.Objective,
		AssignmentCount: it.AssignmentCount,
		InputsHash:      it.InputsHash,
		Diagnostics:     it.Diagnostics,
		IsDeleted:       it.IsDeleted,
		Version:         it.Version,
		CreatedBy:       it.CreatedBy,
		UpdatedBy:       it.UpdatedBy,
	}
	if t, err := time.Parse(time.RFC3339Nano, it.TriggeredAt); err == nil {
		r.TriggeredAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CompletedAt); err == nil {
		r.CompletedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		r.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.UpdatedAt); err == nil {
		r.UpdatedAt = t
	}
	return r
}

func prepareNewAssignmentRun(in domain.AssignmentRun) domain.AssignmentRun {
	if in.ID == "" {
		if id, err := uuid.NewV7(); err == nil {
			in.ID = id.String()
		}
	}
	if in.Status == "" {
		in.Status = domain.AssignmentRunStatusRunning
	}
	now := time.Now().UTC()
	if in.TriggeredAt.IsZero() {
		in.TriggeredAt = now
	}
	in.CreatedAt = now
	in.UpdatedAt = now
	in.Version = 1
	return in
}

// CreateAssignmentRun inserts a new AssignmentRun row.
//
// Concurrency: only one run per conference may carry status=running per
// ASSIGNMENT_ALGORITHM.md §9. We enforce this with a two-step
// "query-then-put" — first query GSI2 for any running rows under the
// conference, then PutItem with attribute_not_exists(PK). There is a small
// race window between the GSI2 query and the PutItem during which a second
// trigger could squeeze in; this is acceptable at NUMUN scale because runs
// are manually triggered and infrequent. If the race ever bites, upgrade to
// a sentinel `ASSIGNMENT_RUN_LOCK#<conferenceId>` row written with
// attribute_not_exists(PK) inside the same TransactWriteItems.
func (c *Client) CreateAssignmentRun(ctx context.Context, in domain.AssignmentRun) (domain.AssignmentRun, error) {
	if existing, err := c.FindInFlightRun(ctx, in.ConferenceID); err == nil {
		_ = existing
		return domain.AssignmentRun{}, ErrAlgorithmAlreadyRunning
	} else if !errors.Is(err, ErrNotFound) {
		return domain.AssignmentRun{}, err
	}
	in = prepareNewAssignmentRun(in)
	it := assignmentRunToItem(in)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("marshal assignment run: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.AssignmentRun{}, ErrAlreadyExists
		}
		return domain.AssignmentRun{}, fmt.Errorf("put assignment run: %w", err)
	}
	return assignmentRunFromItem(it), nil
}

// GetAssignmentRun fetches an AssignmentRun by id. v1 uses an id-only Scan
// because the SK encodes the triggeredAt timestamp and isn't reconstructable
// from the id alone.
func (c *Client) GetAssignmentRun(ctx context.Context, id string) (domain.AssignmentRun, error) {
	out, err := c.DDB.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.Table),
		FilterExpression: aws.String("#e = :entity AND id = :id AND #del = :false"),
		ExpressionAttributeNames: map[string]string{
			"#e":   "entity",
			"#del": "isDeleted",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":entity": &ddbtypes.AttributeValueMemberS{Value: "AssignmentRun"},
			":id":     &ddbtypes.AttributeValueMemberS{Value: id},
			":false":  &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("get assignment run: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.AssignmentRun{}, ErrNotFound
	}
	var it assignmentRunItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &it); err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("unmarshal assignment run: %w", err)
	}
	return assignmentRunFromItem(it), nil
}

// FindInFlightRun returns the currently-running AssignmentRun for a
// conference (status=running), or ErrNotFound if none exists. Used both by
// the concurrency check and by the status RPC.
func (c *Client) FindInFlightRun(ctx context.Context, conferenceID string) (domain.AssignmentRun, error) {
	out, err := c.DDB.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		IndexName:              aws.String("GSI2"),
		KeyConditionExpression: aws.String("GSI2PK = :pk"),
		FilterExpression:       aws.String("isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":    &ddbtypes.AttributeValueMemberS{Value: assignmentRunGSI2PK(conferenceID, domain.AssignmentRunStatusRunning)},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		Limit: aws.Int32(1),
	})
	if err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("find in-flight run: %w", err)
	}
	if len(out.Items) == 0 {
		return domain.AssignmentRun{}, ErrNotFound
	}
	var stub assignmentRunItem
	if err := attributevalue.UnmarshalMap(out.Items[0], &stub); err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("unmarshal in-flight run projection: %w", err)
	}
	get, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: stub.PK},
			"SK": &ddbtypes.AttributeValueMemberS{Value: stub.SK},
		},
	})
	if err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("get in-flight run: %w", err)
	}
	if get.Item == nil {
		return domain.AssignmentRun{}, ErrNotFound
	}
	var it assignmentRunItem
	if err := attributevalue.UnmarshalMap(get.Item, &it); err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("unmarshal in-flight run: %w", err)
	}
	if it.IsDeleted {
		return domain.AssignmentRun{}, ErrNotFound
	}
	return assignmentRunFromItem(it), nil
}

// UpdateAssignmentRunStatus transitions a run to done/failed with completion
// stats. Updates the GSI2PK in the same call so the in-flight query stops
// matching this row.
func (c *Client) UpdateAssignmentRunStatus(ctx context.Context, runID string, status domain.AssignmentRunStatus, objective float64, assignmentCount int, diagnostics string, completedAt time.Time) (domain.AssignmentRun, error) {
	current, err := c.GetAssignmentRun(ctx, runID)
	if err != nil {
		return domain.AssignmentRun{}, err
	}
	now := time.Now().UTC()
	current.Status = status
	current.Objective = objective
	current.AssignmentCount = assignmentCount
	current.Diagnostics = diagnostics
	current.CompletedAt = completedAt
	current.UpdatedAt = now
	expectedVersion := current.Version
	current.Version = expectedVersion + 1

	it := assignmentRunToItem(current)
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return domain.AssignmentRun{}, fmt.Errorf("marshal assignment run: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.Table),
		Item:                av,
		ConditionExpression: aws.String("attribute_exists(PK) AND #v = :ev"),
		ExpressionAttributeNames: map[string]string{
			"#v": "version",
		},
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":ev": &ddbtypes.AttributeValueMemberN{Value: strconv.Itoa(expectedVersion)},
		},
	})
	if err != nil {
		var ccf *ddbtypes.ConditionalCheckFailedException
		if errors.As(err, &ccf) {
			return domain.AssignmentRun{}, ErrVersionMismatch
		}
		return domain.AssignmentRun{}, fmt.Errorf("update assignment run status: %w", err)
	}
	return assignmentRunFromItem(it), nil
}

// ListAssignmentRunsByConference returns runs ordered by SK descending (newest
// first) via base-table Query.
func (c *Client) ListAssignmentRunsByConference(ctx context.Context, conferenceID, cursor string, pageSize int32) ([]domain.AssignmentRun, string, error) {
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	in := &dynamodb.QueryInput{
		TableName:              aws.String(c.Table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
		FilterExpression:       aws.String("isDeleted = :false"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":pk":    &ddbtypes.AttributeValueMemberS{Value: assignmentRunPK(conferenceID)},
			":sk":    &ddbtypes.AttributeValueMemberS{Value: "ASSIGNMENT_RUN#"},
			":false": &ddbtypes.AttributeValueMemberBOOL{Value: false},
		},
		ScanIndexForward: aws.Bool(false),
		Limit:            aws.Int32(pageSize),
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
		return nil, "", fmt.Errorf("list assignment runs: %w", err)
	}
	runs := make([]domain.AssignmentRun, 0, len(out.Items))
	for _, raw := range out.Items {
		var it assignmentRunItem
		if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
			return nil, "", fmt.Errorf("unmarshal assignment run: %w", err)
		}
		runs = append(runs, assignmentRunFromItem(it))
	}
	var next string
	if len(out.LastEvaluatedKey) > 0 {
		next, err = encodeCursor(out.LastEvaluatedKey)
		if err != nil {
			return nil, "", fmt.Errorf("encode cursor: %w", err)
		}
	}
	return runs, next, nil
}

// NextRunOrdinal returns max(runOrdinal)+1 across all (incl. soft-deleted) runs
// in a conference. Walks pagination; NUMUN scale keeps this cheap.
func (c *Client) NextRunOrdinal(ctx context.Context, conferenceID string) (int, error) {
	maxOrdinal := 0
	var startKey map[string]ddbtypes.AttributeValue
	for {
		in := &dynamodb.QueryInput{
			TableName:              aws.String(c.Table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":pk": &ddbtypes.AttributeValueMemberS{Value: assignmentRunPK(conferenceID)},
				":sk": &ddbtypes.AttributeValueMemberS{Value: "ASSIGNMENT_RUN#"},
			},
			ExclusiveStartKey: startKey,
		}
		out, err := c.DDB.Query(ctx, in)
		if err != nil {
			return 0, fmt.Errorf("next run ordinal: %w", err)
		}
		for _, raw := range out.Items {
			var it assignmentRunItem
			if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
				return 0, fmt.Errorf("unmarshal assignment run: %w", err)
			}
			if it.RunOrdinal > maxOrdinal {
				maxOrdinal = it.RunOrdinal
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return maxOrdinal + 1, nil
}
