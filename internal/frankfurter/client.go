// Package frankfurter adapts the Frankfurter v2 reference-rate API to the
// market-rate provider interface. One call performs at most one bounded HTTP
// request and never logs or returns upstream credentials.
package frankfurter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeithrold/ledger/internal/rates"
)

// maxResponseBytes bounds one provider response so a hostile or broken upstream
// cannot exhaust memory.
const maxResponseBytes = 1 << 20

// Client is a bounded Frankfurter v2 HTTP adapter.
type Client struct {
	endpoint  string
	providers []string
	http      *http.Client
}

// New pins the endpoint, optional provider filter and request timeout. The
// adapter owns redirect and timeout policy so no caller can widen it, and it
// normalizes provider keys to the lowercase form the API expects.
func New(endpoint string, providers []string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	normalized := make([]string, 0, len(providers))
	seen := make(map[string]bool, len(providers))
	for _, provider := range providers {
		if key := strings.ToLower(strings.TrimSpace(provider)); key != "" && !seen[key] {
			seen[key] = true
			normalized = append(normalized, key)
		}
	}
	return &Client{
		endpoint:  strings.TrimSuffix(endpoint, "/"),
		providers: normalized,
		http: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type rateRow struct {
	Date      string          `json:"date"`
	Base      string          `json:"base"`
	Quote     string          `json:"quote"`
	Rate      json.Number     `json:"rate"`
	Providers []providerEntry `json:"providers"`
}

type providerEntry struct {
	Key string `json:"key"`
}

// Latest fetches the current reference rates for one base currency.
func (c *Client) Latest(ctx context.Context, base string) ([]rates.Quote, error) {
	target, err := url.Parse(c.endpoint + "/v2/rates")
	if err != nil {
		return nil, errors.New("invalid frankfurter endpoint")
	}
	query := target.Query()
	query.Set("base", strings.ToLower(base))
	query.Set("expand", "providers")
	if len(c.providers) > 0 {
		query.Set("providers", strings.Join(c.providers, ","))
	}
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("build frankfurter request failed")
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, errors.New("frankfurter request failed")
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.New("frankfurter response could not be read")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("frankfurter returned status %d", response.StatusCode)
	}
	var rows []rateRow
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err = decoder.Decode(&rows); err != nil {
		return nil, errors.New("frankfurter returned an invalid response")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("frankfurter returned trailing data")
	}
	quotes := make([]rates.Quote, 0, len(rows))
	for _, row := range rows {
		providers := make([]string, 0, len(row.Providers))
		for _, entry := range row.Providers {
			providers = append(providers, entry.Key)
		}
		quotes = append(quotes, rates.Quote{
			Base: strings.ToUpper(row.Base), Quote: strings.ToUpper(row.Quote),
			Rate: row.Rate.String(), RateDate: row.Date, Providers: providers,
		})
	}
	return quotes, nil
}
