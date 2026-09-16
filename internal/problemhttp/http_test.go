package problemhttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/internal/problemhttp"
)

// kinds pins the published status and title of every problem kind.
var kinds = []struct {
	kind   problem.Kind
	status int
	title  string
}{
	{problem.VersionRequired, http.StatusBadRequest, "API version required"},
	{problem.VersionInvalid, http.StatusBadRequest, "Invalid API version"},
	{problem.VersionUnsupported, http.StatusBadRequest, "Unsupported API version"},
	{problem.InvalidRequest, http.StatusBadRequest, "Invalid request"},
	{problem.AuthenticationRequired, http.StatusUnauthorized, "Authentication required"},
	{problem.InvalidToken, http.StatusUnauthorized, "Invalid token"},
	{problem.UserDisabled, http.StatusForbidden, "User disabled"},
	{problem.AccessDenied, http.StatusForbidden, "Access denied"},
	{problem.MajorUnsupported, http.StatusNotFound, "Unsupported API major version"},
	{problem.NotFound, http.StatusNotFound, "Not found"},
	{problem.MethodNotAllowed, http.StatusMethodNotAllowed, "Method not allowed"},
	{problem.BootstrapRequired, http.StatusConflict, "Bootstrap required"},
	{problem.Conflict, http.StatusConflict, "Accounting conflict"},
	{problem.Unavailable, http.StatusServiceUnavailable, "Service unavailable"},
	{problem.Internal, http.StatusInternalServerError, "Internal server error"},
}

func context(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	return ctx, recorder
}

func TestEveryKindKeepsItsStatusAndTitle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range kinds {
		t.Run(string(tc.kind), func(t *testing.T) {
			ctx, recorder := context(t)
			problemhttp.Write(ctx, problem.New(tc.kind, "detail for "+string(tc.kind)))
			if recorder.Code != tc.status {
				t.Fatalf("status %d, want %d", recorder.Code, tc.status)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("content type %q", got)
			}
			var body map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			wantType := "https://ledger.ztd.me/errors/" + string(tc.kind)
			if body["type"] != wantType || body["title"] != tc.title || body["status"] != float64(tc.status) {
				t.Fatalf("body %v", body)
			}
			if body["detail"] != "detail for "+string(tc.kind) {
				t.Fatalf("detail %v", body["detail"])
			}
			instance, ok := body["instance"].(string)
			if !ok || !strings.HasPrefix(instance, "urn:uuid:") || len(instance) != len("urn:uuid:")+36 {
				t.Fatalf("instance %v", body["instance"])
			}
			authenticate := recorder.Header().Get("WWW-Authenticate")
			if tc.status == http.StatusUnauthorized && authenticate != "Bearer" {
				t.Fatalf("missing challenge: %q", authenticate)
			}
			if tc.status != http.StatusUnauthorized && authenticate != "" {
				t.Fatalf("unexpected challenge %q", authenticate)
			}
			if _, ok := body["supported_versions"]; ok {
				t.Fatalf("unsolicited supported_versions: %v", body)
			}
		})
	}
}

func TestEveryPublishedKindIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pinned := map[problem.Kind]bool{}
	for _, tc := range kinds {
		pinned[tc.kind] = true
	}
	published := problem.Kinds()
	if len(published) != len(pinned) {
		t.Fatalf("%d published kinds but %d pinned contract entries", len(published), len(pinned))
	}
	for _, kind := range published {
		if !pinned[kind] {
			t.Fatalf("published kind %q has no status and title in the contract test", kind)
		}
		ctx, recorder := context(t)
		problemhttp.Write(ctx, problem.New(kind, "detail"))
		if kind != problem.Internal && recorder.Code == http.StatusInternalServerError {
			t.Fatalf("kind %q is not registered by the responder", kind)
		}
	}
}

func TestOptionalExtensionsAndFieldErrors(t *testing.T) {
	ctx, recorder := context(t)
	p := problem.New(problem.VersionUnsupported, "unsupported")
	p.Fields = []problem.FieldError{{Location: "body", Name: "amount", Reason: "must be positive"}}
	problemhttp.Write(ctx, p, "2026-09-16", "2026-09-15")
	var body struct {
		Errors            []problem.FieldError `json:"errors"`
		SupportedVersions []string             `json:"supported_versions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Errors) != 1 || body.Errors[0].Name != "amount" {
		t.Fatalf("field errors %v", body.Errors)
	}
	if len(body.SupportedVersions) != 2 || body.SupportedVersions[0] != "2026-09-16" {
		t.Fatalf("supported versions %v", body.SupportedVersions)
	}
}

func TestRecoveryHidesPanicValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(problemhttp.Recovery())
	router.GET("/panic", func(*gin.Context) { panic("connection string with credentials") })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "https://ledger.ztd.me/errors/internal-error") {
		t.Fatalf("body %s", body)
	}
	if strings.Contains(body, "credentials") || strings.Contains(body, "panic") {
		t.Fatalf("panic value reached the client: %s", body)
	}
}

func TestUnknownKindUsesInternalProblem(t *testing.T) {
	ctx, recorder := context(t)
	problemhttp.Write(ctx, problem.New(problem.Kind("unregistered"), "private implementation"))
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || body["type"] != "https://ledger.ztd.me/errors/internal-error" || body["detail"] == "private implementation" {
		t.Fatalf("unexpected problem: %d %v", recorder.Code, body)
	}
}

func TestCommittedResponseIsNotOverwritten(t *testing.T) {
	ctx, recorder := context(t)
	ctx.String(http.StatusAccepted, "already committed")
	problemhttp.Write(ctx, problem.New(problem.Internal, "must not append"))
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "already committed" || !ctx.IsAborted() {
		t.Fatalf("response overwritten: %d %s", recorder.Code, recorder.Body.String())
	}
}
