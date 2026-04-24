package ws_test

import (
	"context"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/auth"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/broadcast"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/storage"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/testutil"
	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	testPrivKey *rsa.PrivateKey
	testKf      keyfunc.Keyfunc
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	fixture, err := testutil.NewJWKSFixture()
	if err != nil {
		panic(err)
	}
	testPrivKey = fixture.PrivateKey
	testKf = fixture.Keyfunc

	code := m.Run()
	fixture.Close()
	os.Exit(code)
}

type cancelAwareSubscriber struct {
	subscribed chan struct{}
	cancelled  chan struct{}
}

func newCancelAwareSubscriber() *cancelAwareSubscriber {
	return &cancelAwareSubscriber{
		subscribed: make(chan struct{}, 1),
		cancelled:  make(chan struct{}, 1),
	}
}

func (s *cancelAwareSubscriber) Subscribe(ctx context.Context, _ []byte) <-chan broadcast.Message {
	ch := make(chan broadcast.Message)
	s.subscribed <- struct{}{}

	go func() {
		<-ctx.Done()
		select {
		case s.cancelled <- struct{}{}:
		default:
		}
		close(ch)
	}()

	return ch
}

func (s *cancelAwareSubscriber) waitSubscribed(t *testing.T) {
	t.Helper()
	select {
	case <-s.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for subscription")
	}
}

func (s *cancelAwareSubscriber) waitCancelled(t *testing.T) {
	t.Helper()
	select {
	case <-s.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for context cancellation")
	}
}

func signToken(t *testing.T, cpf string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"preferred_username": cpf,
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(testPrivKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func newTestServer(t *testing.T, sub ws.Subscriber) *httptest.Server {
	t.Helper()
	r := gin.New()
	authed := r.Group("/", auth.AuthMiddleware(testKf, []byte("test-cpf-key")))
	authed.GET("/ws", ws.NewHandler(sub).Handle)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func citizenRefForCPF(cpf string) []byte {
	mac := hmac.New(sha256.New, []byte("test-cpf-key"))
	mac.Write([]byte(cpf))
	return mac.Sum(nil)
}

func publishNotification(t *testing.T, pub *broadcast.RedisPublisher, citizenRef []byte, msg broadcast.Message) {
	t.Helper()
	n := &storage.Notification{
		ID:             uuid.MustParse(msg.ID),
		TicketID:       msg.TicketID,
		Type:           msg.Type,
		CitizenRef:     citizenRef,
		PreviousStatus: msg.PreviousStatus,
		NewStatus:      msg.NewStatus,
		Title:          msg.Title,
		Description:    msg.Description,
		EventTimestamp: msg.EventTimestamp,
		ReceivedAt:     msg.ReceivedAt,
	}
	if err := pub.Publish(context.Background(), n); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestWS_RejectsUnauthenticated(t *testing.T) {
	srv := newTestServer(t, ws.NoOpSubscriber{})

	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv), nil)
	if err == nil {
		t.Fatal("expected dial to fail, got nil error")
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestWS_ReceivesMessage(t *testing.T) {
	rdb := testutil.NewTestRedis(t)
	sub := broadcast.NewRedisSubscriber(rdb)
	pub := broadcast.NewRedisPublisher(rdb)
	srv := newTestServer(t, sub)

	cpf := "12345678901"
	header := http.Header{"Authorization": {"Bearer " + signToken(t, cpf)}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	want := broadcast.Message{
		ID:             "11111111-1111-1111-1111-111111111111",
		TicketID:       "T-1",
		Type:           "status_change",
		PreviousStatus: "open",
		NewStatus:      "in_progress",
		Title:          "Status atualizado",
		EventTimestamp: time.Now().UTC().Truncate(time.Millisecond),
		ReceivedAt:     time.Now().UTC().Truncate(time.Millisecond),
	}
	publishNotification(t, pub, citizenRefForCPF(cpf), want)

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got broadcast.Message
	if err := conn.ReadJSON(&got); err != nil {
		t.Fatalf("read json: %v", err)
	}

	if got.ID != want.ID || got.TicketID != want.TicketID || got.Title != want.Title {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestWS_MultipleConnections(t *testing.T) {
	rdb := testutil.NewTestRedis(t)
	sub := broadcast.NewRedisSubscriber(rdb)
	pub := broadcast.NewRedisPublisher(rdb)
	srv := newTestServer(t, sub)

	cpf := "12345678901"
	header := http.Header{"Authorization": {"Bearer " + signToken(t, cpf)}}

	conn1, _, err := websocket.DefaultDialer.Dial(wsURL(srv), header)
	if err != nil {
		t.Fatalf("dial conn1: %v", err)
	}
	defer conn1.Close()

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL(srv), header)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.Close()

	// wait for both connections to be registered before publishing
	time.Sleep(50 * time.Millisecond)

	msg := broadcast.Message{
		ID:             "22222222-2222-2222-2222-222222222222",
		TicketID:       "T-2",
		Type:           "status_change",
		PreviousStatus: "open",
		NewStatus:      "completed",
		Title:          "Novo status",
		EventTimestamp: time.Now().UTC().Truncate(time.Millisecond),
		ReceivedAt:     time.Now().UTC().Truncate(time.Millisecond),
	}
	publishNotification(t, pub, citizenRefForCPF(cpf), msg)

	deadline := time.Now().Add(2 * time.Second)
	conn1.SetReadDeadline(deadline)
	conn2.SetReadDeadline(deadline)

	var got1, got2 broadcast.Message
	if err := conn1.ReadJSON(&got1); err != nil {
		t.Fatalf("read conn1: %v", err)
	}
	if err := conn2.ReadJSON(&got2); err != nil {
		t.Fatalf("read conn2: %v", err)
	}

	if got1.ID != msg.ID {
		t.Errorf("conn1 got %q, want %q", got1.ID, msg.ID)
	}
	if got2.ID != msg.ID {
		t.Errorf("conn2 got %q, want %q", got2.ID, msg.ID)
	}
}

func TestWS_ClientDisconnectCancelsSubscriptionContext(t *testing.T) {
	sub := newCancelAwareSubscriber()
	srv := newTestServer(t, sub)

	header := http.Header{"Authorization": {"Bearer " + signToken(t, "12345678901")}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv), header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	sub.waitSubscribed(t)

	if err := conn.Close(); err != nil {
		t.Fatalf("close client connection: %v", err)
	}

	sub.waitCancelled(t)
}
