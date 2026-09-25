package app

import (
	"context"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"sync"
	"time"
)

type ExchangeRateRepository interface {
	ReadExchangeRate(context.Context) (researchdomain.DailyExchangeRate, error)
	ClaimExchangeRateRefresh(context.Context, time.Time) (bool, error)
	SaveExchangeRate(context.Context, researchdomain.DailyExchangeRate) error
}
type ExchangeRateGateway interface {
	FetchExchangeRate(context.Context) (researchdomain.DailyExchangeRate, error)
}
type ExchangeRateService struct {
	Repository ExchangeRateRepository
	Gateway    ExchangeRateGateway
	mu         sync.Mutex
}
type ExchangeRateView struct {
	SchemaVersion string                            `json:"schemaVersion"`
	Status        string                            `json:"status"`
	Rate          *researchdomain.DailyExchangeRate `json:"rate,omitempty"`
}

func (s *ExchangeRateService) View(ctx context.Context) (ExchangeRateView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	view := ExchangeRateView{SchemaVersion: "vitlane.exchange-rate.v1", Status: "UNAVAILABLE"}
	stored, err := s.Repository.ReadExchangeRate(ctx)
	if err != nil {
		return view, err
	}
	if stored.ObservedAt.UTC().Format("2006-01-02") != now.Format("2006-01-02") && s.Gateway != nil {
		claimed, claimErr := s.Repository.ClaimExchangeRateRefresh(ctx, now)
		if claimErr != nil {
			return view, claimErr
		}
		if claimed {
			fresh, fetchErr := s.Gateway.FetchExchangeRate(ctx)
			if fetchErr == nil && fresh.Valid(now) {
				if saveErr := s.Repository.SaveExchangeRate(ctx, fresh); saveErr != nil {
					return view, saveErr
				}
				stored = fresh
			}
		}
	}
	if stored.Valid(now) {
		view.Rate = &stored
		view.Status = "STALE"
		if stored.ObservedAt.UTC().Format("2006-01-02") == now.Format("2006-01-02") && stored.AsOf == now.Format("2006-01-02") {
			view.Status = "CURRENT"
		}
	}
	return view, nil
}
