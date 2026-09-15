package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zeithrold/ledger/internal/database/sqlgen"
	"github.com/zeithrold/ledger/internal/identity"
)

type prepared struct {
	input       EntryInput
	date        pgtype.Date
	account     sqlgen.Account
	other       sqlgen.Account
	amount      Money
	otherAmount Money
}

func transaction(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, id string) (Transaction, error) {
	var out Transaction
	idValue, err := optID(id)
	if err != nil {
		return out, err
	}
	data, err := q.AccountingTransaction(ctx, sqlgen.AccountingTransactionParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: idValue})
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(data, &out)
	return out, err
}

func getAccount(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, id string, allowArchived bool) (sqlgen.Account, error) {
	key, err := optID(id)
	if err != nil {
		return sqlgen.Account{}, err
	}
	a, err := q.AccountingAccount(ctx, sqlgen.AccountingAccountParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: key})
	if err != nil {
		return a, err
	}
	if a.Role != "asset" || (a.Archived && !allowArchived) {
		return a, invalid("account_id", "Choose an active asset account.")
	}
	return a, nil
}

func systemAccount(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, currency, categoryID, role string) (sqlgen.Account, error) {
	category, err := optID(categoryID)
	if err != nil {
		return sqlgen.Account{}, err
	}
	code := txt("")
	if role == "equity" {
		code = txt("opening")
	}
	a, err := q.SystemAccountingAccount(ctx, sqlgen.SystemAccountingAccountParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), Currency: currency, CategoryID: category, SystemCode: code})
	if err == nil {
		return a, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return a, err
	}
	return q.InsertAccountingAccount(ctx, sqlgen.InsertAccountingAccountParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), BookID: pid(book), Name: role, Role: role, Kind: "system", Currency: currency, CategoryID: category, SystemCode: code})
}

