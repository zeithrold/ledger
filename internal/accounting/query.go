package accounting

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zeithrold/ledger/internal/database/sqlgen"
	"github.com/zeithrold/ledger/internal/identity"
)

type cursor struct {
	Date string    `json:"date"`
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func queryParams(actor identity.Context, book uuid.UUID, f Filter) (sqlgen.AccountingTransactionsParams, error) {
	p := sqlgen.AccountingTransactionsParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), IncludeVoided: f.IncludeVoided, Kind: txt(f.Kind), Currency: txt(f.Currency)}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 100 {
		return p, invalid("limit", "Use a page size between 1 and 100.")
	}
	p.PageSize = f.Limit + 1
	var err error
	if p.FromDate, err = day(f.From); err != nil {
		return p, err
	}
	if p.ToDate, err = day(f.To); err != nil {
		return p, err
	}
	if p.FromDate.Valid && p.ToDate.Valid && p.FromDate.Time.After(p.ToDate.Time) {
		return p, invalid("from", "The start date must not follow the end date.")
	}
	if p.AccountID, err = optID(f.AccountID); err != nil {
		return p, err
	}
	if p.CategoryID, err = optID(f.CategoryID); err != nil {
		return p, err
	}
	if p.CounterpartyID, err = optID(f.CounterpartyID); err != nil {
		return p, err
	}
	if f.Currency != "" {
		if _, ok := currencyUnits[f.Currency]; !ok {
			return p, invalid("currency", "Choose a supported currency.")
		}
	}
	if f.Kind != "" && f.Kind != "income" && f.Kind != "expense" && f.Kind != "transfer" && f.Kind != "refund" && f.Kind != "opening" {
		return p, invalid("kind", "Choose a supported transaction type.")
	}
	if f.Cursor != "" {
		if len(f.Cursor) > 512 {
			return p, invalid("cursor", "The pagination cursor is invalid.")
		}
		b, e := base64.RawURLEncoding.DecodeString(f.Cursor)
		if e != nil {
			return p, invalid("cursor", "The pagination cursor is invalid.")
		}
		var c cursor
		if e = json.Unmarshal(b, &c); e != nil {
			return p, invalid("cursor", "The pagination cursor is invalid.")
		}
		if p.CursorDate, e = day(c.Date); e != nil || !p.CursorDate.Valid {
			return p, invalid("cursor", "The pagination cursor is invalid.")
		}
		if p.CursorID, e = optID(c.ID); e != nil || !p.CursorID.Valid || c.Time.IsZero() {
			return p, invalid("cursor", "The pagination cursor is invalid.")
		}
		p.CursorTime = pgtype.Timestamptz{Time: c.Time, Valid: true}
	}
	return p, nil
}

// Transactions returns current business revisions in stable descending date order.
func (s *Service) Transactions(ctx context.Context, actor identity.Context, book uuid.UUID, f Filter) (TransactionPage, error) {
	out := TransactionPage{Transactions: []Transaction{}, Links: []Link{}}
	if err := authorize(ctx, s.db.Queries, actor, book); err != nil {
		return out, err
	}
	p, err := queryParams(actor, book, f)
	if err != nil {
		return out, err
	}
	rows, err := s.db.Queries.AccountingTransactions(ctx, p)
	if err != nil {
		return out, mapped(err)
	}
	more := len(rows) > int(p.PageSize-1)
	if more {
		rows = rows[:len(rows)-1]
	}
	ids := []pgtype.UUID{}
	for _, row := range rows {
		var t Transaction
		if err = json.Unmarshal(row, &t); err != nil {
			return out, err
		}
		out.Transactions = append(out.Transactions, t)
		ids = append(ids, pid(uuid.MustParse(t.ID)))
	}
	if more {
		t := out.Transactions[len(out.Transactions)-1]
		b, e := json.Marshal(cursor{t.Data.OccurredOn, t.CreatedAt, t.ID})
		if e != nil {
			return out, e
		}
		next := base64.RawURLEncoding.EncodeToString(b)
		out.NextCursor = &next
	}
	links, err := s.db.Queries.AccountingPageLinks(ctx, sqlgen.AccountingPageLinksParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), TransactionIds: ids})
	if err != nil {
		return out, mapped(err)
	}
	for _, l := range links {
		out.Links = append(out.Links, linkDTO(l))
	}
	return out, nil
}

// Links returns the associations for one transaction, or all associations in the book.
func (s *Service) Links(ctx context.Context, actor identity.Context, book uuid.UUID, id string) ([]Link, error) {
	if err := authorize(ctx, s.db.Queries, actor, book); err != nil {
		return nil, err
	}
	key, err := optID(id)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Queries.AccountingLinks(ctx, sqlgen.AccountingLinksParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), Column3: key})
	out := []Link{}
	for _, l := range rows {
		out = append(out, linkDTO(l))
	}
	return out, mapped(err)
}

// Transaction returns a stable business record, including its immutable history.
func (s *Service) Transaction(ctx context.Context, actor identity.Context, book, id uuid.UUID) (Detail, error) {
	var out Detail
	err := pgx.BeginTxFunc(ctx, s.db.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		var readErr error
		out, readErr = readDetail(ctx, s.db.Queries.WithTx(tx), actor, book, id)
		return readErr
	})
	return out, mapped(err)
}

