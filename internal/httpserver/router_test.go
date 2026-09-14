package httpserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type failedDB struct{}

func (failedDB) PingContext(context.Context) error { return errors.New("unavailable") }

func TestHealthAndReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		db   Pinger
		path string
		code int
	}{
		{"HTTP only", nil, "/readyz", 200},
		{"database failure", failedDB{}, "/readyz", 503},
		{"liveness independent of database", failedDB{}, "/healthz", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router, err := New(tc.db)
			if err != nil {
				t.Fatal(err)
			}
			router.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", tc.path, nil))
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d", w.Code, tc.code)
			}
		})
	}
}