func prepare(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, in EntryInput, self string, allowArchived bool) (prepared, error) {
	var p prepared
	if in.Kind != "opening" && in.Kind != "income" && in.Kind != "expense" && in.Kind != "transfer" && in.Kind != "refund" {
		return p, invalid("kind", "Choose a supported transaction type.")
	}
	d, err := day(in.OccurredOn)
	if err != nil {
		return p, err
	}
	if !d.Valid {
		return p, invalid("occurred_on", "Provide the business date.")
	}
	if utf8.RuneCountInString(in.Note) > 1000 {
		return p, invalid("note", "Use at most 1000 characters.")
	}
	in.Note = strings.TrimSpace(in.Note)
	a, err := getAccount(ctx, q, actor, book, in.AccountID, allowArchived)
	if err != nil {
		return p, err
	}
	m, err := ParseMoney(in.Amount, a.Currency)
	if err != nil {
		return p, invalid("amount", err.Error())
	}
	if m.units.Sign() == 0 || (m.units.Sign() < 0 && in.Kind != "opening") {
		return p, invalid("amount", "Provide a positive nonzero amount.")
	}
	in.Amount = m.String()
	if in.CounterpartyID != "" {
		id, e := optID(in.CounterpartyID)
		if e != nil {
			return p, e
		}
		c, e := q.AccountingCounterparty(ctx, sqlgen.AccountingCounterpartyParams{TenantID: pid(actor.Tenant.ID), ID: id})
		if e != nil {
			return p, e
		}
		if c.Archived && !allowArchived {
			return p, invalid("counterparty_id", "Choose an active counterparty.")
		}
	}
	if in.Kind != "transfer" && (in.ToAccountID != "" || in.ToAmount != "") {
		return p, invalid("to_account_id", "Destination fields are only valid for transfers.")
	}
	if in.Kind != "refund" && in.OriginalID != "" {
		return p, invalid("original_id", "Only refunds reference an original expense.")
	}
	if in.Kind == "refund" {
		original, e := transaction(ctx, q, actor, book, in.OriginalID)
		if e != nil {
			return p, e
		}
		if original.Status != "posted" || original.Data.Kind != "expense" {
			return p, invalid("original_id", "Refund an active expense.")
		}
		oa, e := getAccount(ctx, q, actor, book, original.Data.AccountID, true)
		if e != nil {
			return p, e
		}
		if oa.Currency != a.Currency {
			return p, invalid("account_id", "Refund into an account in the original currency.")
		}
		if in.CategoryID != "" && in.CategoryID != original.Data.CategoryID {
			return p, invalid("category_id", "Refunds retain the original expense category.")
		}
		in.CategoryID = original.Data.CategoryID
		originalID, e := optID(in.OriginalID)
		if e != nil {
			return p, e
		}
		exclude := pid(uuid.Nil)
		if self != "" {
			exclude, e = optID(self)
			if e != nil {
				return p, e
			}
		}
		refunded, e := q.AccountingRefunded(ctx, sqlgen.AccountingRefundedParams{SourceID: originalID, ID: exclude})
		if e != nil {
			return p, e
		}
		used, e := ParseMoney(refunded, a.Currency)
		if e != nil {
			return p, e
		}
		limit, e := ParseMoney(original.Data.Amount, a.Currency)
		if e != nil {
			return p, e
		}
		if new(big.Int).Add(used.units, m.units).Cmp(limit.units) > 0 {
			return p, conflict("The refund exceeds the remaining expense amount.")
		}
	}
	var other sqlgen.Account
	otherAmount := m
	switch in.Kind {
	case "transfer":
		if in.CategoryID != "" {
			return p, invalid("category_id", "Transfers have no income or expense category.")
		}
		other, err = getAccount(ctx, q, actor, book, in.ToAccountID, allowArchived)
		if err != nil {
			return p, err
		}
		if other.ID == a.ID {
			return p, invalid("to_account_id", "Choose a different destination account.")
		}
		otherAmount, err = ParseMoney(in.ToAmount, other.Currency)
		if err != nil {
			return p, invalid("to_amount", err.Error())
		}
		if otherAmount.units.Sign() <= 0 {
			return p, invalid("to_amount", "Provide a positive amount.")
		}
		if other.Currency == a.Currency && otherAmount.units.Cmp(m.units) != 0 {
			return p, invalid("to_amount", "Same-currency transfer principal must match. Record fees separately.")
		}
		in.ToAmount = otherAmount.String()
	case "opening":
		if in.CategoryID != "" {
			return p, invalid("category_id", "Opening balances use the opening equity account.")
		}
		if self == "" {
			exists, e := q.AccountingOpeningExists(ctx, a.ID)
			if e != nil {
				return p, e
			}
			if exists {
				return p, conflict("This account already has an opening balance. Correct that transaction instead.")
			}
		}
		other, err = systemAccount(ctx, q, actor, book, a.Currency, "", "equity")
	default:
		id, e := optID(in.CategoryID)
		if e != nil {
			return p, e
		}
		c, e := q.AccountingCategory(ctx, sqlgen.AccountingCategoryParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: id})
		if e != nil {
			return p, e
		}
		role := in.Kind
		if role == "refund" {
			role = "expense"
		}
		if c.Kind != role || (c.Archived && !allowArchived && in.Kind != "refund") {
			return p, invalid("category_id", "Choose an active category matching the transaction type.")
		}
		other, err = systemAccount(ctx, q, actor, book, a.Currency, in.CategoryID, role)
	}
	if err != nil {
		return p, err
	}
	return prepared{in, d, a, other, m, otherAmount}, nil
}

