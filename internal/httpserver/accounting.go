package httpserver

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/zeithrold/ledger/internal/accounting"
	"github.com/zeithrold/ledger/internal/apiv1"
	"github.com/zeithrold/ledger/internal/problem"
)

func (a api) accountingReady(c *gin.Context) bool {
	if a.deps.Accounting == nil {
		problem.Write(c, problem.New(problem.Unavailable, "The accounting API is not configured."))
		return false
	}
	return true
}

func accountingReply(c *gin.Context, status int, value any, err error) {
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(status, value)
}

func (a api) ListCurrencies(c *gin.Context, _ apiv1.ListCurrenciesParams) {
	if !a.accountingReady(c) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"currencies": accounting.Currencies()})
}

func (a api) ListAccountingAccounts(c *gin.Context, book uuid.UUID, _ apiv1.ListAccountingAccountsParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.Accounts(c.Request.Context(), localActor(c), book)
	accountingReply(c, 200, gin.H{"accounts": v}, err)
}

func (a api) GetAccountingAccount(c *gin.Context, book, id uuid.UUID, _ apiv1.GetAccountingAccountParams) {
	if !a.accountingReady(c) {
		return
	}
	items, err := a.deps.Accounting.Accounts(c.Request.Context(), localActor(c), book)
	if err != nil {
		respondError(c, err)
		return
	}
	for _, v := range items {
		if v.ID == id.String() {
			c.JSON(200, v)
			return
		}
	}
	problem.Write(c, problem.New(problem.NotFound, "The account was not found."))
}

func (a api) CreateAccountingAccount(c *gin.Context, book uuid.UUID, p apiv1.CreateAccountingAccountParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.AccountInput
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.CreateAccount(c.Request.Context(), localActor(c), book, p.IdempotencyKey.String(), in)
	accountingReply(c, 201, v, err)
}

func (a api) UpdateAccountingAccount(c *gin.Context, book, id uuid.UUID, p apiv1.UpdateAccountingAccountParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.ReferencePatch
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.UpdateAccount(c.Request.Context(), localActor(c), book, id, p.IdempotencyKey.String(), in)
	accountingReply(c, 200, v, err)
}

func (a api) ListAccountingCategories(c *gin.Context, book uuid.UUID, _ apiv1.ListAccountingCategoriesParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.Categories(c.Request.Context(), localActor(c), book)
	accountingReply(c, 200, gin.H{"categories": v}, err)
}

func (a api) CreateAccountingCategory(c *gin.Context, book uuid.UUID, p apiv1.CreateAccountingCategoryParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.CategoryInput
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.CreateCategory(c.Request.Context(), localActor(c), book, p.IdempotencyKey.String(), in)
	accountingReply(c, 201, v, err)
}

func (a api) UpdateAccountingCategory(c *gin.Context, book, id uuid.UUID, p apiv1.UpdateAccountingCategoryParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.ReferencePatch
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.UpdateCategory(c.Request.Context(), localActor(c), book, id, p.IdempotencyKey.String(), in)
	accountingReply(c, 200, v, err)
}

func (a api) ListAccountingCounterparties(c *gin.Context, _ apiv1.ListAccountingCounterpartiesParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.Counterparties(c.Request.Context(), localActor(c))
	accountingReply(c, 200, gin.H{"counterparties": v}, err)
}

func (a api) CreateAccountingCounterparty(c *gin.Context, p apiv1.CreateAccountingCounterpartyParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.CounterpartyInput
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.CreateCounterparty(c.Request.Context(), localActor(c), p.IdempotencyKey.String(), in)
	accountingReply(c, 201, v, err)
}

func (a api) UpdateAccountingCounterparty(c *gin.Context, id uuid.UUID, p apiv1.UpdateAccountingCounterpartyParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.ReferencePatch
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.UpdateCounterparty(c.Request.Context(), localActor(c), id, p.IdempotencyKey.String(), in)
	accountingReply(c, 200, v, err)
}

func (a api) ListAccountingTransactions(c *gin.Context, book uuid.UUID, _ apiv1.ListAccountingTransactionsParams) {
	if !a.accountingReady(c) {
		return
	}
	limit := 50
	if value := c.Query("limit"); value != "" {
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n < 1 || n > 100 {
			problem.Write(c, problem.New(problem.InvalidRequest, "Use a limit between 1 and 100."))
			return
		}
		limit = int(n)
	}
	in := accounting.Filter{From: c.Query("from"), To: c.Query("to"), AccountID: c.Query("account_id"), CategoryID: c.Query("category_id"), CounterpartyID: c.Query("counterparty_id"), Kind: c.Query("kind"), Currency: c.Query("currency"), Cursor: c.Query("cursor"), Limit: int32(limit), IncludeVoided: c.Query("include_voided") == "true"}
	v, err := a.deps.Accounting.Transactions(c.Request.Context(), localActor(c), book, in)
	accountingReply(c, 200, v, err)
}

func (a api) GetAccountingTransaction(c *gin.Context, book, id uuid.UUID, _ apiv1.GetAccountingTransactionParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.Transaction(c.Request.Context(), localActor(c), book, id)
	accountingReply(c, 200, v, err)
}

func (a api) CreateAccountingTransaction(c *gin.Context, book uuid.UUID, p apiv1.CreateAccountingTransactionParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.CreateInput
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.CreateTransaction(c.Request.Context(), localActor(c), book, p.IdempotencyKey.String(), in)
	accountingReply(c, 201, v, err)
}

func (a api) CorrectAccountingTransaction(c *gin.Context, book, id uuid.UUID, p apiv1.CorrectAccountingTransactionParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.CorrectionInput
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.CorrectTransaction(c.Request.Context(), localActor(c), book, id, p.IdempotencyKey.String(), in)
	accountingReply(c, 200, v, err)
}

func (a api) ListAccountingLinks(c *gin.Context, book uuid.UUID, _ apiv1.ListAccountingLinksParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.Links(c.Request.Context(), localActor(c), book, c.Query("transaction_id"))
	accountingReply(c, 200, gin.H{"links": v}, err)
}

func (a api) CreateAccountingLink(c *gin.Context, book uuid.UUID, p apiv1.CreateAccountingLinkParams) {
	if !a.accountingReady(c) {
		return
	}
	var in accounting.LinkInput
	if !decode(c, &in, false) {
		return
	}
	v, err := a.deps.Accounting.CreateLink(c.Request.Context(), localActor(c), book, p.IdempotencyKey.String(), in)
	accountingReply(c, 201, v, err)
}

func (a api) RemoveAccountingLink(c *gin.Context, book, id uuid.UUID, p apiv1.RemoveAccountingLinkParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.RemoveLink(c.Request.Context(), localActor(c), book, id, p.IdempotencyKey.String())
	accountingReply(c, 200, v, err)
}

func (a api) GetAccountingSummary(c *gin.Context, book uuid.UUID, _ apiv1.GetAccountingSummaryParams) {
	if !a.accountingReady(c) {
		return
	}
	v, err := a.deps.Accounting.Summary(c.Request.Context(), localActor(c), book, c.Query("from"), c.Query("to"))
	accountingReply(c, 200, v, err)
}
