//go:build integration

package integration_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/problem"
)

func TestAccountingQueriesAndPrecision(t *testing.T) {
	db := phaseOneDB(t)
	s := accounting.New(db)
	actor, _, err := identity.New(db).Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "query-test"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "UTC", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	book := actor.DefaultBook.ID
	account, err := s.CreateAccount(t.Context(), actor, book, uuid.NewString(), accounting.AccountInput{Name: "Cash", Kind: "cash", Currency: "CNY", OpeningAmount: "-2", OpeningDate: "2026-09-01"})
	if err != nil || account.Balance != "-2.00" {
		t.Fatal(account, err)
	}
	categories, err := s.Categories(t.Context(), actor, book)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, c := range categories {
		ids[c.SystemCode] = c.ID
	}
	cp, err := s.CreateCounterparty(t.Context(), actor, uuid.NewString(), accounting.CounterpartyInput{Name: "Cafe"})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"food", "meals"} {
		_, err = s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), accounting.CreateInput{Entry: accounting.EntryInput{Kind: "expense", OccurredOn: "2026-09-15", AccountID: account.ID, Amount: "10", CategoryID: ids[code], CounterpartyID: cp.ID}})
		if err != nil {
			t.Fatal(err)
		}
	}
	summary, err := s.Summary(t.Context(), actor, book, "2026-09-15", "2026-09-15")
	if err != nil || len(summary.Totals) != 1 || summary.Totals[0].Amount != "20.00" {
		t.Fatal(summary, err)
	}
	rollups := map[string]string{}
	for _, row := range summary.Categories {
		rollups[row.CategoryID] = row.Amount
	}
	if rollups[ids["food"]] != "20.00" || rollups[ids["meals"]] != "10.00" {
		t.Fatal("root must include own and child without double counting", rollups)
	}
	filter := accounting.Filter{From: "2026-09-15", To: "2026-09-15", Kind: "expense", Currency: "CNY", AccountID: account.ID, CategoryID: ids["food"], CounterpartyID: cp.ID, Limit: 1}
	first, err := s.Transactions(t.Context(), actor, book, filter)
	if err != nil || len(first.Transactions) != 1 || first.NextCursor == nil {
		t.Fatal(first, err)
	}
	filter.Cursor = *first.NextCursor
	second, err := s.Transactions(t.Context(), actor, book, filter)
	if err != nil || len(second.Transactions) != 1 || second.NextCursor != nil || first.Transactions[0].ID == second.Transactions[0].ID {
		t.Fatal(second, err)
	}
	filter.Currency, filter.Cursor = "USD", ""
	page, err := s.Transactions(t.Context(), actor, book, filter)
	if err != nil || len(page.Transactions) != 0 {
		t.Fatal(page, err)
	}
	for _, tc := range []struct{ currency, amount string }{{"JPY", "0.1"}, {"KWD", "0.0001"}, {"CNY", "1.001"}, {"USD", "1000000000000000000"}} {
		_, err = s.CreateAccount(t.Context(), actor, book, uuid.NewString(), accounting.AccountInput{Name: tc.currency, Kind: "bank", Currency: tc.currency, OpeningAmount: tc.amount, OpeningDate: "2026-09-15"})
		expectKind(t, err, problem.InvalidRequest)
	}
	other, err := s.CreateAccount(t.Context(), actor, book, uuid.NewString(), accounting.AccountInput{Name: "Wallet", Kind: "wallet", Currency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	transfer := accounting.CreateInput{Entry: accounting.EntryInput{Kind: "transfer", OccurredOn: "2026-09-15", AccountID: account.ID, Amount: "3", ToAccountID: other.ID, ToAmount: "4"}}
	_, err = s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), transfer)
	expectKind(t, err, problem.InvalidRequest)
	transfer.Entry.ToAmount = "3"
	result, err := s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), transfer)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.Transaction(t.Context(), actor, book, uuid.MustParse(result.Transactions[0].ID))
	if err != nil || detail.ExchangeRate.Numerator != "1" || detail.ExchangeRate.Source != "identity" {
		t.Fatal(detail, err)
	}
	_, err = s.CreateLink(t.Context(), actor, book, uuid.NewString(), accounting.LinkInput{SourceID: result.Transactions[0].ID, TargetID: first.Transactions[0].ID, Kind: "fee"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Summary(t.Context(), actor, book, "2026-09-15", "2026-09-15")
	if err != nil || after.Totals[0].Amount != "20.00" {
		t.Fatal("transfer and existing fee link must not duplicate expenses", after, err)
	}
}