func writeRevision(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, id string, revision int32, in EntryInput, voided bool) error {
	tid, err := optID(id)
	if err != nil {
		return err
	}
	account, err := optID(in.AccountID)
	if err != nil {
		return err
	}
	toAccount, err := optID(in.ToAccountID)
	if err != nil {
		return err
	}
	category, err := optID(in.CategoryID)
	if err != nil {
		return err
	}
	counterparty, err := optID(in.CounterpartyID)
	if err != nil {
		return err
	}
	original, err := optID(in.OriginalID)
	if err != nil {
		return err
	}
	d, err := day(in.OccurredOn)
	if err != nil {
		return err
	}
	amount, err := num(in.Amount)
	if err != nil {
		return err
	}
	toAmount, err := num(in.ToAmount)
	if err != nil {
		return err
	}
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return q.InsertAccountingRevision(ctx, sqlgen.InsertAccountingRevisionParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), TransactionID: tid, Revision: revision, Kind: in.Kind, OccurredOn: d, AccountID: account, ToAccountID: toAccount, CategoryID: category, CounterpartyID: counterparty, Amount: amount, ToAmount: toAmount, OriginalID: original, Data: data, Voided: voided, ActorID: pid(actor.User.ID)})
}

func signed(m Money, negative bool) Money {
	n := new(big.Int).Set(m.units)
	if negative {
		n.Neg(n)
	}
	return Money{n, m.scale}
}

func writeJournal(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, id string, revision int32, p prepared) error {
	tid, err := optID(id)
	if err != nil {
		return err
	}
	journal := pid(uuid.New())
	if err = q.InsertAccountingJournal(ctx, sqlgen.InsertAccountingJournalParams{ID: journal, TenantID: pid(actor.Tenant.ID), BookID: pid(book), TransactionID: tid, Revision: revision, OccurredOn: p.date, ValuationCurrency: p.account.Currency}); err != nil {
		return err
	}
	assetNegative := p.input.Kind == "expense" || p.input.Kind == "transfer"
	amounts := []Money{signed(p.amount, assetNegative), signed(p.otherAmount, !assetNegative)}
	values := []Money{signed(p.amount, assetNegative), signed(p.amount, !assetNegative)}
	accounts := []sqlgen.Account{p.account, p.other}
	for i, a := range accounts {
		r := new(big.Rat).Quo(values[i].rat(), amounts[i].rat())
		n, e := num(r.Num().String())
		if e != nil {
			return e
		}
		d, e := num(r.Denom().String())
		if e != nil {
			return e
		}
		quantity, e := num(amounts[i].String())
		if e != nil {
			return e
		}
		value, e := num(values[i].String())
		if e != nil {
			return e
		}
		source := "identity"
		if a.Currency != p.account.Currency {
			source = "manual_actual"
		}
		if e = q.InsertAccountingPosting(ctx, sqlgen.InsertAccountingPostingParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), BookID: pid(book), JournalID: journal, AccountID: a.ID, Amount: quantity, ValuationAmount: value, RateNumerator: n, RateDenominator: d, RateSource: source, RateDate: p.date}); e != nil {
			return e
		}
	}
	return q.SealAccountingJournal(ctx, journal)
}

func (s *Service) createEntry(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, in EntryInput) (Transaction, error) {
	p, err := prepare(ctx, q, actor, book, in, "", false)
	if err != nil {
		return Transaction{}, err
	}
	id := uuid.New()
	if err = q.InsertAccountingTransaction(ctx, sqlgen.InsertAccountingTransactionParams{ID: pid(id), TenantID: pid(actor.Tenant.ID), BookID: pid(book), CreatedBy: pid(actor.User.ID)}); err != nil {
		return Transaction{}, err
	}
	if err = writeRevision(ctx, q, actor, book, id.String(), 1, p.input, false); err != nil {
		return Transaction{}, err
	}
	if err = writeJournal(ctx, q, actor, book, id.String(), 1, p); err != nil {
		return Transaction{}, err
	}
	if p.input.Kind == "refund" {
		if _, err = insertLink(ctx, q, actor, book, LinkInput{SourceID: p.input.OriginalID, TargetID: id.String(), Kind: "refund"}); err != nil {
			return Transaction{}, err
		}
	}
	out, err := transaction(ctx, q, actor, book, id.String())
	if err != nil {
		return out, err
	}
	return out, audit(ctx, q, actor, book, "transaction.create", pid(id), nil, out)
}

