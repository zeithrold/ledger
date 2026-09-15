package accounting

import "time"

// Account is a user-visible asset account and its derived original-currency balance.
type Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Archived bool   `json:"archived"`
	Revision int32  `json:"revision"`
	Balance  string `json:"balance"`
}

// AccountInput creates an account, optionally with an atomic opening entry.
type AccountInput struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Currency      string `json:"currency"`
	OpeningAmount string `json:"opening_amount,omitempty"`
	OpeningDate   string `json:"opening_date,omitempty"`
}

// ReferencePatch changes display fields without altering the identity of a resource.
type ReferencePatch struct {
	ExpectedRevision int32  `json:"expected_revision"`
	Name             string `json:"name"`
	Archived         bool   `json:"archived"`
}

// Category represents either level of the income/expense catalog.
type Category struct {
	ID         string `json:"id"`
	ParentID   string `json:"parent_id,omitempty"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	NameZH     string `json:"name_zh"`
	SystemCode string `json:"system_code,omitempty"`
	Archived   bool   `json:"archived"`
	Revision   int32  `json:"revision"`
}

// CategoryInput creates a root or one child of a root category.
type CategoryInput struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	ParentID string `json:"parent_id,omitempty"`
}

// Counterparty identifies an optional merchant or person within a tenant.
type Counterparty struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
	Revision int32  `json:"revision"`
}

// CounterpartyInput creates a tenant-scoped counterparty.
type CounterpartyInput struct {
	Name string `json:"name"`
}

// EntryInput captures business intent. The service, not the caller, builds postings.
type EntryInput struct {
	Kind           string `json:"kind"`
	OccurredOn     string `json:"occurred_on"`
	AccountID      string `json:"account_id"`
	Amount         string `json:"amount"`
	ToAccountID    string `json:"to_account_id,omitempty"`
	ToAmount       string `json:"to_amount,omitempty"`
	CategoryID     string `json:"category_id,omitempty"`
	CounterpartyID string `json:"counterparty_id,omitempty"`
	Note           string `json:"note,omitempty"`
	OriginalID     string `json:"original_id,omitempty"`
}

// CreateInput optionally creates one separate fee in the same database transaction.
type CreateInput struct {
	Entry    EntryInput  `json:"entry"`
	Fee      *EntryInput `json:"fee,omitempty"`
	FeeForID string      `json:"fee_for_id,omitempty"`
}

// Transaction is a stable business identity with a current immutable revision.
type Transaction struct {
	ID        string     `json:"id"`
	Revision  int32      `json:"revision"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	Data      EntryInput `json:"data"`
}

// Link connects two stable transaction identities, without changing balances.
type Link struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Kind     string `json:"kind"`
}

// LinkInput creates a symmetric ordinary relation or a directed fee relation.
type LinkInput struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Kind     string `json:"kind"`
}

// Correction changes one transaction; an absent entry means an explicit void.
type Correction struct {
	ID               string      `json:"id"`
	ExpectedRevision int32       `json:"expected_revision"`
	Entry            *EntryInput `json:"entry,omitempty"`
}

// CorrectionInput includes only the associated fees explicitly selected by the user.
type CorrectionInput struct {
	ExpectedRevision int32        `json:"expected_revision"`
	Entry            *EntryInput  `json:"entry,omitempty"`
	Fees             []Correction `json:"fees,omitempty"`
}

// MutationResult is persisted verbatim for safe idempotent retries.
type MutationResult struct {
	Transactions []Transaction `json:"transactions"`
	Links        []Link        `json:"links"`
}

// Revision preserves both the original entry and the time it was corrected.
type Revision struct {
	Revision  int32      `json:"revision"`
	Voided    bool       `json:"voided"`
	Data      EntryInput `json:"data"`
	ActorID   string     `json:"actor_id"`
	CreatedAt time.Time  `json:"created_at"`
}

// ExchangeRate keeps an exact ratio, with a separate rounded display value.
type ExchangeRate struct {
	FromCurrency string `json:"from_currency"`
	ToCurrency   string `json:"to_currency"`
	Numerator    string `json:"numerator"`
	Denominator  string `json:"denominator"`
	Display      string `json:"display"`
	Date         string `json:"date"`
	Source       string `json:"source"`
}

// PostingView exposes immutable original-currency and valuation amounts.
type PostingView struct {
	AccountID       string `json:"account_id"`
	Currency        string `json:"currency"`
	Amount          string `json:"amount"`
	ValuationAmount string `json:"valuation_amount"`
	RateNumerator   string `json:"rate_numerator"`
	RateDenominator string `json:"rate_denominator"`
	RateSource      string `json:"rate_source"`
	RateDate        string `json:"rate_date"`
}

// JournalView identifies a posting or an exact reversal and its effective date.
type JournalView struct {
	ID                string        `json:"id"`
	Revision          int32         `json:"revision"`
	OccurredOn        string        `json:"occurred_on"`
	ValuationCurrency string        `json:"valuation_currency"`
	ReversalOf        *string       `json:"reversal_of"`
	Postings          []PostingView `json:"postings"`
}

// Detail exposes related records and immutable correction history.
type Detail struct {
	Transaction      Transaction   `json:"transaction"`
	Links            []Link        `json:"links"`
	History          []Revision    `json:"history"`
	Journals         []JournalView `json:"journals"`
	RefundableAmount string        `json:"refundable_amount,omitempty"`
	ExchangeRate     *ExchangeRate `json:"exchange_rate,omitempty"`
}

// Filter uses inclusive business dates and an exclusive stable pagination cursor.
type Filter struct {
	From           string
	To             string
	AccountID      string
	CategoryID     string
	CounterpartyID string
	Kind           string
	Currency       string
	Cursor         string
	Limit          int32
	IncludeVoided  bool
}

// TransactionPage contains independently visible business transactions.
type TransactionPage struct {
	Transactions []Transaction `json:"transactions"`
	Links        []Link        `json:"links"`
	NextCursor   *string       `json:"next_cursor"`
}

// SummaryRow is a total in exactly one currency; category rows never mix currencies.
type SummaryRow struct {
	Currency   string `json:"currency"`
	Kind       string `json:"kind"`
	CategoryID string `json:"category_id,omitempty"`
	Amount     string `json:"amount"`
}

// Summary separates currency totals and category rollups to prevent double counting.
type Summary struct {
	Totals     []SummaryRow `json:"totals"`
	Categories []SummaryRow `json:"categories"`
}
