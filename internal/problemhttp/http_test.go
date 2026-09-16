package problemhttp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/internal/problemhttp"
)

func TestUnknownKindUsesInternalProblem(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
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
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	ctx.String(http.StatusAccepted, "already committed")
	problemhttp.Write(ctx, problem.New(problem.Internal, "must not append"))
	if recorder.Code != http.StatusAccepted || recorder.Body.String() != "already committed" || !ctx.IsAborted() {
		t.Fatalf("response overwritten: %d %s", recorder.Code, recorder.Body.String())
	}
}