func insertLink(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, in LinkInput) (Link, error) {
	if in.SourceID == in.TargetID {
		return Link{}, invalid("target_id", "A transaction cannot link to itself.")
	}
	if in.Kind == "related" && in.SourceID > in.TargetID {
		in.SourceID, in.TargetID = in.TargetID, in.SourceID
	}
	source, err := optID(in.SourceID)
	if err != nil {
		return Link{}, err
	}
	target, err := optID(in.TargetID)
	if err != nil {
		return Link{}, err
	}
	l, err := q.InsertAccountingLink(ctx, sqlgen.InsertAccountingLinkParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), BookID: pid(book), SourceID: source, TargetID: target, Kind: in.Kind, CreatedBy: pid(actor.User.ID)})
	if err != nil {
		return Link{}, err
	}
	out := linkDTO(l)
	return out, audit(ctx, q, actor, book, "link.create", l.ID, nil, out)
}

func validateFee(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, mainID, feeID string) error {
	main, err := transaction(ctx, q, actor, book, mainID)
	if err != nil {
		return err
	}
	fee, err := transaction(ctx, q, actor, book, feeID)
	if err != nil {
		return err
	}
	if main.Status != "posted" || fee.Status != "posted" || fee.Data.Kind != "expense" {
		return invalid("target_id", "A fee must be an active expense linked to an active transaction.")
	}
	links, err := q.AccountingLinks(ctx, sqlgen.AccountingLinksParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book)})
	if err != nil {
		return err
	}
	for _, l := range links {
		if l.Kind == "fee" && (sid(l.TargetID) == mainID || sid(l.SourceID) == feeID) {
			return invalid("source_id", "Fee relations cannot form nested chains.")
		}
	}
	return nil
}

// CreateTransaction creates principal and optional independent fee atomically.
func (s *Service) CreateTransaction(ctx context.Context, actor identity.Context, book uuid.UUID, key string, in CreateInput) (MutationResult, error) {
	return mutation(ctx, s, actor, book, key, "transaction.create", in, func(q *sqlgen.Queries) (MutationResult, error) {
		out := MutationResult{Transactions: []Transaction{}, Links: []Link{}}
		if in.FeeForID != "" && in.Fee != nil {
			return out, invalid("fee", "A fee cannot have a nested fee.")
		}
		t, err := s.createEntry(ctx, q, actor, book, in.Entry)
		if err != nil {
			return out, err
		}
		out.Transactions = append(out.Transactions, t)
		if in.FeeForID != "" {
			if err = validateFee(ctx, q, actor, book, in.FeeForID, t.ID); err != nil {
				return out, err
			}
			l, e := insertLink(ctx, q, actor, book, LinkInput{SourceID: in.FeeForID, TargetID: t.ID, Kind: "fee"})
			if e != nil {
				return out, e
			}
			out.Links = append(out.Links, l)
		}
		if in.Fee != nil {
			if in.Fee.Kind != "expense" {
				return out, invalid("fee", "Record fees as a separate expense.")
			}
			fee, e := s.createEntry(ctx, q, actor, book, *in.Fee)
			if e != nil {
				return out, e
			}
			l, e := insertLink(ctx, q, actor, book, LinkInput{SourceID: t.ID, TargetID: fee.ID, Kind: "fee"})
			if e != nil {
				return out, e
			}
			out.Transactions = append(out.Transactions, fee)
			out.Links = append(out.Links, l)
		}
		if t.Data.Kind == "refund" {
			links, e := q.AccountingLinks(ctx, sqlgen.AccountingLinksParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), Column3: pid(uuid.MustParse(t.ID))})
			if e != nil {
				return out, e
			}
			for _, l := range links {
				if l.Kind == "refund" {
					out.Links = append(out.Links, linkDTO(l))
				}
			}
		}
		return out, nil
	})
}

func negate(n pgtype.Numeric) pgtype.Numeric { n.Int = new(big.Int).Neg(n.Int); return n }

