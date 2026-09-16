package accounting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/zeithrold/ledger/internal/database"
	"github.com/zeithrold/ledger/internal/database/sqlgen"
	"github.com/zeithrold/ledger/internal/identity"
	"github.com/zeithrold/ledger/internal/problem"
)

// Service keeps all database changes behind one authorized, idempotent boundary.
type Service struct{ db *database.DB }

// New constructs the accounting service without performing database writes.
func New(db *database.DB) *Service { return &Service{db: db} }

func pid(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
func sid(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func optID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, invalid("id", "Provide a valid nonzero UUID.")
	}
	return pid(id), nil
}
func txt(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }
func num(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if s == "" {
		return n, nil
	}
	err := n.Scan(s)
	return n, err
}

func day(s string) (pgtype.Date, error) {
	if s == "" {
		return pgtype.Date{}, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Format(time.DateOnly) != s {
		return pgtype.Date{}, invalid("occurred_on", "Provide a YYYY-MM-DD date.")
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

func invalid(field, reason string) error {
	return &problem.Error{Kind: problem.InvalidRequest, Detail: "Accounting parameters are invalid.", Fields: []problem.FieldError{{Location: "body", Name: field, Reason: reason}}}
}
func conflict(message string) error { return problem.New(problem.Conflict, message) }
func mapped(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return problem.New(problem.NotFound, "The accounting resource was not found.")
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return conflict("The record or relation already exists.")
		case "23503", "23514", "23502", "22003":
			return invalid("entry", "The accounting relationship, precision or balance is invalid.")
		case "40001", "40P01":
			return conflict("A concurrent change occurred. Retry the same operation.")
		}
	}
	return err
}

func authorize(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID) error {
	_, err := q.AuthorizeAccounting(ctx, sqlgen.AuthorizeAccountingParams{ID: pid(actor.User.ID), TenantID: pid(actor.Tenant.ID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return problem.New(problem.AccessDenied, "The personal space is no longer available.")
	}
	if err != nil {
		return err
	}
	if book != uuid.Nil {
		_, err = q.GetBook(ctx, sqlgen.GetBookParams{TenantID: pid(actor.Tenant.ID), ID: pid(book)})
	}
	return mapped(err)
}

func mutation[T any](ctx context.Context, s *Service, actor identity.Context, book uuid.UUID, key, operation string, input any, fn func(*sqlgen.Queries) (T, error)) (T, error) {
	var result T
	id, err := uuid.Parse(key)
	if err != nil || id == uuid.Nil {
		return result, invalid("idempotency_key", "Provide an Idempotency-Key UUID.")
	}
	encoded, err := json.Marshal(struct {
		Operation string
		Book      uuid.UUID
		Input     any
	}{operation, book, input})
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256(encoded)
	fingerprint := hex.EncodeToString(hash[:])
	err = pgx.BeginFunc(ctx, s.db.Pool, func(tx pgx.Tx) error {
		q := s.db.Queries.WithTx(tx)
		// Tenant serialization also protects tenant-wide idempotency keys across books.
		if _, e := q.LockAccountingTenant(ctx, pid(actor.Tenant.ID)); e != nil {
			return e
		}
		if e := authorize(ctx, q, actor, book); e != nil {
			return e
		}
		if book != uuid.Nil {
			if _, e := q.LockAccountingBook(ctx, sqlgen.LockAccountingBookParams{TenantID: pid(actor.Tenant.ID), ID: pid(book)}); e != nil {
				return e
			}
		}
		old, e := q.ReadAccountingOperation(ctx, sqlgen.ReadAccountingOperationParams{TenantID: pid(actor.Tenant.ID), Key: pid(id)})
		if e == nil {
			if old.RequestHash != fingerprint {
				return conflict("The idempotency key was already used with different content.")
			}
			return json.Unmarshal(old.Response, &result)
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		result, e = fn(q)
		if e != nil {
			return e
		}
		response, e := json.Marshal(result)
		if e != nil {
			return e
		}
		return q.SaveAccountingOperation(ctx, sqlgen.SaveAccountingOperationParams{TenantID: pid(actor.Tenant.ID), Key: pid(id), RequestHash: fingerprint, Response: response})
	})
	return result, mapped(err)
}

func audit(ctx context.Context, q *sqlgen.Queries, actor identity.Context, book uuid.UUID, action string, id pgtype.UUID, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	bid := pgtype.UUID{}
	if book != uuid.Nil {
		bid = pid(book)
	}
	return q.AccountingAudit(ctx, sqlgen.AccountingAuditParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), BookID: bid, ActorID: pid(actor.User.ID), Action: action, ResourceID: id, BeforeData: b, AfterData: a})
}

func name(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" || utf8.RuneCountInString(v) > 120 {
		return "", invalid("name", "Provide a name of 1 to 120 UTF-8 bytes.")
	}
	return v, nil
}

func accountDTO(a sqlgen.Account, balance string) Account {
	return Account{sid(a.ID), a.Name, a.Kind, a.Currency, a.Archived, a.Revision, balance}
}

func categoryDTO(c sqlgen.Category) Category {
	return Category{sid(c.ID), sid(c.ParentID), c.Kind, c.Name, c.NameZh, c.SystemCode.String, c.Archived, c.Revision}
}

func counterpartyDTO(c sqlgen.Counterparty) Counterparty {
	return Counterparty{sid(c.ID), c.Name, c.Archived, c.Revision}
}

func linkDTO(l sqlgen.TransactionLink) Link {
	return Link{sid(l.ID), sid(l.SourceID), sid(l.TargetID), l.Kind}
}

// Accounts returns asset accounts with balances derived from all original and reversal postings.
func (s *Service) Accounts(ctx context.Context, actor identity.Context, book uuid.UUID) ([]Account, error) {
	if err := authorize(ctx, s.db.Queries, actor, book); err != nil {
		return nil, err
	}
	rows, err := s.db.Queries.AccountingAccounts(ctx, sqlgen.AccountingAccountsParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book)})
	result := []Account{}
	for _, a := range rows {
		result = append(result, Account{sid(a.ID), a.Name, a.Kind, a.Currency, a.Archived, a.Revision, a.Balance})
	}
	return result, mapped(err)
}

