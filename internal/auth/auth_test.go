package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func encodeBase64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

var (
	testPrivKey *rsa.PrivateKey
	testKf      keyfunc.Keyfunc
)

func TestMain(m *testing.M) {
	var err error
	testPrivKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}

	// serve a minimal JWKS with the test public key
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := testPrivKey.PublicKey.N.Bytes()
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
			N:   encodeBase64URL(n),
			E:   encodeBase64URL(e),
		}}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))

	ctx := context.Background()
	kf, err := auth.NewJWKSKeyfunc(ctx, jwksServer.URL)
	if err != nil {
		panic(err)
	}
	testKf = kf

	m.Run()
	jwksServer.Close()
}

func signToken(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(testPrivKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func validClaims(cpf string) jwt.MapClaims {
	return jwt.MapClaims{
		"preferred_username": cpf,
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
}

func makeRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(auth.AuthMiddleware(testKf))
	r.GET("/test", func(c *gin.Context) {
		cpf, _ := auth.CPFFromContext(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"cpf": cpf})
	})
	return r
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	token := signToken(t, validClaims("12345678901"))
	r := makeRouter()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	r := makeRouter()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_MalformedToken(t *testing.T) {
	r := makeRouter()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer not.a.token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	claims := jwt.MapClaims{
		"preferred_username": "12345678901",
		"exp":                time.Now().Add(-time.Hour).Unix(),
	}
	token := signToken(t, claims)

	r := makeRouter()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_MissingPreferredUsername(t *testing.T) {
	claims := jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	token := signToken(t, claims)

	r := makeRouter()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMiddleware_WrongAlgorithm(t *testing.T) {
	// sign with HMAC instead of RS256
	claims := validClaims("12345678901")
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte("secret"))

	r := makeRouter()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestCPFFromContext(t *testing.T) {
	token := signToken(t, validClaims("12345678901"))
	r := makeRouter()

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["cpf"] != "12345678901" {
		t.Errorf("cpf = %q, want %q", body["cpf"], "12345678901")
	}
}
