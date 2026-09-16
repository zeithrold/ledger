package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zeithrold/ledger/internal/apiv1"
	"github.com/zeithrold/ledger/internal/problem"
	"github.com/zeithrold/ledger/internal/problemhttp"
	"github.com/zeithrold/ledger/internal/rates"
)

func (a api) ratesReady(c *gin.Context) bool {
	if a.deps.Rates == nil {
		problemhttp.Write(c, problem.New(problem.Unavailable, "The exchange-rate API is not configured."))
		return false
	}
	return true
}

// GetExchangeRate serves the published market snapshot. It never triggers a
// provider call and never invents a rate for an unavailable pair.
func (a api) GetExchangeRate(c *gin.Context, params apiv1.GetExchangeRateParams) {
	if !a.ratesReady(c) {
		return
	}
	rate, err := a.deps.Rates.Rate(c.Request.Context(), params.Base, params.Quote)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, marketRateDTO(rate))
}

func marketRateDTO(rate rates.Rate) apiv1.MarketRate {
	return apiv1.MarketRate{
		Base:               rate.Base,
		Quote:              rate.Quote,
		Status:             apiv1.MarketRateStatus(rate.Status),
		Stale:              rate.Stale,
		Source:             rate.Source,
		ProviderFilter:     optionalText(rate.ProviderFilter),
		Pivot:              optionalText(rate.Pivot),
		Rate:               optionalText(rate.Display),
		Numerator:          optionalText(rate.Numerator),
		Denominator:        optionalText(rate.Denominator),
		Derived:            optionalText(string(rate.Derived)),
		RateDate:           optionalText(rate.RateDate),
		SnapshotDate:       optionalText(rate.SnapshotDate),
		FetchedAt:          optionalText(rate.FetchedAt),
		LatestSnapshotDate: optionalText(rate.LatestSnapshotDate),
		Reason:             optionalText(string(rate.Reason)),
	}
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
