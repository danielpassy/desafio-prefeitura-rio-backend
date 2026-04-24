package auth_test

import (
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

var (
	testPrivKey *rsa.PrivateKey
	testKf      keyfunc.Keyfunc
)

func TestMain(m *testing.M) {
	fixture, err := testutil.NewJWKSFixture()
	if err != nil {
		panic(err)
	}
	testPrivKey = fixture.PrivateKey
	testKf = fixture.Keyfunc

	m.Run()
	fixture.Close()
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
