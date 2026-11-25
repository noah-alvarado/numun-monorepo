package cms

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
)

// NewDev returns a real Client aimed at a fake GitHub (the e2e suite's
// fakes server), so award syncs exercise the same HTTP path as in prod.
// The fake doesn't verify the app JWT, so a throwaway key signs it.
func NewDev(apiBaseURL string, logger *slog.Logger) (*Client, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return New(Config{
		AppID:          "e2e",
		InstallationID: "1",
		PrivateKeyPEM:  string(keyPEM),
		Repo:           "numun/e2e-content",
		APIBaseURL:     apiBaseURL,
	}, logger)
}
