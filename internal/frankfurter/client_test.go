package frankfurter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const sampleBody = `[{"date":"2026-09-16","base":"USD","quote":"CNY","rate":6.7082,"providers":[{"key":"AMCM","date":"2026-09-16","rate":6.7137},{"key":"ECB","date":"2026-09-16","rate":6.7}]},` +
	`{"date":"2026-09-15","base":"USD","quote":"ANG","rate":1.79}]`

// respond writes a canned body; a failed test-server write is reported instead
// of being silently discarded.
func respond(t *testing.T, w http.ResponseWriter, body string) {
	if _, err := w.Write([]byte(body)); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestLatestParsesQuotesAndQuery(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/v2/rates" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("accept %s", r.Header.Get("Accept"))
		}
		respond(t, w, sampleBody)
	}))
	defer server.Close()
	quotes, err := New(server.URL+"/", []string{"ECB", " boe ", "ecb"}, 5*time.Second).Latest(t.Context(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	query, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("base") != "usd" || query.Get("expand") != "providers" || query.Get("providers") != "ecb,boe" {
		t.Fatalf("query %s", gotQuery)
	}
	if len(quotes) != 2 {
		t.Fatalf("quotes %+v", quotes)
	}
	first := quotes[0]
	if first.Base != "USD" || first.Quote != "CNY" || first.Rate != "6.7082" || first.RateDate != "2026-09-16" || len(first.Providers) != 2 || first.Providers[0] != "AMCM" {
		t.Fatalf("first quote %+v", first)
	}
	if quotes[1].Rate != "1.79" {
		t.Fatalf("second quote %+v", quotes[1])
	}
}

func TestLatestBlendedOmitsProviderFilter(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respond(t, w, `[]`)
	}))
	defer server.Close()
	quotes, err := New(server.URL, nil, 5*time.Second).Latest(t.Context(), "EUR")
	if err != nil || len(quotes) != 0 {
		t.Fatalf("quotes %+v err %v", quotes, err)
	}
	if strings.Contains(gotQuery, "providers=") {
		t.Fatalf("blended request filtered providers: %s", gotQuery)
	}
}

func TestLatestRejectsBadResponses(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"redirect": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "https://example.invalid")
			w.WriteHeader(http.StatusFound)
		},
		"json":     func(w http.ResponseWriter, _ *http.Request) { respond(t, w, `{"not":"an array"}`) },
		"trailing": func(w http.ResponseWriter, _ *http.Request) { respond(t, w, sampleBody+sampleBody) },
		"oversized": func(w http.ResponseWriter, _ *http.Request) {
			respond(t, w, `[{"date":"2026-09-16","base":"USD","quote":"CNY","rate":1,"padding":"`+strings.Repeat("x", maxResponseBytes+1)+`"}]`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			if _, err := New(server.URL, nil, 3*time.Second).Latest(t.Context(), "USD"); err == nil {
				t.Fatal("accepted an invalid provider response")
			}
		})
	}
}

// A text-encoded rate is tolerated because json.Number retains the literal and
// the market-rate service parses the exact decimal itself.
func TestLatestAcceptsTextEncodedRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, `[{"date":"2026-09-16","base":"USD","quote":"CNY","rate":"6.7"}]`)
	}))
	defer server.Close()
	quotes, err := New(server.URL, nil, 3*time.Second).Latest(t.Context(), "USD")
	if err != nil || len(quotes) != 1 || quotes[0].Rate != "6.7" {
		t.Fatalf("quotes %+v err %v", quotes, err)
	}
}

func TestLatestHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		respond(t, w, sampleBody)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := New(server.URL, nil, 5*time.Second).Latest(ctx, "USD"); err == nil {
		t.Fatal("ignored context cancellation")
	}
}

func TestLatestRejectsInvalidEndpoint(t *testing.T) {
	if _, err := New("://bad", nil, 3*time.Second).Latest(t.Context(), "USD"); err == nil {
		t.Fatal("accepted an invalid endpoint")
	}
}

func TestNewInstallsBoundedTransport(t *testing.T) {
	client := New("https://api.frankfurter.dev", nil, 0)
	if client.http.Timeout != 5*time.Second {
		t.Fatalf("timeout %s", client.http.Timeout)
	}
	if err := client.http.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy %v", err)
	}
}
