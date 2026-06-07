package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// IDTokenClaims holds the subset of fields the AuthService.Exchange handler
// needs out of a Cognito ID token.
type IDTokenClaims struct {
	Sub   string
	Email string
	Role  string // from custom:role; may be empty when not yet set (advisor default)
	Name  string
}

// Verifier validates Cognito-issued ID tokens via JWKS. The JWKS is fetched
// lazily on first use and cached. JWKS URLs are derived from the user pool
// region + id per Cognito convention.
type Verifier struct {
	mu     sync.Mutex
	region string
	pool   string
	keys   jwk.Set
	// In dev-bypass mode the verifier is built without a real pool; methods
	// will refuse to operate.
	enabled bool
}

// NewVerifier wires a Verifier for the given Cognito region + user-pool id.
// When either is empty, the Verifier is disabled and returns an error on use.
func NewVerifier(region, userPoolID string) *Verifier {
	return &Verifier{
		region:  region,
		pool:    userPoolID,
		enabled: region != "" && userPoolID != "",
	}
}

func (v *Verifier) jwksURL() string {
	return fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s/.well-known/jwks.json", v.region, v.pool)
}

func (v *Verifier) issuer() string {
	return fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", v.region, v.pool)
}

func (v *Verifier) keySet(ctx context.Context) (jwk.Set, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.keys != nil {
		return v.keys, nil
	}
	set, err := jwk.Fetch(ctx, v.jwksURL())
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	v.keys = set
	return set, nil
}

// VerifyIDToken parses, signature-checks, and validates a Cognito ID token.
// Returns the canonical subset of claims used by AuthService.Exchange.
func (v *Verifier) VerifyIDToken(ctx context.Context, raw string, expectedAud string) (IDTokenClaims, error) {
	if !v.enabled {
		return IDTokenClaims{}, errors.New("auth: verifier disabled (no user pool configured)")
	}
	keys, err := v.keySet(ctx)
	if err != nil {
		return IDTokenClaims{}, err
	}
	t, err := jwt.Parse([]byte(strings.TrimSpace(raw)),
		jwt.WithKeySet(keys),
		jwt.WithIssuer(v.issuer()),
		jwt.WithAcceptableSkew(time.Minute),
		jwt.WithValidate(true),
	)
	if err != nil {
		return IDTokenClaims{}, fmt.Errorf("parse id token: %w", err)
	}
	// Cognito ID tokens carry `aud` = the app-client id. Verify ourselves
	// rather than via jwt.WithAudience so the diagnostic is clearer when it
	// mismatches.
	if expectedAud != "" {
		if !containsAud(t.Audience(), expectedAud) {
			return IDTokenClaims{}, fmt.Errorf("id token aud mismatch: %v", t.Audience())
		}
	}
	if tu, ok := t.Get("token_use"); ok {
		if s, _ := tu.(string); s != "id" {
			return IDTokenClaims{}, fmt.Errorf("expected token_use=id, got %v", tu)
		}
	}
	out := IDTokenClaims{Sub: t.Subject()}
	if e, ok := t.Get("email"); ok {
		out.Email, _ = e.(string)
	}
	if r, ok := t.Get("custom:role"); ok {
		out.Role, _ = r.(string)
	}
	if n, ok := t.Get("name"); ok {
		out.Name, _ = n.(string)
	}
	return out, nil
}

func containsAud(aud []string, want string) bool {
	for _, a := range aud {
		if a == want {
			return true
		}
	}
	return false
}
