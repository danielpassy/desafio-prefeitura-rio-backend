package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/api"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/webhook"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	pool, err := testutil.NewTestDB("../../migrations")
	if err != nil {
		log.Fatalf("router tests require postgres: %v", err)
	}
	testDB = pool

	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func testRouter(t *testing.T) *gin.Engine {
	t.Helper()

	fixture, err := testutil.NewJWKSFixture()
	if err != nil {
		t.Fatalf("jwks fixture: %v", err)
	}
	t.Cleanup(fixture.Close)

	tx, err := testDB.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })

	repo := storage.NewNotificationRepo(tx)

	return api.NewRouter(api.RouterParams{
		Keyfunc:       fixture.Keyfunc,
		Notifications: repo,
		Publisher:     webhook.NoOpPublisher{},
		WebhookSecret: "test-webhook-secret",
		CPFKey:        "test-cpf-key",
	})
}

func hasRoute(r *gin.Engine, method, path string) bool {
	for _, route := range r.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func TestRouter_RequiresAuthOnNotifications(t *testing.T) {
	r := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRouter_RegistersUnreadCountRoute(t *testing.T) {
	r := testRouter(t)

	if !hasRoute(r, http.MethodGet, "/notifications/unread-count") {
		t.Fatal("missing GET /notifications/unread-count route")
	}
}

func TestRouter_RegistersMarkReadAsPatchOnly(t *testing.T) {
	r := testRouter(t)

	if !hasRoute(r, http.MethodPatch, "/notifications/:id/read") {
		t.Fatal("missing PATCH /notifications/:id/read route")
	}
}

func TestRouter_LeavesWebhookUnauthenticated(t *testing.T) {
	r := testRouter(t)

	body := []byte(``)
	mac := hmac.New(sha256.New, []byte("test-webhook-secret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	req.Header.Set("X-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
