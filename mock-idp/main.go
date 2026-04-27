// Mock IdP de desenvolvimento — emite JWTs RS256 com preferred_username = client_id.
//
// Substitui o ghcr.io/navikt/mock-oauth2-server porque o JSON_CONFIG dele tem
// claims estáticos (sem templating ${client_id}), o que exigiria uma entrada
// por CPF no config. Aqui o claim é dinâmico, sem limite de CPFs.
//
// NÃO USAR EM PRODUÇÃO: não valida client_secret nem rotaciona chave.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const kid = "mock-idp-key-1"

func main() {
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("generate rsa key: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/default/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host + "/default"
		writeJSON(w, map[string]any{
			"issuer":                                base,
			"token_endpoint":                        base + "/token",
			"jwks_uri":                              base + "/jwks",
			"grant_types_supported":                 []string{"client_credentials"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/default/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
			}},
		})
	})

	mux.HandleFunc("/default/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		clientID := r.Form.Get("client_id")
		if clientID == "" {
			http.Error(w, "missing client_id", http.StatusBadRequest)
			return
		}
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss":                "http://" + r.Host + "/default",
			"sub":                clientID,
			"preferred_username": clientID,
			"iat":                now.Unix(),
			"nbf":                now.Unix(),
			"exp":                now.Add(time.Hour).Unix(),
		})
		token.Header["kid"] = kid
		signed, err := token.SignedString(priv)
		if err != nil {
			http.Error(w, "sign error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"access_token": signed,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	log.Printf("mock-idp listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
