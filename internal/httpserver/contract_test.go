package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/problem"
)

type verifierStub struct {
	calls int
	err   error
}

func (v *verifierStub) Verify(context.Context, string) (auth.Identity, error) {
	v.calls++
	return auth.Identity{Issuer: "test", Subject: "test"}, v.err
}

type backendStub struct {
	Backend
	calls int
	err   error
}

func (b *backendStub) Bootstrap(context.Context, auth.Identity, identity.BootstrapInput) (identity.Context, bool, error) {
	b.calls++
	return identity.Context{}, true, b.err
}

func (b *backendStub) Current(context.Context, auth.Identity) (identity.Context, error) {
	b.calls++
	return identity.Context{}, b.err
}

func request(t *testing.T, r http.Handler, method, path, version, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if version != "" {
		req.Header.Set(VersionHeader, version)
	}
	req.Header.Set("Authorization", "Bearer fake")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func checkProblem(t *testing.T, w *httptest.ResponseRecorder, code int, kind problem.Kind) map[string]any {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("content type=%s", w.Header().Get("Content-Type"))
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != "https://ledger.ztd.me/errors/"+string(kind) || body["status"] != float64(code) {
		t.Fatalf("problem=%v", body)
	}
	instance, isString := body["instance"].(string)
	if body["title"] == "" || body["detail"] == "" || !isString || !strings.HasPrefix(instance, "urn:uuid:") {
		t.Fatalf("incomplete problem=%v", body)
	}
	if _, nested := body["error"]; nested {
		t.Fatal("nested error envelope")
	}
	return body
}

func TestVersionGatePrecedesAuthenticationAndWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	v, b := &verifierStub{}, &backendStub{}
	r, err := New(nil, Dependencies{Backend: b, Verifier: v})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		versions   []string
		code       int
		kind       problem.Kind
	}{
		{"missing", "/api/v1/bootstrap", nil, 400, problem.VersionRequired},
		{"empty", "/api/v1/bootstrap", []string{""}, 400, problem.VersionInvalid},
		{"invalid date", "/api/v1/bootstrap", []string{"2026-02-30"}, 400, problem.VersionInvalid},
		{"duplicate", "/api/v1/bootstrap", []string{"2026-09-14", "2026-09-14"}, 400, problem.VersionInvalid},
		{"comma", "/api/v1/bootstrap", []string{"2026-09-14,2026-09-14"}, 400, problem.VersionInvalid},
		{"old", "/api/v1/bootstrap", []string{"2026-09-13"}, 400, problem.VersionUnsupported},
		{"future", "/api/v1/bootstrap", []string{"2026-09-15"}, 400, problem.VersionUnsupported},
		{"major", "/api/v2/bootstrap", []string{"2026-09-14"}, 404, problem.MajorUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), "POST", tc.path, nil)
			for _, version := range tc.versions {
				req.Header.Add(VersionHeader, version)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			body := checkProblem(t, w, tc.code, tc.kind)
			if tc.kind == problem.VersionUnsupported {
				if versions, ok := body["supported_versions"].([]any); !ok || len(versions) != 1 || versions[0] != "2026-09-14" {
					t.Fatalf("versions=%v", body)
				}
			}
			if w.Header().Get(VersionHeader) != "" {
				t.Fatal("rejected version was accepted")
			}
		})
	}
	if v.calls != 0 || b.calls != 0 {
		t.Fatal("version failure executed auth or business code")
	}
	w := request(t, r, "POST", "/api/v1/bootstrap", "2026-09-14", "{}")
	if w.Code != 201 || v.calls != 1 || b.calls != 1 || w.Header().Get(VersionHeader) != "2026-09-14" {
		t.Fatalf("status=%d calls=%d/%d", w.Code, v.calls, b.calls)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
}

