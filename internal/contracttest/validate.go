// Package contracttest checks HTTP responses against the published contract.
package contracttest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/zeithrold/ledger/internal/apicontract"
)

// Response validates a recorded response without accepting invalid requests as valid.
func Response(t testing.TB, req *http.Request, w *httptest.ResponseRecorder) {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(apicontract.Document())
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Validate(req.Context()); err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		t.Fatal(err)
	}
	input := &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}, Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes())), Options: &openapi3filter.Options{IncludeResponseStatus: true}}
	if err = openapi3filter.ValidateResponse(req.Context(), input); err != nil {
		t.Fatalf("%s %s response violates contract: %v", req.Method, req.URL.Path, err)
	}
}