func reverseJournal(ctx context.Context, q *sqlgen.Queries, old Transaction, revision int32) error {
	id, err := optID(old.ID)
	if err != nil {
		return err
	}
	j, err := q.CurrentAccountingJournal(ctx, sqlgen.CurrentAccountingJournalParams{TransactionID: id, Revision: old.Revision})
	if err != nil {
		return err
	}
	newID := pid(uuid.New())
	if err = q.InsertAccountingJournal(ctx, sqlgen.InsertAccountingJournalParams{ID: newID, TenantID: j.TenantID, BookID: j.BookID, TransactionID: j.TransactionID, Revision: revision, OccurredOn: j.OccurredOn, ValuationCurrency: j.ValuationCurrency, ReversalOf: j.ID}); err != nil {
		return err
	}
	rows, err := q.AccountingPostings(ctx, j.ID)
	if err != nil {
		return err
	}
	for _, p := range rows {
		if err = q.InsertAccountingPosting(ctx, sqlgen.InsertAccountingPostingParams{ID: pid(uuid.New()), TenantID: p.TenantID, BookID: p.BookID, JournalID: newID, AccountID: p.AccountID, Amount: negate(p.Amount), ValuationAmount: negate(p.ValuationAmount), RateNumerator: p.RateNumerator, RateDenominator: p.RateDenominator, RateSource: p.RateSource, RateDate: p.RateDate}); err != nil {
			return err
		}
	}
	return q.SealAccountingJournal(ctx, newID)
}

func (s *Service) correctEntry(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, c Correction) (Transaction, error) {
	old, err := transaction(ctx, q, actor, book, c.ID)
	if err != nil {
		return old, err
	}
	if old.Status != "posted" || old.Revision != c.ExpectedRevision {
		return old, conflict("This transaction changed or was deleted. Reload before editing.")
	}
	input := old.Data
	if c.Entry != nil {
		input = *c.Entry
	}
	if input.Kind != old.Data.Kind || input.OriginalID != old.Data.OriginalID {
		return old, invalid("kind", "Corrections retain the transaction type and original refund relationship.")
	}
	if input.Kind == "opening" && input.AccountID != old.Data.AccountID {
		return old, invalid("account_id", "An opening balance remains attached to its original account.")
	}
	a, b := input, old.Data
	a.Note = ""
	b.Note = ""
	memoOnly := c.Entry != nil && a == b
	if old.Data.Kind == "expense" && !memoOnly {
		refunded, e := q.AccountingRefunded(ctx, sqlgen.AccountingRefundedParams{SourceID: pid(uuid.MustParse(old.ID)), ID: pid(uuid.Nil)})
		if e != nil {
			return old, e
		}
		r, ok := new(big.Rat).SetString(refunded)
		if !ok {
			return old, errors.New("invalid stored refund total")
		}
		if r.Sign() != 0 {
			return old, conflict("Handle the existing refunds before changing or deleting this expense.")
		}
	}
	var p prepared
	if c.Entry != nil {
		p, err = prepare(ctx, q, actor, book, input, old.ID, memoOnly)
		if err != nil {
			return old, err
		}
		input = p.input
	}
	next := old.Revision + 1
	if next <= 0 {
		return old, conflict("The revision limit was reached.")
	}
	if err = writeRevision(ctx, q, actor, book, old.ID, next, input, c.Entry == nil); err != nil {
		return old, err
	}
	if err = reverseJournal(ctx, q, old, next); err != nil {
		return old, err
	}
	status := "void"
	if c.Entry != nil {
		if err = writeJournal(ctx, q, actor, book, old.ID, next, p); err != nil {
			return old, err
		}
		status = "posted"
	}
	if err = q.ChangeAccountingTransaction(ctx, sqlgen.ChangeAccountingTransactionParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: pid(uuid.MustParse(old.ID)), Revision: next, Status: status}); err != nil {
		return old, err
	}
	out, err := transaction(ctx, q, actor, book, old.ID)
	if err != nil {
		return out, err
	}
	return out, audit(ctx, q, actor, book, "transaction.correct", pid(uuid.MustParse(old.ID)), old, out)
}

