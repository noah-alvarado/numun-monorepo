package email

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UnsubscribeToken is the HMAC-signed payload referenced by EMAIL.md §2.2.
// Stateless: hitting the URL flips announcementsOptIn = false; no server-side
// state needs to exist before the click.
type UnsubscribeToken struct {
	UserID    string `json:"u"`
	Kind      string `json:"k"` // "announcements" in v1
	IssuedAt  int64  `json:"t"`
}

// SignedUnsubscribeURL builds a URL whose query carries a base64-encoded
// {payload}.{hmac} token. Returns an error when the signing secret is empty —
// callers in transactional paths swallow that error (no header gets added).
func SignedUnsubscribeURL(baseURL, secret, userID string) (string, error) {
	if secret == "" {
		return "", errors.New("unsubscribe: signing secret unset")
	}
	if userID == "" {
		return "", errors.New("unsubscribe: empty userId")
	}
	payload := UnsubscribeToken{
		UserID:   userID,
		Kind:     "announcements",
		IssuedAt: time.Now().Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encPayload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encPayload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	sep := "?"
	if strings.Contains(baseURL, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%stoken=%s.%s", baseURL, sep, encPayload, sig), nil
}

// VerifyUnsubscribeToken checks signature and returns the decoded payload.
// Used by the HTTP handler exposed at /v1/email/unsubscribe.
func VerifyUnsubscribeToken(secret, token string) (UnsubscribeToken, error) {
	var out UnsubscribeToken
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return out, errors.New("unsubscribe: malformed token")
	}
	encPayload, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encPayload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return out, errors.New("unsubscribe: signature mismatch")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encPayload)
	if err != nil {
		return out, fmt.Errorf("unsubscribe: bad payload: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("unsubscribe: bad json: %w", err)
	}
	return out, nil
}