// CreateAccount optionally posts the opening balance in the same transaction.
func (s *Service) CreateAccount(ctx context.Context, actor identity.Context, book uuid.UUID, key string, in AccountInput) (Account, error) {
	return mutation(ctx, s, actor, book, key, "account.create", in, func(q *sqlgen.Queries) (Account, error) {
		n, err := name(in.Name)
		if err != nil {
			return Account{}, err
		}
		if _, ok := currencyUnits[in.Currency]; !ok {
			return Account{}, invalid("currency", "Choose a supported currency.")
		}
		if in.Kind != "cash" && in.Kind != "bank" && in.Kind != "wallet" && in.Kind != "other" {
			return Account{}, invalid("kind", "Choose cash, bank, wallet or other.")
		}
		a, err := q.InsertAccountingAccount(ctx, sqlgen.InsertAccountingAccountParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), BookID: pid(book), Name: n, Role: "asset", Kind: in.Kind, Currency: in.Currency})
		if err != nil {
			return Account{}, err
		}
		balance := "0"
		if in.OpeningAmount != "" {
			m, e := ParseMoney(in.OpeningAmount, in.Currency)
			if e != nil {
				return Account{}, invalid("opening_amount", e.Error())
			}
			balance = m.String()
			if m.Sign() != 0 {
				if _, e = s.createEntry(ctx, q, actor, book, EntryInput{Kind: "opening", OccurredOn: in.OpeningDate, AccountID: sid(a.ID), Amount: balance}); e != nil {
					return Account{}, e
				}
			}
		}
		out := accountDTO(a, balance)
		return out, audit(ctx, q, actor, book, "account.create", a.ID, nil, out)
	})
}