// CorrectTransaction applies only the requested principal and selected fee revisions.
func (s *Service) CorrectTransaction(ctx context.Context, actor identity.Context, book, id uuid.UUID, key string, in CorrectionInput) (MutationResult, error) {
	return mutation(ctx, s, actor, book, key, "transaction.correct/"+id.String(), in, func(q *sqlgen.Queries) (MutationResult, error) {
		out := MutationResult{Transactions: []Transaction{}, Links: []Link{}}
		if len(in.Fees) > 20 {
			return out, invalid("fees", "Select at most 20 related fees.")
		}
		links, err := q.AccountingLinks(ctx, sqlgen.AccountingLinksParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), Column3: pid(id)})
		if err != nil {
			return out, err
		}
		allowed := map[string]bool{}
		for _, l := range links {
			out.Links = append(out.Links, linkDTO(l))
			if l.Kind == "fee" && l.SourceID == pid(id) {
				allowed[sid(l.TargetID)] = true
			}
		}
		changes := []Correction{{ID: id.String(), ExpectedRevision: in.ExpectedRevision, Entry: in.Entry}}
		for _, fee := range in.Fees {
			if !allowed[fee.ID] {
				return out, invalid("fees", "Select each directly related fee at most once.")
			}
			delete(allowed, fee.ID)
			if in.Entry == nil && fee.Entry != nil {
				return out, invalid("fees", "Deleting the principal may only delete selected fees.")
			}
			changes = append(changes, fee)
		}
		for _, change := range changes {
			t, e := s.correctEntry(ctx, q, actor, book, change)
			if e != nil {
				return out, e
			}
			out.Transactions = append(out.Transactions, t)
		}
		return out, nil
	})
}

// CreateLink creates ordinary and fee links; refund links belong to refund posting.
func (s *Service) CreateLink(ctx context.Context, actor identity.Context, book uuid.UUID, key string, in LinkInput) (Link, error) {
	return mutation(ctx, s, actor, book, key, "link.create", in, func(q *sqlgen.Queries) (Link, error) {
		if in.Kind != "related" && in.Kind != "fee" {
			return Link{}, invalid("kind", "Choose related or fee. Refund links are created by refund transactions.")
		}
		for _, id := range []string{in.SourceID, in.TargetID} {
			t, e := transaction(ctx, q, actor, book, id)
			if e != nil {
				return Link{}, e
			}
			if in.Kind == "fee" && t.Status != "posted" {
				return Link{}, invalid("transaction_id", "Link active transactions for fees.")
			}
		}
		if in.Kind == "fee" {
			if err := validateFee(ctx, q, actor, book, in.SourceID, in.TargetID); err != nil {
				return Link{}, err
			}
		}
		return insertLink(ctx, q, actor, book, in)
	})
}

// RemoveLink only removes an ordinary association; accounting relationships remain auditable.
func (s *Service) RemoveLink(ctx context.Context, actor identity.Context, book, id uuid.UUID, key string) (Link, error) {
	return mutation(ctx, s, actor, book, key, "link.remove/"+id.String(), nil, func(q *sqlgen.Queries) (Link, error) {
		l, err := q.AccountingLink(ctx, sqlgen.AccountingLinkParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: pid(id)})
		if err != nil {
			return Link{}, err
		}
		if l.Kind != "related" {
			return Link{}, invalid("kind", "Only ordinary associations can be removed.")
		}
		if err = q.RemoveAccountingLink(ctx, sqlgen.RemoveAccountingLinkParams{TenantID: l.TenantID, BookID: l.BookID, ID: l.ID}); err != nil {
			return Link{}, err
		}
		out := linkDTO(l)
		return out, audit(ctx, q, actor, book, "link.remove", l.ID, out, nil)
	})
}
