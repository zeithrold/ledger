package httpserver

import (
	"testing"

	"github.com/zeithrold/ledger/internal/apiv1"
	"github.com/zeithrold/ledger/internal/rates"
)

func TestMarketRateDTO(t *testing.T) {
	dto := marketRateDTO(rates.Rate{
		Base: "USD", Quote: "CNY", Status: rates.StatusStale, Stale: true, Source: "frankfurter",
		ProviderFilter: "blended", Pivot: "EUR", Display: "6.7082", Numerator: "67082", Denominator: "10000",
		Derived: rates.DerivedCross, RateDate: "2026-09-15", SnapshotDate: "2026-09-16",
		FetchedAt: "2026-09-16T17:10:03Z", LatestSnapshotDate: "2026-09-16",
	})
	if dto.Base != "USD" || dto.Quote != "CNY" || dto.Status != apiv1.MarketRateStatus("stale") || !dto.Stale || dto.Source != "frankfurter" {
		t.Fatalf("identity fields: %+v", dto)
	}
	if dto.ProviderFilter == nil || *dto.ProviderFilter != "blended" || dto.Pivot == nil || *dto.Pivot != "EUR" {
		t.Fatalf("snapshot provenance: %+v", dto)
	}
	if dto.Rate == nil || *dto.Rate != "6.7082" || dto.Numerator == nil || *dto.Numerator != "67082" || dto.Denominator == nil || *dto.Denominator != "10000" {
		t.Fatalf("rate fields: %+v", dto)
	}
	if dto.Derived == nil || *dto.Derived != "cross" || dto.RateDate == nil || *dto.RateDate != "2026-09-15" {
		t.Fatalf("derivation fields: %+v", dto)
	}
	if dto.SnapshotDate == nil || *dto.SnapshotDate != "2026-09-16" || dto.FetchedAt == nil || *dto.FetchedAt != "2026-09-16T17:10:03Z" {
		t.Fatalf("fetch fields: %+v", dto)
	}
	if dto.LatestSnapshotDate == nil || *dto.LatestSnapshotDate != "2026-09-16" || dto.Reason != nil {
		t.Fatalf("availability fields: %+v", dto)
	}

	empty := marketRateDTO(rates.Rate{Base: "USD", Quote: "CNY", Status: rates.StatusUnavailable, Source: "frankfurter", Reason: rates.ReasonNoSnapshot})
	for name, value := range map[string]*string{
		"provider_filter": empty.ProviderFilter, "pivot": empty.Pivot, "rate": empty.Rate, "numerator": empty.Numerator,
		"denominator": empty.Denominator, "derived": empty.Derived, "rate_date": empty.RateDate, "snapshot_date": empty.SnapshotDate,
		"fetched_at": empty.FetchedAt, "latest_snapshot_date": empty.LatestSnapshotDate,
	} {
		if value != nil {
			t.Fatalf("%s should be null", name)
		}
	}
	if empty.Reason == nil || *empty.Reason != "no_snapshot" {
		t.Fatalf("reason: %+v", empty.Reason)
	}
}