// UpdateAccount changes only display name and archival state.
func (s *Service) UpdateAccount(ctx context.Context, actor identity.Context, book, id uuid.UUID, key string, in ReferencePatch) (Account, error) {
	return mutation(ctx, s, actor, book, key, "account.update/"+id.String(), in, func(q *sqlgen.Queries) (Account, error) {
		a, err := q.AccountingAccount(ctx, sqlgen.AccountingAccountParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: pid(id)})
		if err != nil {
			return Account{}, err
		}
		if a.Role != "asset" {
			return Account{}, invalid("account_id", "Choose an asset account.")
		}
		if a.Revision != in.ExpectedRevision {
			return Account{}, conflict("The account has changed. Reload before editing.")
		}
		n, err := name(in.Name)
		if err != nil {
			return Account{}, err
		}
		if err = q.ChangeAccountingAccount(ctx, sqlgen.ChangeAccountingAccountParams{TenantID: a.TenantID, BookID: a.BookID, ID: a.ID, Name: n, Archived: in.Archived}); err != nil {
			return Account{}, err
		}
		old := accountDTO(a, "0")
		a.Name = n
		a.Archived = in.Archived
		a.Revision++
		rows, err := q.AccountingAccounts(ctx, sqlgen.AccountingAccountsParams{TenantID: a.TenantID, BookID: a.BookID})
		if err != nil {
			return Account{}, err
		}
		balance := "0"
		for _, row := range rows {
			if row.ID == a.ID {
				balance = row.Balance
			}
		}
		out := accountDTO(a, balance)
		return out, audit(ctx, q, actor, book, "account.update", a.ID, old, out)
	})
}

// Categories returns both levels, including archived references needed by history.
func (s *Service) Categories(ctx context.Context, actor identity.Context, book uuid.UUID) ([]Category, error) {
	if err := authorize(ctx, s.db.Queries, actor, book); err != nil {
		return nil, err
	}
	rows, err := s.db.Queries.AccountingCategories(ctx, sqlgen.AccountingCategoriesParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book)})
	out := []Category{}
	for _, c := range rows {
		out = append(out, categoryDTO(c))
	}
	return out, mapped(err)
}

// CreateCategory adds one root or one child, with no mutable category hierarchy.
func (s *Service) CreateCategory(ctx context.Context, actor identity.Context, book uuid.UUID, key string, in CategoryInput) (Category, error) {
	return mutation(ctx, s, actor, book, key, "category.create", in, func(q *sqlgen.Queries) (Category, error) {
		n, err := name(in.Name)
		if err != nil {
			return Category{}, err
		}
		if in.Kind != "income" && in.Kind != "expense" {
			return Category{}, invalid("kind", "Choose income or expense.")
		}
		parent, err := optID(in.ParentID)
		if err != nil {
			return Category{}, err
		}
		if parent.Valid {
			p, e := q.AccountingCategory(ctx, sqlgen.AccountingCategoryParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: parent})
			if e != nil {
				return Category{}, e
			}
			if p.ParentID.Valid || p.Kind != in.Kind || p.Archived {
				return Category{}, invalid("parent_id", "Choose an active root of the same kind.")
			}
		}
		c, err := q.InsertAccountingCategory(ctx, sqlgen.InsertAccountingCategoryParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), BookID: pid(book), ParentID: parent, Kind: in.Kind, Name: n})
		if err != nil {
			return Category{}, err
		}
		out := categoryDTO(c)
		return out, audit(ctx, q, actor, book, "category.create", c.ID, nil, out)
	})
}

