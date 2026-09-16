package rates

import (
	"context"
	"errors"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/database/sqlgen"
	"github.com/zeithrold/ledger/internal/money"
	"github.com/zeithrold/ledger/internal/problem"
)

const (
	maxQuotes    = 5000
	maxProviders = 64
)

var (
	codePattern = regexp.MustCompile(`^[A-Z]{3}$`)
	maxRate     = big.NewInt(1000000000000000000)
)

var supportedCurrencies = func() map[string]bool {
	result := make(map[string]bool)
	for _, currency := range money.Currencies() {
		result[currency.Code] = true
	}
	return result
}()

// Service owns the public market-data cache behind one database boundary.
type Service struct {
	db             *database.DB
	provider       Provider
	providerFilter string
	retentionDays  int
	clock          Clock
}

// Options configures a Service; zero values select production defaults.
type Options struct {
	Provider       Provider
	ProviderFilter string
	RetentionDays  int
	Clock          Clock
}

// New constructs the service without fetching anything.
func New(db *database.DB, options Options) *Service {
	service := &Service{db: db, provider: options.Provider, providerFilter: options.ProviderFilter, retentionDays: options.RetentionDays, clock: options.Clock}
	if service.providerFilter == "" {
		service.providerFilter = BlendedFilter
	}
	if service.retentionDays <= 0 {
		service.retentionDays = DefaultRetentionDays
	}
	if service.clock == nil {
		service.clock = time.Now
	}
	return service
}

type normalizedQuote struct {
	quote     string
	rate      string
	rateDate  time.Time
	providers []string
}

