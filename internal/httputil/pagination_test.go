package httputil_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielpassy/desafio-prefeitura-rio-backend/internal/httputil"
	"github.com/gin-gonic/gin"
)

func callParsePagination(query string) (limit, offset int, ok bool, status int) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		l, o, ok := httputil.ParsePagination(c)
		if !ok {
			return
		}
		limit, offset = l, o
	})

	req := httptest.NewRequest(http.MethodGet, "/"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return limit, offset, w.Code == http.StatusOK, w.Code
}

func TestParsePagination_Defaults(t *testing.T) {
	limit, offset, ok, _ := callParsePagination("")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if limit != 20 || offset != 0 {
		t.Errorf("got limit=%d offset=%d, want limit=20 offset=0", limit, offset)
	}
}

func TestParsePagination_ExplicitValues(t *testing.T) {
	limit, offset, ok, _ := callParsePagination("?limit=5&offset=10")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if limit != 5 || offset != 10 {
		t.Errorf("got limit=%d offset=%d, want limit=5 offset=10", limit, offset)
	}
}

func TestParsePagination_MaxLimit(t *testing.T) {
	limit, _, ok, _ := callParsePagination("?limit=100")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if limit != 100 {
		t.Errorf("got limit=%d, want 100", limit)
	}
}

func TestParsePagination_LimitZero(t *testing.T) {
	_, _, ok, status := callParsePagination("?limit=0")
	if ok || status != http.StatusBadRequest {
		t.Errorf("expected 400, got ok=%v status=%d", ok, status)
	}
}

func TestParsePagination_LimitAboveMax(t *testing.T) {
	_, _, ok, status := callParsePagination("?limit=101")
	if ok || status != http.StatusBadRequest {
		t.Errorf("expected 400, got ok=%v status=%d", ok, status)
	}
}

func TestParsePagination_LimitNonNumeric(t *testing.T) {
	_, _, ok, status := callParsePagination("?limit=abc")
	if ok || status != http.StatusBadRequest {
		t.Errorf("expected 400, got ok=%v status=%d", ok, status)
	}
}

func TestParsePagination_NegativeOffset(t *testing.T) {
	_, _, ok, status := callParsePagination("?offset=-1")
	if ok || status != http.StatusBadRequest {
		t.Errorf("expected 400, got ok=%v status=%d", ok, status)
	}
}

func TestParsePagination_OffsetNonNumeric(t *testing.T) {
	_, _, ok, status := callParsePagination("?offset=abc")
	if ok || status != http.StatusBadRequest {
		t.Errorf("expected 400, got ok=%v status=%d", ok, status)
	}
}