// UpdateCategory archives children together with their parent and preserves historical references.
func (s *Service) UpdateCategory(ctx context.Context, actor identity.Context, book, id uuid.UUID, key string, in ReferencePatch) (Category, error) {
	return mutation(ctx, s, actor, book, key, "category.update/"+id.String(), in, func(q *sqlgen.Queries) (Category, error) {
		c, err := q.AccountingCategory(ctx, sqlgen.AccountingCategoryParams{TenantID: pid(actor.Tenant.ID), BookID: pid(book), ID: pid(id)})
		if err != nil {
			return Category{}, err
		}
		if c.Revision != in.ExpectedRevision {
			return Category{}, conflict("The category has changed. Reload before editing.")
		}
		n, err := name(in.Name)
		if err != nil {
			return Category{}, err
		}
		if err = q.ChangeAccountingCategory(ctx, sqlgen.ChangeAccountingCategoryParams{TenantID: c.TenantID, BookID: c.BookID, ID: c.ID, Name: n, Archived: in.Archived}); err != nil {
			return Category{}, err
		}
		if in.Archived {
			if err = q.ArchiveAccountingChildren(ctx, sqlgen.ArchiveAccountingChildrenParams{TenantID: c.TenantID, BookID: c.BookID, ParentID: c.ID}); err != nil {
				return Category{}, err
			}
		}
		old := categoryDTO(c)
		if c.Name != n {
			c.NameZh = ""
		}
		c.Name = n
		c.Archived = in.Archived
		c.Revision++
		out := categoryDTO(c)
		return out, audit(ctx, q, actor, book, "category.update", c.ID, old, out)
	})
}

// Counterparties returns the active and archived tenant catalog.
func (s *Service) Counterparties(ctx context.Context, actor identity.Context) ([]Counterparty, error) {
	if err := authorize(ctx, s.db.Queries, actor, uuid.Nil); err != nil {
		return nil, err
	}
	rows, err := s.db.Queries.AccountingCounterparties(ctx, pid(actor.Tenant.ID))
	out := []Counterparty{}
	for _, c := range rows {
		out = append(out, counterpartyDTO(c))
	}
	return out, mapped(err)
}

// CreateCounterparty creates a reusable tenant-scoped merchant or person.
func (s *Service) CreateCounterparty(ctx context.Context, actor identity.Context, key string, in CounterpartyInput) (Counterparty, error) {
	return mutation(ctx, s, actor, uuid.Nil, key, "counterparty.create", in, func(q *sqlgen.Queries) (Counterparty, error) {
		n, err := name(in.Name)
		if err != nil {
			return Counterparty{}, err
		}
		c, err := q.InsertAccountingCounterparty(ctx, sqlgen.InsertAccountingCounterpartyParams{ID: pid(uuid.New()), TenantID: pid(actor.Tenant.ID), Name: n})
		if err != nil {
			return Counterparty{}, err
		}
		out := counterpartyDTO(c)
		return out, audit(ctx, q, actor, uuid.Nil, "counterparty.create", c.ID, nil, out)
	})
}

// UpdateCounterparty preserves identity and changes only its name and archival state.
func (s *Service) UpdateCounterparty(ctx context.Context, actor identity.Context, id uuid.UUID, key string, in ReferencePatch) (Counterparty, error) {
	return mutation(ctx, s, actor, uuid.Nil, key, "counterparty.update/"+id.String(), in, func(q *sqlgen.Queries) (Counterparty, error) {
		c, err := q.AccountingCounterparty(ctx, sqlgen.AccountingCounterpartyParams{TenantID: pid(actor.Tenant.ID), ID: pid(id)})
		if err != nil {
			return Counterparty{}, err
		}
		if c.Revision != in.ExpectedRevision {
			return Counterparty{}, conflict("The counterparty has changed. Reload before editing.")
		}
		n, err := name(in.Name)
		if err != nil {
			return Counterparty{}, err
		}
		if err = q.ChangeAccountingCounterparty(ctx, sqlgen.ChangeAccountingCounterpartyParams{TenantID: c.TenantID, ID: c.ID, Name: n, Archived: in.Archived}); err != nil {
			return Counterparty{}, err
		}
		old := counterpartyDTO(c)
		c.Name = n
		c.Archived = in.Archived
		c.Revision++
		out := counterpartyDTO(c)
		return out, audit(ctx, q, actor, uuid.Nil, "counterparty.update", c.ID, old, out)
	})
}
