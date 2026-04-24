package testutil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
)

// JWKSFixture holds a test RSA key and a local JWKS server backed by it.
// Call Close when the test suite is done.
type JWKSFixture struct {
	PrivateKey *rsa.PrivateKey
	Keyfunc    keyfunc.Keyfunc
	server     *httptest.Server
}

func (f *JWKSFixture) Close() { f.server.Close() }

// NewJWKSFixture generates an RSA-2048 key pair, starts an in-process JWKS
// server that advertises the public key, and returns a ready-to-use keyfunc.
func NewJWKSFixture() (*JWKSFixture, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := key.PublicKey.N.Bytes()
		e := []byte{0x01, 0x00, 0x01} // 65537
		type jwkKey struct {
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		type jwks struct {
			Keys []jwkKey `json:"keys"`
		}
		body, _ := json.Marshal(jwks{Keys: []jwkKey{{
			Kty: "RSA",
			Alg: "RS256",
			Use: "sig",
			N:   base64.RawURLEncoding.EncodeToString(n),
			E:   base64.RawURLEncoding.EncodeToString(e),
		}}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	kf, err := auth.NewJWKSKeyfunc(context.Background(), srv.URL)
	if err != nil {
		srv.Close()
		return nil, err
	}

	return &JWKSFixture{PrivateKey: key, Keyfunc: kf, server: srv}, nil
}
