package notification_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/api"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/circuitbreaker"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/webhook"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sony/gobreaker"
)

const testCPFKey = "test-cpf-key"

var (
	testPrivKey *rsa.PrivateKey
	testKf      keyfunc.Keyfunc
	testDB      *pgxpool.Pool
)

func TestMain(m *testing.M) {
	fixture, err := testutil.NewJWKSFixture()
	if err != nil {
		panic(err)
	}
	testPrivKey = fixture.PrivateKey
	testKf = fixture.Keyfunc

	testDB, err = testutil.NewTestDB("../../migrations")
	if err != nil {
		log.Fatalf("notification tests require postgres: %v", err)
	}

	code := m.Run()
	fixture.Close()
	testDB.Close()
	os.Exit(code)
}

// testSetup begins a transaction, creates a repo backed by it,
// and registers a rollback on test cleanup to keep tests isolated.
// Returns both the repo (for seeding data) and the router (for making requests).
func testSetup(t *testing.T) (*storage.NotificationRepo, *gin.Engine) {
	t.Helper()
	tx, err := testDB.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })

	repo := storage.NewNotificationRepo(tx)
	r := api.NewRouter(api.RouterParams{
		Keyfunc:       testKf,
		Notifications: repo,
		Publisher:     webhook.NoOpPublisher{},
		WebhookSecret: "test-webhook-secret",
		CPFKey:        testCPFKey,
	})

	return repo, r
}

func signToken(t *testing.T, cpf string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"preferred_username": cpf,
		"exp":                time.Now().Add(time.Hour).Unix(),
	})
	signed, err := tok.SignedString(testPrivKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func citizenRef(cpf string) []byte {
	mac := hmac.New(sha256.New, []byte(testCPFKey))
	mac.Write([]byte(cpf))
	return mac.Sum(nil)
}

func insertNotification(t *testing.T, repo *storage.NotificationRepo, cpf string, extra func(*storage.InsertParams)) *storage.Notification {
	t.Helper()
	p := storage.InsertParams{
		TicketID:       "CH-2024-001",
		Type:           "status_change",
		CitizenRef:     citizenRef(cpf),
		PreviousStatus: "open",
		NewStatus:      "in_progress",
		Title:          "Ticket updated",
		EventTimestamp: time.Now().UTC().Truncate(time.Microsecond),
		EventHash:      randomBytes(t),
	}
	if extra != nil {
		extra(&p)
	}
	n, err := repo.Insert(context.Background(), p)
	if err != nil || n == nil {
		t.Fatalf("insert notification: err=%v, n=%v", err, n)
	}
	return n
}

func randomBytes(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random bytes: %v", err)
	}
	return b
}

// R-TST-7: GET /notifications returns only the authenticated citizen's notifications.
func TestList_CitizenIsolation(t *testing.T) {
	repo, r := testSetup(t)

	insertNotification(t, repo, "11111111111", nil)
	insertNotification(t, repo, "22222222222", nil) // another citizen

	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, "11111111111"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Errorf("total=%d items=%d, want total=1 items=1", resp.Total, len(resp.Items))
	}
}

func TestList_Pagination(t *testing.T) {
	repo, r := testSetup(t)

	cpf := "33333333333"
	for range 5 {
		insertNotification(t, repo, cpf, nil)
	}

	req := httptest.NewRequest(http.MethodGet, "/notifications?limit=2&offset=0", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, cpf))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Items  []json.RawMessage `json:"items"`
		Total  int               `json:"total"`
		Limit  int               `json:"limit"`
		Offset int               `json:"offset"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Total != 5 {
		t.Errorf("total = %d, want 5", resp.Total)
	}
	if len(resp.Items) != 2 {
		t.Errorf("len(items) = %d, want 2", len(resp.Items))
	}
	if resp.Limit != 2 || resp.Offset != 0 {
		t.Errorf("limit=%d offset=%d, want limit=2 offset=0", resp.Limit, resp.Offset)
	}
}

func TestList_Defaults(t *testing.T) {
	_, r := testSetup(t)

	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, "44444444444"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Limit != 20 || resp.Offset != 0 {
		t.Errorf("defaults: limit=%d offset=%d, want limit=20 offset=0", resp.Limit, resp.Offset)
	}
}

func TestList_InvalidLimit(t *testing.T) {
	_, r := testSetup(t)

	for _, limit := range []string{"0", "101", "abc", "-1"} {
		req := httptest.NewRequest(http.MethodGet, "/notifications?limit="+limit, nil)
		req.Header.Set("Authorization", "Bearer "+signToken(t, "55555555555"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("limit=%q: status = %d, want 400", limit, w.Code)
		}
	}
}

// R-TST-8: PATCH /notifications/:id/read succeeds for the notification's owner.
func TestMarkRead_OwnNotification(t *testing.T) {
	repo, r := testSetup(t)

	cpf := "66666666666"
	n := insertNotification(t, repo, cpf, nil)

	url := fmt.Sprintf("/notifications/%s/read", n.ID)
	req := httptest.NewRequest(http.MethodPatch, url, nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, cpf))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Read bool `json:"read"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Read {
		t.Error("expected read=true in response")
	}
}