func TestProblemCoverageAndHeaders(t *testing.T) {
	v, b := &verifierStub{}, &backendStub{}
	r, err := New(failedDB{}, Dependencies{Backend: b, Verifier: v})
	if err != nil {
		t.Fatal(err)
	}
	checkProblem(t, request(t, r, "GET", "/readyz", "", ""), 503, problem.Unavailable)
	checkProblem(t, request(t, r, "GET", "/absent", "", ""), 404, problem.NotFound)
	w := request(t, r, "DELETE", "/api/v1/books/00000000-0000-0000-0000-000000000000", "2026-09-14", "")
	checkProblem(t, w, 405, problem.MethodNotAllowed)
	if w.Header().Get("Allow") != "GET" {
		t.Fatalf("Allow=%s", w.Header().Get("Allow"))
	}
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/v1/me", nil)
	req.Header.Set(VersionHeader, "2026-09-14")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	checkProblem(t, w, 401, problem.AuthenticationRequired)
	if w.Header().Get("WWW-Authenticate") != "Bearer" || w.Header().Get(VersionHeader) == "" {
		t.Fatal("missing authentication/version headers")
	}
	v.err = problem.New(problem.InvalidToken, "The session token is invalid.")
	checkProblem(t, request(t, r, "GET", "/api/v1/me", "2026-09-14", ""), 401, problem.InvalidToken)
	v.err = problem.New(problem.Unavailable, "Keys unavailable.")
	checkProblem(t, request(t, r, "GET", "/api/v1/me", "2026-09-14", ""), 503, problem.Unavailable)
	v.err = nil
	b.err = problem.New(problem.BootstrapRequired, "Initialize your personal space first.")
	checkProblem(t, request(t, r, "GET", "/api/v1/me", "2026-09-14", ""), 409, problem.BootstrapRequired)
	b.err = errors.New("DATABASE_URL secret=do-not-disclose")
	w = request(t, r, "POST", "/api/v1/bootstrap", "2026-09-14", "{}")
	checkProblem(t, w, 500, problem.Internal)
	if strings.Contains(w.Body.String(), "do-not-disclose") {
		t.Fatal("internal error leaked")
	}
	onlyHTTP, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	checkProblem(t, request(t, onlyHTTP, "GET", "/api/v1/me", "2026-09-14", ""), 503, problem.Unavailable)
	if w = request(t, onlyHTTP, "GET", "/healthz", "", ""); w.Code != 200 {
		t.Fatal("health requires version")
	}
}

func TestStrictBodiesAndProblemLogging(t *testing.T) {
	v, b := &verifierStub{}, &backendStub{}
	r, err := New(nil, Dependencies{Backend: b, Verifier: v})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"null", "[]", "{", "{} {}", `{"role":"admin"}`, strings.Repeat("x", 16385)} {
		checkProblem(t, request(t, r, "POST", "/api/v1/bootstrap", "2026-09-14", body), 400, problem.InvalidRequest)
	}
	if b.calls != 0 {
		t.Fatal("invalid bodies reached backend")
	}
	var log bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	r.GET("/panic", func(*gin.Context) { panic("secret panic payload") })
	w := request(t, r, "GET", "/panic", "", "")
	body := checkProblem(t, w, 500, problem.Internal)
	instance, isString := body["instance"].(string)
	if !isString || !strings.Contains(log.String(), instance) || strings.Contains(log.String()+w.Body.String(), "secret panic payload") {
		t.Fatal("panic correlation or redaction failed")
	}
}

func TestVaryMergeAndPreflightExemption(t *testing.T) {
	h := http.Header{"Vary": []string{"Origin, Accept-Encoding"}}
	addVary(h, VersionHeader)
	addVary(h, strings.ToLower(VersionHeader))
	if len(h.Values("Vary")) != 2 || h.Values("Vary")[0] != "Origin, Accept-Encoding" {
		t.Fatalf("vary=%v", h)
	}
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), "OPTIONS", "/api/v1/me", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	checkProblem(t, w, 405, problem.MethodNotAllowed)
	if w.Header().Get(VersionHeader) != "" {
		t.Fatal("preflight selected a version")
	}
}
