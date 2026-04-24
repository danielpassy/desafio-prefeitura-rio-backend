package webhook_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/api"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/webhook"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	testSecret = "test-webhook-secret"
	testCPFKey = "test-cpf-key"
	testCPF    = "12345678901"
)

var (
	testDB *pgxpool.Pool
	testKf keyfunc.Keyfunc
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	fixture, err := testutil.NewJWKSFixture()
	if err != nil {
		log.Fatalf("jwks fixture: %v", err)
	}
	testKf = fixture.Keyfunc

	pool, err := testutil.NewTestDB("../../migrations")
	if err != nil {
		log.Fatalf("webhook tests require postgres: %v", err)
	}
	testDB = pool
	code := m.Run()
	fixture.Close()
	pool.Close()
	os.Exit(code)
}

func testRouter(t *testing.T) (*gin.Engine, *storage.NotificationRepo, func()) {
	t.Helper()
	tx, err := testDB.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	cleanup := func() { tx.Rollback(context.Background()) }

	repo := storage.NewNotificationRepo(tx)
	r := api.NewRouter(api.RouterParams{
		Keyfunc:       testKf,
		Notifications: repo,
		Publisher:     webhook.NoOpPublisher{},
		WebhookSecret: testSecret,
		CPFKey:        testCPFKey,
	})
	return r, repo, cleanup
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func validBody() map[string]any {
	return map[string]any{
		"ticket_id":       "CH-2024-001234",
		"type":            "status_change",
		"cpf":             testCPF,
		"previous_status": "open",
		"new_status":      "in_progress",
		"title":           "Buraco na Rua",
		"timestamp":       "2024-11-15T14:30:00Z",
	}
}

func post(r *gin.Engine, body []byte, sig string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Signature-256", sig)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestWebhookHandler_ValidRequest(t *testing.T) {
	r, repo, cleanup := testRouter(t)
	defer cleanup()

	body, _ := json.Marshal(validBody())
	w := post(r, body, sign(body, testSecret))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	items, total, err := repo.List(context.Background(), storage.ListParams{
		CitizenRef: computeExpectedCitizenRef(testCPF),
		Limit:      1,
	})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 notification, got %d", total)
	}

	n := items[0]
	if n.TicketID != "CH-2024-001234" {
		t.Errorf("ticket_id = %q, want CH-2024-001234", n.TicketID)
	}
	if n.Type != "status_change" {
		t.Errorf("type = %q, want status_change", n.Type)
	}
	if n.PreviousStatus != "open" {
		t.Errorf("previous_status = %q, want open", n.PreviousStatus)
	}
	if n.NewStatus != "in_progress" {
		t.Errorf("new_status = %q, want in_progress", n.NewStatus)
	}
	if n.Title != "Buraco na Rua" {
		t.Errorf("title = %q, want Buraco na Rua", n.Title)
	}
	if n.Read {
		t.Error("read should be false on creation")
	}
	if n.ReadAt != nil {
		t.Error("read_at should be nil on creation")
	}

	expectedHash := sha256.Sum256(body)
	if !bytes.Equal(n.EventHash, expectedHash[:]) {
		t.Error("event_hash does not match sha256(body)")
	}
}

func TestWebhookHandler_MissingSignature(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	body, _ := json.Marshal(validBody())
	w := post(r, body, "")

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	body, _ := json.Marshal(validBody())
	w := post(r, body, sign(body, "wrong-secret"))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandler_MalformedJSON(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	body := []byte(`{not valid json}`)
	w := post(r, body, sign(body, testSecret))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookHandler_InvalidType(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	m := validBody()
	m["type"] = "other_type"
	body, _ := json.Marshal(m)
	w := post(r, body, sign(body, testSecret))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookHandler_InvalidCPF(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	m := validBody()
	m["cpf"] = "123"
	body, _ := json.Marshal(m)
	w := post(r, body, sign(body, testSecret))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookHandler_InvalidTimestamp(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	m := validBody()
	m["timestamp"] = "not-a-date"
	body, _ := json.Marshal(m)
	w := post(r, body, sign(body, testSecret))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookHandler_InvalidStatus(t *testing.T) {
	r, _, cleanup := testRouter(t)
	defer cleanup()

	m := validBody()
	m["new_status"] = "em_execucao"
	body, _ := json.Marshal(m)
	w := post(r, body, sign(body, testSecret))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestWebhookHandler_Idempotency(t *testing.T) {
	r, repo, cleanup := testRouter(t)
	defer cleanup()

	body, _ := json.Marshal(validBody())

	// first request — inserts
	w1 := post(r, body, sign(body, testSecret))
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w1.Code)
	}

	// second identical request — must be silently accepted (no duplicate record)
	w2 := post(r, body, sign(body, testSecret))
	if w2.Code != http.StatusOK {
		t.Errorf("duplicate request status = %d, want 200", w2.Code)
	}

	items, total, err := repo.List(context.Background(), storage.ListParams{
		CitizenRef: computeExpectedCitizenRef(testCPF),
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("duplicate webhook inserted %d rows, want 1", total)
	}
}

func TestWebhookHandler_CitizenRefNotCPF(t *testing.T) {
	r, repo, cleanup := testRouter(t)
	defer cleanup()

	body, _ := json.Marshal(validBody())
	w := post(r, body, sign(body, testSecret))
	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", w.Code)
	}

	// Query via the same transaction so we see the uncommitted row.
	expectedRef := computeExpectedCitizenRef(testCPF)
	items, _, err := repo.List(context.Background(), storage.ListParams{
		CitizenRef: expectedRef,
		Limit:      1,
	})
	if err != nil || len(items) == 0 {
		t.Fatalf("expected one notification; err=%v items=%d", err, len(items))
	}

	citizenRef := items[0].CitizenRef
	if bytes.Equal(citizenRef, []byte(testCPF)) {
		t.Error("citizen_ref must not equal the raw CPF")
	}
	if len(citizenRef) != sha256.Size {
		t.Errorf("citizen_ref length = %d, want %d (HMAC-SHA256)", len(citizenRef), sha256.Size)
	}
}

// computeExpectedCitizenRef mirrors the handler's derivation so the test can query by it.
func computeExpectedCitizenRef(cpf string) []byte {
	mac := hmac.New(sha256.New, []byte(testCPFKey))
	mac.Write([]byte(cpf))
	return mac.Sum(nil)
}