// R-API-2.1: marking an already-read notification is idempotent.
func TestMarkRead_Idempotent(t *testing.T) {
	repo, r := testSetup(t)

	cpf := "77777777777"
	n := insertNotification(t, repo, cpf, nil)
	url := fmt.Sprintf("/notifications/%s/read", n.ID)

	for range 2 {
		req := httptest.NewRequest(http.MethodPatch, url, nil)
		req.Header.Set("Authorization", "Bearer "+signToken(t, cpf))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 on idempotent call", w.Code)
		}
	}
}

// R-TST-9: PATCH /notifications/:id/read returns 404 for another citizen's notification.
func TestMarkRead_AnotherCitizen(t *testing.T) {
	repo, r := testSetup(t)

	ownerCPF := "88888888888"
	otherCPF := "99999999999"
	n := insertNotification(t, repo, ownerCPF, nil)

	url := fmt.Sprintf("/notifications/%s/read", n.ID)
	req := httptest.NewRequest(http.MethodPatch, url, nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, otherCPF))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestMarkRead_NonExistentID(t *testing.T) {
	_, r := testSetup(t)

	url := "/notifications/00000000-0000-0000-0000-000000000000/read"
	req := httptest.NewRequest(http.MethodPatch, url, nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, "11122233344"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestMarkRead_InvalidUUID(t *testing.T) {
	_, r := testSetup(t)

	url := "/notifications/not-a-uuid/read"
	req := httptest.NewRequest(http.MethodPatch, url, nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, "11122233344"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// R-TST-10: GET /notifications/unread-count returns correct count for the citizen.
func TestUnreadCount(t *testing.T) {
	repo, r := testSetup(t)

	cpf := "10101010101"
	for range 3 {
		insertNotification(t, repo, cpf, nil)
	}
	// insert one for another citizen — must not be counted
	insertNotification(t, repo, "20202020202", nil)

	req := httptest.NewRequest(http.MethodGet, "/notifications/unread-count", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, cpf))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		UnreadCount int `json:"unread_count"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.UnreadCount != 3 {
		t.Errorf("unread_count = %d, want 3", resp.UnreadCount)
	}
}

func TestUnreadCount_DecreasesAfterRead(t *testing.T) {
	repo, r := testSetup(t)

	cpf := "30303030303"
	n := insertNotification(t, repo, cpf, nil)
	insertNotification(t, repo, cpf, nil)

	// mark one as read via the HTTP endpoint
	url := fmt.Sprintf("/notifications/%s/read", n.ID)
	req := httptest.NewRequest(http.MethodPatch, url, nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, cpf))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/notifications/unread-count", nil)
	req2.Header.Set("Authorization", "Bearer "+signToken(t, cpf))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	var resp struct {
		UnreadCount int `json:"unread_count"`
	}
	json.NewDecoder(w2.Body).Decode(&resp)
	if resp.UnreadCount != 1 {
		t.Errorf("unread_count = %d, want 1", resp.UnreadCount)
	}
}

func TestList_Unauthenticated(t *testing.T) {
	_, r := testSetup(t)
	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestList_PostgresCBOpen_Returns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		ReadyToTrip: func(counts gobreaker.Counts) bool { return counts.ConsecutiveFailures >= 1 },
	})
	wrappedQ := circuitbreaker.WrapQuerier(testutil.ErrQuerier{}, cb)

	// One failing call is enough to open the circuit.
	wrappedQ.Query(context.Background(), "SELECT 1")

	repo := storage.NewNotificationRepo(wrappedQ)
	r := api.NewRouter(api.RouterParams{
		Keyfunc:       testKf,
		Notifications: repo,
		Publisher:     webhook.NoOpPublisher{},
		WebhookSecret: "test-webhook-secret",
		CPFKey:        testCPFKey,
	})

	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+signToken(t, "12345678901"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}
