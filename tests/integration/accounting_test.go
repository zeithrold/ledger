//go:build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/apicontract"
	"github.com/zeithrold/ledger/internal/auth"
	"github.com/zeithrold/ledger/internal/contracttest"
	"github.com/zeithrold/ledger/internal/httpserver"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/problem"
)

func TestAccountingLifecycle(t *testing.T) {
	db := phaseOneDB(t)
	s := accounting.New(db)
	identities := identity.New(db)
	actor, _, err := identities.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "accounting"}, identity.BootstrapInput{BaseCurrency: "CNY", Timezone: "Asia/Shanghai", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	book := actor.DefaultBook.ID
	categories, err := s.Categories(t.Context(), actor, book)
	if err != nil {
		t.Fatal(err)
	}
	byCode := map[string]string{}
	for _, c := range categories {
		byCode[c.SystemCode] = c.ID
	}
	if len(byCode) != 16 {
		t.Fatalf("category seeds: %d", len(byCode))
	}
	createAccount := func(currency, opening string) accounting.Account {
		t.Helper()
		a, e := s.CreateAccount(t.Context(), actor, book, uuid.NewString(), accounting.AccountInput{Name: currency, Kind: "bank", Currency: currency, OpeningAmount: opening, OpeningDate: "2026-09-01"})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	usd := createAccount("USD", "1000")
	cny := createAccount("CNY", "0")
	balances := func(wantUSD, wantCNY string) {
		t.Helper()
		accounts, e := s.Accounts(t.Context(), actor, book)
		if e != nil {
			t.Fatal(e)
		}
		got := map[string]string{}
		for _, a := range accounts {
			got[a.ID] = a.Balance
		}
		if got[usd.ID] != wantUSD || got[cny.ID] != wantCNY {
			t.Fatalf("balances=%v expected %s, %s", got, wantUSD, wantCNY)
		}
	}
	input := accounting.CreateInput{Entry: accounting.EntryInput{Kind: "transfer", OccurredOn: "2026-09-10", AccountID: usd.ID, Amount: "100", ToAccountID: cny.ID, ToAmount: "720"}, Fee: &accounting.EntryInput{Kind: "expense", OccurredOn: "2026-09-10", AccountID: usd.ID, Amount: "1", CategoryID: byCode["fees"]}}
	key := uuid.NewString()
	var result accounting.MutationResult
	t.Run("concurrent duplicate creates one principal and one independent fee", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make([]accounting.MutationResult, 6)
		errs := make([]error, 6)
		for i := range results {
			wg.Go(func() { results[i], errs[i] = s.CreateTransaction(t.Context(), actor, book, key, input) })
		}
		wg.Wait()
		for i, e := range errs {
			if e != nil {
				t.Fatal(e)
			}
			if len(results[i].Transactions) != 2 || len(results[i].Links) != 1 {
				t.Fatal(results[i])
			}
			if results[i].Transactions[0].ID != results[0].Transactions[0].ID {
				t.Fatal("duplicate transaction")
			}
		}
		result = results[0]
		balances("899.00", "720.00")
		changed := input
		changed.Entry.Amount = "101"
		_, e := s.CreateTransaction(t.Context(), actor, book, key, changed)
		expectKind(t, e, problem.Conflict)
		summary, e := s.Summary(t.Context(), actor, book, "", "")
		if e != nil {
			t.Fatal(e)
		}
		if len(summary.Totals) != 1 || summary.Totals[0].Amount != "1.00" || summary.Totals[0].Currency != "USD" {
			t.Fatal(summary)
		}
	})
	t.Run("fee failure rolls back principal and idempotency record", func(t *testing.T) {
		bad := input
		fee := *input.Fee
		fee.Amount = "0.001"
		bad.Fee = &fee
		k := uuid.NewString()
		before := countRows(t, db, "SELECT count(*) FROM transactions")
		if _, e := s.CreateTransaction(t.Context(), actor, book, k, bad); e == nil {
			t.Fatal("invalid fee accepted")
		}
		if countRows(t, db, "SELECT count(*) FROM transactions") != before {
			t.Fatal("partial principal")
		}
		if _, e := s.CreateTransaction(t.Context(), actor, book, k, input); e != nil {
			t.Fatal("failed operation consumed key", e)
		}
	})
	// Void the extra successful retry, returning to the baseline transfer.
	page, err := s.Transactions(t.Context(), actor, book, accounting.Filter{Kind: "transfer"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range page.Transactions {
		if tx.ID != result.Transactions[0].ID {
			detail, e := s.Transaction(t.Context(), actor, book, uuid.MustParse(tx.ID))
			if e != nil {
				t.Fatal(e)
			}
			fees := []accounting.Correction{}
			for _, l := range detail.Links {
				if l.Kind == "fee" {
					fees = append(fees, accounting.Correction{ID: l.TargetID, ExpectedRevision: 1})
				}
			}
			if _, e = s.CorrectTransaction(t.Context(), actor, book, uuid.MustParse(tx.ID), uuid.NewString(), accounting.CorrectionInput{ExpectedRevision: 1, Fees: fees}); e != nil {
				t.Fatal(e)
			}
		}
	}
	balances("899.00", "720.00")
	expense, err := s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), accounting.CreateInput{Entry: accounting.EntryInput{Kind: "expense", OccurredOn: "2026-09-11", AccountID: cny.ID, Amount: "100", CategoryID: byCode["meals"]}})
	if err != nil {
		t.Fatal(err)
	}
	original := expense.Transactions[0]
	refundInput := accounting.CreateInput{Entry: accounting.EntryInput{Kind: "refund", OccurredOn: "2026-09-12", AccountID: cny.ID, Amount: "30", OriginalID: original.ID}}
	refund, err := s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), refundInput)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("partial refunds reduce original expense and protect its principal", func(t *testing.T) {
		balances("899.00", "650.00")
		detail, e := s.Transaction(t.Context(), actor, book, uuid.MustParse(original.ID))
		if e != nil {
			t.Fatal(e)
		}
		if detail.RefundableAmount != "70.00" {
			t.Fatal(detail)
		}
		bad := refundInput
		bad.Entry.Amount = "70.01"
		_, e = s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), bad)
		expectKind(t, e, problem.Conflict)
		_, e = s.CorrectTransaction(t.Context(), actor, book, uuid.MustParse(original.ID), uuid.NewString(), accounting.CorrectionInput{ExpectedRevision: 1})
		expectKind(t, e, problem.Conflict)
		memo := original.Data
		memo.Note = "Reviewed"
		updated, e := s.CorrectTransaction(t.Context(), actor, book, uuid.MustParse(original.ID), uuid.NewString(), accounting.CorrectionInput{ExpectedRevision: 1, Entry: &memo})
		if e != nil {
			t.Fatal(e)
		}
		if updated.Transactions[0].ID != original.ID {
			t.Fatal("identity changed")
		}
		summary, e := s.Summary(t.Context(), actor, book, "", "")
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range summary.Totals {
			if r.Currency == "CNY" && r.Amount != "70.00" {
				t.Fatal(summary)
			}
		}
	})
	t.Run("concurrent refunds cannot exceed remaining principal", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Go(func() {
				in := refundInput
				in.Entry.Amount = "50"
				_, errs[i] = s.CreateTransaction(t.Context(), actor, book, uuid.NewString(), in)
			})
		}
		wg.Wait()
		success := 0
		for _, e := range errs {
			if e == nil {
				success++
			} else {
				expectKind(t, e, problem.Conflict)
			}
		}
		if success != 1 {
			t.Fatalf("success=%d", success)
		}
	})
	t.Run("ordinary relations do not change totals and can be removed", func(t *testing.T) {
		before, e := s.Summary(t.Context(), actor, book, "", "")
		if e != nil {
			t.Fatal(e)
		}
		l, e := s.CreateLink(t.Context(), actor, book, uuid.NewString(), accounting.LinkInput{SourceID: original.ID, TargetID: result.Transactions[0].ID, Kind: "related"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.CreateLink(t.Context(), actor, book, uuid.NewString(), accounting.LinkInput{SourceID: l.TargetID, TargetID: l.SourceID, Kind: "related"})
		expectKind(t, e, problem.Conflict)
		after, e := s.Summary(t.Context(), actor, book, "", "")
		if e != nil {
			t.Fatal(e)
		}
		a, e := json.Marshal(before)
		if e != nil {
			t.Fatal(e)
		}
		b, e := json.Marshal(after)
		if e != nil {
			t.Fatal(e)
		}
		if string(a) != string(b) {
			t.Fatal("link changed summary")
		}
		if _, e = s.RemoveLink(t.Context(), actor, book, uuid.MustParse(l.ID), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		_, e = s.RemoveLink(t.Context(), actor, book, uuid.MustParse(refund.Links[0].ID), uuid.NewString())
		expectKind(t, e, problem.InvalidRequest)
	})
	t.Run("selected fees correct atomically and history stays readable", func(t *testing.T) {
		main := result.Transactions[0]
		fee := result.Transactions[1]
		replacement := main.Data
		replacement.Amount = "120"
		replacement.ToAmount = "840"
		feeReplacement := fee.Data
		feeReplacement.Amount = "2"
		in := accounting.CorrectionInput{ExpectedRevision: 1, Entry: &replacement, Fees: []accounting.Correction{{ID: fee.ID, ExpectedRevision: 9, Entry: &feeReplacement}}}
		_, e := s.CorrectTransaction(t.Context(), actor, book, uuid.MustParse(main.ID), uuid.NewString(), in)
		expectKind(t, e, problem.Conflict)
		d, e := s.Transaction(t.Context(), actor, book, uuid.MustParse(main.ID))
		if e != nil || d.Transaction.Revision != 1 {
			t.Fatal("partial correction", e)
		}
		in.Fees[0].ExpectedRevision = 1
		updated, e := s.CorrectTransaction(t.Context(), actor, book, uuid.MustParse(main.ID), uuid.NewString(), in)
		if e != nil {
			t.Fatal(e)
		}
		if updated.Transactions[0].Revision != 2 {
			t.Fatal(updated)
		}
		d, e = s.Transaction(t.Context(), actor, book, uuid.MustParse(main.ID))
		if e != nil {
			t.Fatal(e)
		}
		if d.ExchangeRate.Display != "7" || len(d.History) != 2 {
			t.Fatal(d)
		}
		_, e = s.CorrectTransaction(t.Context(), actor, book, uuid.MustParse(main.ID), uuid.NewString(), accounting.CorrectionInput{ExpectedRevision: 2})
		if e != nil {
			t.Fatal(e)
		}
		f, e := s.Transaction(t.Context(), actor, book, uuid.MustParse(fee.ID))
		if e != nil || f.Transaction.Status != "posted" {
			t.Fatal("unselected fee changed", e)
		}
		d, e = s.Transaction(t.Context(), actor, book, uuid.MustParse(main.ID))
		if e != nil || d.Transaction.Status != "void" || len(d.History) != 3 {
			t.Fatal("missing void history", e)
		}
	})
	t.Run("classification has two levels and archives children", func(t *testing.T) {
		root, e := s.CreateCategory(t.Context(), actor, book, uuid.NewString(), accounting.CategoryInput{Name: "Travel", Kind: "expense"})
		if e != nil {
			t.Fatal(e)
		}
		child, e := s.CreateCategory(t.Context(), actor, book, uuid.NewString(), accounting.CategoryInput{Name: "Hotel", Kind: "expense", ParentID: root.ID})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.CreateCategory(t.Context(), actor, book, uuid.NewString(), accounting.CategoryInput{Name: "Third", Kind: "expense", ParentID: child.ID})
		expectKind(t, e, problem.InvalidRequest)
		_, e = s.UpdateCategory(t.Context(), actor, book, uuid.MustParse(root.ID), uuid.NewString(), accounting.ReferencePatch{ExpectedRevision: 1, Name: root.Name, Archived: true})
		if e != nil {
			t.Fatal(e)
		}
		cats, e := s.Categories(t.Context(), actor, book)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range cats {
			if c.ID == child.ID && !c.Archived {
				t.Fatal("child still active")
			}
		}
	})
	t.Run("preset translation survives archival and custom rename replaces it", func(t *testing.T) {
		id := uuid.MustParse(byCode["other_income"])
		archived, e := s.UpdateCategory(t.Context(), actor, book, id, uuid.NewString(), accounting.ReferencePatch{ExpectedRevision: 1, Name: "Other income", Archived: true})
		if e != nil || archived.NameZH != "其他收入" {
			t.Fatalf("archive lost preset translation: %+v, %v", archived, e)
		}
		cats, e := s.Categories(t.Context(), actor, book)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range cats {
			if c.ID == id.String() && c.NameZH != "其他收入" {
				t.Fatal("stored preset translation lost")
			}
		}
		renamed, e := s.UpdateCategory(t.Context(), actor, book, id, uuid.NewString(), accounting.ReferencePatch{ExpectedRevision: 2, Name: "Unexpected gifts"})
		if e != nil || renamed.NameZH != "" || renamed.Name != "Unexpected gifts" {
			t.Fatalf("custom name did not replace translation: %+v, %v", renamed, e)
		}
	})
	t.Run("tenant and book ownership apply to reads writes and retries", func(t *testing.T) {
		other, _, e := identities.Bootstrap(t.Context(), auth.Identity{Issuer: "test", Subject: "other-accounting"}, identity.BootstrapInput{BaseCurrency: "USD", Timezone: "UTC", Locale: "en"})
		if e != nil {
			t.Fatal(e)
		}
		_, e = s.Accounts(t.Context(), other, book)
		expectKind(t, e, problem.NotFound)
		_, e = s.CreateTransaction(t.Context(), other, other.DefaultBook.ID, uuid.NewString(), input)
		expectKind(t, e, problem.NotFound)
		fake := actor
		fake.User.Status = "active"
		execute(t, db, "UPDATE tenant_members SET status='disabled' WHERE tenant_id=$1", actor.Tenant.ID)
		_, e = s.CreateTransaction(t.Context(), fake, book, key, input)
		expectKind(t, e, problem.AccessDenied)
		execute(t, db, "UPDATE tenant_members SET status='active' WHERE tenant_id=$1", actor.Tenant.ID)
	})
	t.Run("database rejects sealed edits and invalid journals", func(t *testing.T) {
		for _, sql := range []string{"UPDATE postings SET amount=amount WHERE account_id='" + usd.ID + "'", "DELETE FROM postings WHERE account_id='" + usd.ID + "'", "UPDATE transaction_revisions SET amount=amount", "UPDATE journal_entries SET sealed=false"} {
			if _, e := db.Pool.Exec(t.Context(), sql); e == nil {
				t.Fatal("immutable write accepted", sql)
			}
		}
		for _, mode := range []string{"empty", "single", "unbalanced", "precision", "cross-book", "append", "nan", "infinity", "zero", "over-limit"} {
			t.Run(mode, func(t *testing.T) {
				e := pgx.BeginFunc(t.Context(), db.Pool, func(tx pgx.Tx) error {
					var journal string
					if err := tx.QueryRow(t.Context(), "SELECT id::text FROM journal_entries WHERE reversal_of IS NULL LIMIT 1").Scan(&journal); err != nil {
						return err
					}
					if mode == "append" {
						_, err := tx.Exec(t.Context(), "INSERT INTO postings SELECT gen_random_uuid(),tenant_id,book_id,journal_id,account_id,amount,valuation_amount,rate_numerator,rate_denominator,rate_source,rate_date FROM postings WHERE journal_id=$1 LIMIT 1", journal)
						return err
					}
					newTxn, newJournal := uuid.NewString(), uuid.NewString()
					if _, err := tx.Exec(t.Context(), "INSERT INTO transactions(id,tenant_id,book_id,revision,status,created_by) VALUES($1,$2,$3,1,'posted',$4)", newTxn, actor.Tenant.ID, book, actor.User.ID); err != nil {
						return err
					}
					if _, err := tx.Exec(t.Context(), "INSERT INTO transaction_revisions SELECT tenant_id,book_id,$1,1,kind,occurred_on,account_id,to_account_id,category_id,counterparty_id,amount,to_amount,original_id,data,false,actor_id,created_at FROM transaction_revisions WHERE transaction_id=$2 AND revision=1", newTxn, original.ID); err != nil {
						return err
					}
					if _, err := tx.Exec(t.Context(), "INSERT INTO journal_entries(id,tenant_id,book_id,transaction_id,revision,occurred_on,valuation_currency) VALUES($1,$2,$3,$4,1,'2026-09-11','CNY')", newJournal, actor.Tenant.ID, book, newTxn); err != nil {
						return err
					}
					amount := "100"
					switch mode {
					case "nan":
						amount = "NaN"
					case "infinity":
						amount = "Infinity"
					case "zero":
						amount = "0"
					case "over-limit":
						amount = "1000000000000000000"
					}
					if mode == "precision" {
						amount = "100.001"
					}
					if mode != "empty" {
						if _, err := tx.Exec(t.Context(), "INSERT INTO postings VALUES(gen_random_uuid(),$1,$2,$3,$4,$5,$5,1,1,'identity','2026-09-11')", actor.Tenant.ID, book, newJournal, cny.ID, amount); err != nil {
							return err
						}
					}
					if mode == "unbalanced" {
						if _, err := tx.Exec(t.Context(), "INSERT INTO postings VALUES(gen_random_uuid(),$1,$2,$3,$4,1,1,1,1,'identity','2026-09-11')", actor.Tenant.ID, book, newJournal, cny.ID); err != nil {
							return err
						}
					}
					if mode == "cross-book" {
						if _, err := tx.Exec(t.Context(), "INSERT INTO postings VALUES(gen_random_uuid(),$1,$2,$3,$4,-100,-100,1,1,'identity','2026-09-11')", actor.Tenant.ID, uuid.New(), newJournal, cny.ID); err != nil {
							return err
						}
					}
					_, err := tx.Exec(t.Context(), "UPDATE journal_entries SET sealed=true WHERE id=$1", newJournal)
					return err
				})
				if e == nil {
					t.Fatal("invalid journal committed")
				}
			})
		}
	})
	t.Run("all accounting HTTP response types match the contract", func(t *testing.T) {
		r, e := httpserver.New(db, httpserver.Dependencies{Backend: identities, Accounting: s, Verifier: localVerifier{}})
		if e != nil {
			t.Fatal(e)
		}
		base := "/api/v1/books/" + book.String()
		for _, path := range []string{"/api/v1/currencies", base + "/accounts", base + "/accounts/" + usd.ID, base + "/categories", "/api/v1/counterparties", base + "/transactions?limit=1", base + "/transactions/" + original.ID, base + "/summary", base + "/transaction-links"} {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer accounting")
			req.Header.Set("X-Ledger-API-Version", apicontract.Version())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			contracttest.Response(t, req, w)
			if path == "/api/v1/currencies" && (strings.Contains(w.Body.String(), "name_en") || strings.Contains(w.Body.String(), "name_zh")) {
				t.Fatal("currency API leaked locale-specific fields")
			}
		}
		data, e := json.Marshal(accounting.CreateInput{Entry: accounting.EntryInput{Kind: "income", OccurredOn: "2026-09-15", AccountID: cny.ID, Amount: "10", CategoryID: byCode["salary"]}})
		if e != nil {
			t.Fatal(e)
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, base+"/transactions", strings.NewReader(string(data)))
		req.Header.Set("Authorization", "Bearer accounting")
		req.Header.Set("X-Ledger-API-Version", apicontract.Version())
		req.Header.Set("Idempotency-Key", uuid.NewString())
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body)
		}
		contracttest.Response(t, req, w)
	})
}