// SyncDaily publishes one snapshot. A published batch is never rewritten, a
// provider failure leaves the previous batch intact and no network call happens
// while a database transaction is open.
func (s *Service) SyncDaily(ctx context.Context, date string) (Result, error) {
	day, err := time.Parse(time.DateOnly, date)
	if err != nil || day.Format(time.DateOnly) != date {
		return Result{}, errors.New("snapshot date must be YYYY-MM-DD")
	}
	if s.provider == nil {
		return Result{}, errors.New("no reference-rate provider is configured")
	}
	result := Result{SnapshotDate: date}
	key := sqlgen.HasPublishedMarketRateSnapshotParams{SnapshotDate: dateValue(day), Source: Source, SourceVersion: SourceVersion, ProviderFilter: s.providerFilter}
	switch _, err = s.db.Queries.HasPublishedMarketRateSnapshot(ctx, key); {
	case err == nil:
		return result, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return Result{}, err
	}
	quotes, err := s.provider.Latest(ctx, Pivot)
	if err != nil {
		return Result{}, err
	}
	rows, err := normalizeQuotes(quotes)
	if err != nil {
		return Result{}, err
	}
	fetched := s.clock().UTC()
	err = pgx.BeginFunc(ctx, s.db.Pool, func(tx pgx.Tx) error {
		q := s.db.Queries.WithTx(tx)
		inserted := uuid.New()
		if e := q.InsertMarketRateSnapshot(ctx, sqlgen.InsertMarketRateSnapshotParams{
			ID: idValue(inserted), SnapshotDate: dateValue(day), Source: Source, SourceVersion: SourceVersion,
			ProviderFilter: s.providerFilter, BaseCurrency: Pivot, FetchedAt: timeValue(fetched),
		}); e != nil {
			return e
		}
		stored, e := q.FindMarketRateSnapshot(ctx, sqlgen.FindMarketRateSnapshotParams{
			SnapshotDate: dateValue(day), Source: Source, SourceVersion: SourceVersion, ProviderFilter: s.providerFilter,
		})
		if e != nil {
			return e
		}
		if stored.Status == "published" {
			return nil
		}
		if e = q.DeleteMarketRates(ctx, stored.ID); e != nil {
			return e
		}
		params := make([]sqlgen.InsertMarketRatesParams, 0, len(rows))
		for _, row := range rows {
			value, valueErr := numericValue(row.rate)
			if valueErr != nil {
				return valueErr
			}
			params = append(params, sqlgen.InsertMarketRatesParams{
				ID: idValue(uuid.New()), SnapshotID: stored.ID, BaseCurrency: Pivot, QuoteCurrency: row.quote,
				Rate: value, RateDate: dateValue(row.rateDate), Providers: row.providers,
			})
		}
		if _, e = q.InsertMarketRates(ctx, params); e != nil {
			return e
		}
		if e = q.PublishMarketRateSnapshot(ctx, sqlgen.PublishMarketRateSnapshotParams{ID: stored.ID, PublishedAt: timeValue(s.clock().UTC())}); e != nil {
			return e
		}
		// A caller-supplied future date must never prune the recent cache: the
		// retention window is anchored to the newer of the snapshot date and the
		// current UTC day.
		anchor := day
		if now := s.clock().UTC(); now.Before(anchor) {
			anchor = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		}
		if e = q.PruneMarketRateSnapshots(ctx, dateValue(anchor.AddDate(0, 0, -(s.retentionDays-1)))); e != nil {
			return e
		}
		result.Published, result.Rates = true, len(params)
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Rate resolves one pair from the newest published snapshot that carries every
// leg it needs. It never calls the provider.
func (s *Service) Rate(ctx context.Context, base, quote string) (Rate, error) {
	base, quote = strings.TrimSpace(base), strings.TrimSpace(quote)
	if err := validatePair(base, quote); err != nil {
		return Rate{}, err
	}
	out := Rate{Base: base, Quote: quote, Source: Source, Status: StatusUnavailable, Reason: ReasonNoSnapshot}
	latest, err := s.db.Queries.LatestPublishedMarketSnapshotDate(ctx)
	if err != nil {
		return Rate{}, err
	}
	if latest.Valid {
		out.LatestSnapshotDate = latest.Time.Format(time.DateOnly)
	}
	first, second := legs(base, quote, Pivot)
	snapshot, err := s.db.Queries.CurrentMarketRateSnapshot(ctx, sqlgen.CurrentMarketRateSnapshotParams{BaseCurrency: Pivot, QuoteCurrency: first, Leg2: textValue(second)})
	if errors.Is(err, pgx.ErrNoRows) {
		// A published cache exists but carries none of the legs this pair needs.
		if latest.Valid {
			out.Reason = ReasonPairUnavailable
		}
		return out, nil
	}
	if err != nil {
		return Rate{}, err
	}
	out.Reason = ReasonPairUnavailable
	out.ProviderFilter = snapshot.ProviderFilter
	needed := []string{first}
	if second != "" {
		needed = append(needed, second)
	}
	stored, err := s.db.Queries.MarketSnapshotRates(ctx, sqlgen.MarketSnapshotRatesParams{SnapshotID: snapshot.ID, Quotes: needed})
	if err != nil {
		return Rate{}, err
	}
	quotes, dates := make(map[string]string, len(stored)), make(map[string]string, len(stored))
	for _, row := range stored {
		value, valueErr := row.Rate.Value()
		text, isText := value.(string)
		if valueErr != nil || !isText || !row.RateDate.Valid {
			continue
		}
		quotes[row.QuoteCurrency] = text
		dates[row.QuoteCurrency] = row.RateDate.Time.Format(time.DateOnly)
	}
	display, numerator, denominator, derived, used, ok := resolveLegs(base, quote, Pivot, quotes)
	if !ok {
		return out, nil
	}
	out.Pivot, out.Display, out.Numerator, out.Denominator, out.Derived = Pivot, display, numerator, denominator, derived
	if snapshot.SnapshotDate.Valid {
		out.SnapshotDate = snapshot.SnapshotDate.Time.Format(time.DateOnly)
	}
	out.Status, out.Stale, out.Reason = StatusAvailable, false, ""
	if out.SnapshotDate != s.clock().UTC().Format(time.DateOnly) {
		out.Status, out.Stale = StatusStale, true
	}
	if snapshot.FetchedAt.Valid {
		out.FetchedAt = snapshot.FetchedAt.Time.UTC().Format(time.RFC3339)
	}
	out.RateDate = earliest(used, dates)
	if out.RateDate == "" {
		out.RateDate = out.SnapshotDate
	}
	return out, nil
}

func earliest(used []string, dates map[string]string) string {
	result := ""
	for _, code := range used {
		date, found := dates[code]
		if !found {
			continue
		}
		if result == "" || date < result {
			result = date
		}
	}
	return result
}

func normalizeQuotes(quotes []Quote) ([]normalizedQuote, error) {
	if len(quotes) == 0 {
		return nil, errors.New("provider returned no reference rates")
	}
	if len(quotes) > maxQuotes {
		return nil, errors.New("provider returned too many reference rates")
	}
	seen := make(map[string]bool, len(quotes))
	rows := make([]normalizedQuote, 0, len(quotes))
	for _, quote := range quotes {
		if quote.Base != Pivot {
			return nil, errors.New("provider returned an unexpected base currency")
		}
		// The provider echoes the pivot against itself; it carries no rate and
		// the cache forbids a self pair, so it is skipped rather than stored.
		if quote.Quote == Pivot {
			continue
		}
		if !codePattern.MatchString(quote.Quote) {
			return nil, errors.New("provider returned an invalid quote currency")
		}
		value, err := money.ParseDecimal(quote.Rate)
		if err != nil || value.Sign() <= 0 || value.Cmp(new(big.Rat).SetInt(maxRate)) >= 0 {
			return nil, errors.New("provider returned an invalid rate")
		}
		date, err := time.Parse(time.DateOnly, quote.RateDate)
		if err != nil || date.Format(time.DateOnly) != quote.RateDate {
			return nil, errors.New("provider returned an invalid rate date")
		}
		if seen[quote.Quote] {
			continue
		}
		seen[quote.Quote] = true
		rows = append(rows, normalizedQuote{quote: quote.Quote, rate: quote.Rate, rateDate: date, providers: normalizeProviders(quote.Providers)})
	}
	if len(rows) == 0 {
		return nil, errors.New("provider returned no usable reference rates")
	}
	return rows, nil
}

func normalizeProviders(providers []string) []string {
	result := make([]string, 0, len(providers))
	seen := make(map[string]bool, len(providers))
	for _, provider := range providers {
		key := strings.ToLower(strings.TrimSpace(provider))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, key)
		if len(result) == maxProviders {
			break
		}
	}
	sort.Strings(result)
	return result
}

func validatePair(base, quote string) error {
	if !codePattern.MatchString(base) {
		return invalid("base", "Provide an uppercase ISO 4217 currency code.")
	}
	if !codePattern.MatchString(quote) {
		return invalid("quote", "Provide an uppercase ISO 4217 currency code.")
	}
	if base == quote {
		return invalid("quote", "The base and quote currencies must differ.")
	}
	if !supportedCurrencies[base] {
		return invalid("base", "Use a currency from the ledger catalog.")
	}
	if !supportedCurrencies[quote] {
		return invalid("quote", "Use a currency from the ledger catalog.")
	}
	return nil
}

func invalid(field, reason string) error {
	return &problem.Error{Kind: problem.InvalidRequest, Detail: "Exchange-rate parameters are invalid.", Fields: []problem.FieldError{{Location: "query", Name: field, Reason: reason}}}
}

func idValue(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func textValue(value string) pgtype.Text { return pgtype.Text{String: value, Valid: value != ""} }

func dateValue(value time.Time) pgtype.Date { return pgtype.Date{Time: value, Valid: true} }

func timeValue(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func numericValue(value string) (pgtype.Numeric, error) {
	var number pgtype.Numeric
	err := number.Scan(value)
	return number, err
}
