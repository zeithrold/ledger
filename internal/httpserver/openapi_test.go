package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/zeithrold/ledger/internal/apicontract"
	"github.com/zeithrold/ledger/internal/contracttest"
)

func TestOpenAPIRoutesAndResponses(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(apicontract.Document())
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			count++
			found := false
			for _, route := range r.Routes() {
				normalized := route.Path
				for _, part := range strings.Split(route.Path, "/") {
					if strings.HasPrefix(part, ":") {
						normalized = strings.ReplaceAll(normalized, part, "{"+part[1:]+"}")
					}
				}
				if route.Method == method && normalized == path {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing route %s %s", method, path)
			}
			actual := strings.ReplaceAll(strings.ReplaceAll(path, "{book_id}", "00000000-0000-0000-0000-000000000001"), "{user_id}", "00000000-0000-0000-0000-000000000001")
			req := httptest.NewRequestWithContext(t.Context(), method, actual, nil)
			req.Header.Set(VersionHeader, apicontract.Version())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			contracttest.Response(t, req, w)
		}
	}
	if count != len(r.Routes()) {
		t.Fatal("undocumented routes")
	}
}

func TestDocsConfiguration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		r, err := New(nil, Dependencies{DocsEnabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/docs", apicontract.URL(), "/docs/assets/init.js"} {
			w := request(t, r, "GET", path, "", "")
			expected := 404
			if enabled {
				expected = 200
			}
			if w.Code != expected {
				t.Fatalf("%s status %d", path, w.Code)
			}
			if w.Header().Get(VersionHeader) != "" {
				t.Fatal("docs accepted an API version")
			}
		}
	}
}

func TestOpenAPIRejectsResponseDrift(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(apicontract.Document())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		value map[string]any
	}{
		{"User", map[string]any{"id": "00000000-0000-0000-0000-000000000001", "display_name": "Test"}},
		{"Problem", map[string]any{"error": map[string]any{"status": 400}}},
		{"Preferences", map[string]any{"locale": "en", "timezone": "UTC", "theme": "invalid"}},
	} {
		if err = doc.Components.Schemas[tc.name].Value.VisitJSON(tc.value); err == nil {
			t.Fatalf("%s accepted response drift", tc.name)
		}
	}
}
