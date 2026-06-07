package store

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/numun/numun/api/internal/domain"
)

// sessionItem is the on-the-wire DDB shape for a Session row. Refresh and
// access tokens rely on DDB's table-level encryption at rest (KMS-managed key)
// for v1; application-layer KMS envelope encryption is left for M12 per
// AUTH.md §15 / SECURITY.md §10.
type sessionItem struct {
	PK                         string `dynamodbav:"PK"`
	SK                         string `dynamodbav:"SK"`
	Entity                     string `dynamodbav:"entity"`
	ID                         string `dynamodbav:"id"`
	UserID                     string `dynamodbav:"userId"`
	RefreshToken               string `dynamodbav:"refreshToken"`
	CachedAccessToken          string `dynamodbav:"cachedAccessToken"`
	CachedAccessTokenExpiresAt string `dynamodbav:"cachedAccessTokenExpiresAt"`
	CSRFToken                  string `dynamodbav:"csrfToken"`
	IP                         string `dynamodbav:"ip,omitempty"`
	UserAgent                  string `dynamodbav:"userAgent,omitempty"`
	CreatedAt                  string `dynamodbav:"createdAt"`
	LastUsedAt                 string `dynamodbav:"lastUsedAt"`
	ExpiresAt                  int64  `dynamodbav:"expiresAt"` // epoch seconds for DDB TTL
}

func sessionPK(id string) string { return "SESSION#" + id }

const sessionSK = "META"

func sessionFromItem(it sessionItem) domain.Session {
	s := domain.Session{
		ID:                it.ID,
		UserID:            it.UserID,
		RefreshToken:      it.RefreshToken,
		CachedAccessToken: it.CachedAccessToken,
		CSRFToken:         it.CSRFToken,
		IP:                it.IP,
		UserAgent:         it.UserAgent,
		ExpiresAt:         time.Unix(it.ExpiresAt, 0).UTC(),
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CreatedAt); err == nil {
		s.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.LastUsedAt); err == nil {
		s.LastUsedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, it.CachedAccessTokenExpiresAt); err == nil {
		s.CachedAccessTokenExpiresAt = t
	}
	return s
}

// PutSession inserts (or replaces) a Session row. Sessions are not
// version-locked — TTL handles cleanup and concurrent updates from the same
// browser are not a realistic threat (AUTH.md §13.1).
func (c *Client) PutSession(ctx context.Context, s domain.Session) error {
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.LastUsedAt.IsZero() {
		s.LastUsedAt = s.CreatedAt
	}
	it := sessionItem{
		PK:                         sessionPK(s.ID),
		SK:                         sessionSK,
		Entity:                     "Session",
		ID:                         s.ID,
		UserID:                     s.UserID,
		RefreshToken:               s.RefreshToken,
		CachedAccessToken:          s.CachedAccessToken,
		CachedAccessTokenExpiresAt: s.CachedAccessTokenExpiresAt.Format(time.RFC3339Nano),
		CSRFToken:                  s.CSRFToken,
		IP:                         s.IP,
		UserAgent:                  s.UserAgent,
		CreatedAt:                  s.CreatedAt.Format(time.RFC3339Nano),
		LastUsedAt:                 s.LastUsedAt.Format(time.RFC3339Nano),
		ExpiresAt:                  s.ExpiresAt.Unix(),
	}
	av, err := attributevalue.MarshalMap(it)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	_, err = c.DDB.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.Table),
		Item:      av,
	})
	if err != nil {
		return fmt.Errorf("put session: %w", err)
	}
	return nil
}

// GetSession fetches a Session by id. Returns ErrNotFound when missing or
// expired (DDB's TTL deletion may lag, so we treat past-expiry rows as gone).
func (c *Client) GetSession(ctx context.Context, id string) (domain.Session, error) {
	out, err := c.DDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: sessionPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: sessionSK},
		},
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	if out.Item == nil {
		return domain.Session{}, ErrNotFound
	}
	var it sessionItem
	if err := attributevalue.UnmarshalMap(out.Item, &it); err != nil {
		return domain.Session{}, fmt.Errorf("unmarshal session: %w", err)
	}
	if time.Now().Unix() >= it.ExpiresAt {
		return domain.Session{}, ErrNotFound
	}
	return sessionFromItem(it), nil
}

// TouchSession updates the access-token cache + lastUsedAt in a single call.
// Best-effort — failures do not block the request (AUTH.md §5.1 step 7).
func (c *Client) TouchSession(ctx context.Context, id string, cachedAccessToken string, cachedExpiresAt time.Time) error {
	_, err := c.DDB.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: sessionPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: sessionSK},
		},
		UpdateExpression: aws.String("SET cachedAccessToken = :tok, cachedAccessTokenExpiresAt = :exp, lastUsedAt = :now"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":tok": &ddbtypes.AttributeValueMemberS{Value: cachedAccessToken},
			":exp": &ddbtypes.AttributeValueMemberS{Value: cachedExpiresAt.Format(time.RFC3339Nano)},
			":now": &ddbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339Nano)},
		},
	})
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// DeleteSession removes a session row by id. Idempotent.
func (c *Client) DeleteSession(ctx context.Context, id string) error {
	_, err := c.DDB.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.Table),
		Key: map[string]ddbtypes.AttributeValue{
			"PK": &ddbtypes.AttributeValueMemberS{Value: sessionPK(id)},
			"SK": &ddbtypes.AttributeValueMemberS{Value: sessionSK},
		},
	})
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