func readDetail(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book, id uuid.UUID) (Detail, error) {
	out := Detail{Links: []Link{}, History: []Revision{}, Journals: []JournalView{}}
	if err := authorize(ctx, q, actor, book); err != nil {
		return out, err
	}
	t, err := transaction(ctx, q, actor, book, id.String())
	if err != nil {
		return out, mapped(err)
	}
	out.Transaction = t
	links, err := q.AccountingLinks(ctx, sqlgen.AccountingLinksParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), Column3: pid(id)})
	for _, l := range links {
		out.Links = append(out.Links, linkDTO(l))
	}
	if err != nil {
		return out, err
	}
	rows, err := q.AccountingHistory(ctx, sqlgen.AccountingHistoryParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), TransactionID: pid(id)})
	if err != nil {
		return out, mapped(err)
	}
	for _, r := range rows {
		var entry EntryInput
		if err = json.Unmarshal(r.Data, &entry); err != nil {
			return out, err
		}
		out.History = append(out.History, Revision{r.Revision, r.Voided, entry, sid(r.ActorID), r.CreatedAt.Time})
	}
	if t.Data.Kind == "expense" && t.Status == "posted" {
		a, e := getAccount(ctx, q, actor, book, t.Data.AccountID, true)
		if e != nil {
			return out, mapped(e)
		}
		refunded, e := q.AccountingRefunded(ctx, sqlgen.AccountingRefundedParams{SourceID: pid(id), ID: pid(uuid.Nil)})
		if e != nil {
			return out, mapped(e)
		}
		m, e := ParseMoney(t.Data.Amount, a.Currency)
		if e != nil {
			return out, e
		}
		used, e := ParseMoney(refunded, a.Currency)
		if e != nil {
			return out, e
		}
		out.RefundableAmount = (Money{new(big.Int).Sub(m.units, used.units), m.scale}).String()
	}
	if t.Data.Kind == "transfer" {
		a, e := getAccount(ctx, q, actor, book, t.Data.AccountID, true)
		if e != nil {
			return out, mapped(e)
		}
		b, e := getAccount(ctx, q, actor, book, t.Data.ToAccountID, true)
		if e != nil {
			return out, mapped(e)
		}
		m, e := ParseMoney(t.Data.Amount, a.Currency)
		if e != nil {
			return out, e
		}
		n, e := ParseMoney(t.Data.ToAmount, b.Currency)
		if e != nil {
			return out, e
		}
		numerator, denominator, display := Ratio(m, n)
		source := "identity"
		if a.Currency != b.Currency {
			source = "manual_actual"
		}
		out.ExchangeRate = &ExchangeRate{a.Currency, b.Currency, numerator, denominator, display, t.Data.OccurredOn, source}
	}
	documents, err := q.AccountingJournals(ctx, sqlgen.AccountingJournalsParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), TransactionID: pid(id)})
	if err != nil {
		return out, mapped(err)
	}
	for _, document := range documents {
		var j JournalView
		if err = json.Unmarshal(document, &j); err != nil {
			return out, err
		}
		out.Journals = append(out.Journals, j)
	}
	return out, nil
}

// Summary returns per-currency totals and independently labeled category rollups.
func (s *Service) Summary(ctx context.Context, actor identity.Context, book uuid.UUID, from, to string) (Summary, error) {
	out := Summary{Totals: []SummaryRow{}, Categories: []SummaryRow{}}
	if err := authorize(ctx, s.db.Queries, actor, book); err != nil {
		return out, err
	}
	p, err := queryParams(actor, book, Filter{From: from, To: to})
	if err != nil {
		return out, err
	}
	rows, err := s.db.Queries.AccountingSummary(ctx, sqlgen.AccountingSummaryParams{TenantID: p.TenantID, BookID: p.BookID, FromDate: p.FromDate, ToDate: p.ToDate})
	if err != nil {
		return out, mapped(err)
	}
	categories, err := s.db.Queries.AccountingCategories(ctx, sqlgen.AccountingCategoriesParams{TenantID: p.TenantID, BookID: p.BookID})
	if err != nil {
		return out, mapped(err)
	}
	parents := map[string]string{}
	for _, c := range categories {
		parents[sid(c.ID)] = sid(c.ParentID)
	}
	totals := map[[3]string]*big.Rat{}
	rollups := map[[3]string]*big.Rat{}
	add := func(m map[[3]string]*big.Rat, k [3]string, r *big.Rat) {
		if m[k] == nil {
			m[k] = new(big.Rat)
		}
		m[k].Add(m[k], r)
	}
	for _, r := range rows {
		n, ok := new(big.Rat).SetString(r.Amount)
		if !ok {
			return out, errors.New("invalid stored summary amount")
		}
		add(totals, [3]string{r.Currency, r.Role, ""}, n)
		add(rollups, [3]string{r.Currency, r.Role, sid(r.CategoryID)}, n)
		if parent := parents[sid(r.CategoryID)]; parent != "" {
			add(rollups, [3]string{r.Currency, r.Role, parent}, n)
		}
	}
	build := func(values map[[3]string]*big.Rat) []SummaryRow {
		result := []SummaryRow{}
		for k, r := range values {
			result = append(result, SummaryRow{k[0], k[1], k[2], r.FloatString(currencyUnits[k[0]])})
		}
		sort.Slice(result, func(i, j int) bool {
			a, b := result[i], result[j]
			if a.Currency != b.Currency {
				return a.Currency < b.Currency
			}
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return a.CategoryID < b.CategoryID
		})
		return result
	}
	out.Totals = build(totals)
	out.Categories = build(rollups)
	return out, nil
}
