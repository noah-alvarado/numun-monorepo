package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func TestVerifyIDToken(t *testing.T) {
	signer, public := testKey(t, "k1")
	other, _ := testKey(t, "k1") // same kid, different key: a forgery
	set := jwk.NewSet()
	_ = set.AddKey(public)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pool/.well-known/jwks.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)
	issuer := srv.URL + "/pool"
	v := NewVerifier("", "").WithIssuer(issuer)

	token := func(key jwk.Key, mutate func(jwt.Token)) string {
		tok := jwt.New()
		_ = tok.Set(jwt.IssuerKey, issuer)
		_ = tok.Set(jwt.AudienceKey, "client")
		_ = tok.Set(jwt.SubjectKey, "user-1")
		_ = tok.Set(jwt.ExpirationKey, time.Now().Add(time.Hour))
		_ = tok.Set("token_use", "id")
		_ = tok.Set("email", "a@example.org")
		_ = tok.Set("custom:role", "staff-admin")
		if mutate != nil {
			mutate(tok)
		}
		raw, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, key))
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return string(raw)
	}

	got, err := v.VerifyIDToken(context.Background(), token(signer, nil), "client")
	if err != nil {
		t.Fatalf("valid token: %v", err)
	}
	if got.Sub != "user-1" || got.Email != "a@example.org" || got.Role != "staff-admin" {
		t.Fatalf("claims = %+v", got)
	}

	for name, raw := range map[string]string{
		"forged signature": token(other, nil),
		"expired":          token(signer, func(tok jwt.Token) { _ = tok.Set(jwt.ExpirationKey, time.Now().Add(-time.Hour)) }),
		"wrong audience":   token(signer, func(tok jwt.Token) { _ = tok.Set(jwt.AudienceKey, "someone-else") }),
		"wrong issuer":     token(signer, func(tok jwt.Token) { _ = tok.Set(jwt.IssuerKey, "https://evil.example") }),
		"access token":     token(signer, func(tok jwt.Token) { _ = tok.Set("token_use", "access") }),
	} {
		if _, err := v.VerifyIDToken(context.Background(), raw, "client"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func testKey(t *testing.T, kid string) (private, public jwk.Key) {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	private, err = jwk.FromRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	_ = private.Set(jwk.KeyIDKey, kid)
	_ = private.Set(jwk.AlgorithmKey, jwa.RS256)
	public, err = private.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	return private, public
}
