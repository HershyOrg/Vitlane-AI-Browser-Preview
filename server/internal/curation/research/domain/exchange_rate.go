package domain

import (
	"errors"
	"math/big"
	"time"
)

var ErrExchangeRate = errors.New("EXCHANGE_RATE_UNAVAILABLE")

type DailyExchangeRate struct {
	Base       string    `json:"base"`
	Quote      string    `json:"quote"`
	Rate       string    `json:"rate"`
	AsOf       string    `json:"asOf"`
	ObservedAt time.Time `json:"observedAt"`
	Source     string    `json:"source"`
}

func (r DailyExchangeRate) Valid(now time.Time) bool {
	rate, ok := new(big.Rat).SetString(r.Rate)
	date, err := time.Parse("2006-01-02", r.AsOf)
	return r.Base == "USD" && r.Quote == "KRW" && len(r.Rate) <= 32 && ok && rate.Sign() > 0 && rate.Cmp(big.NewRat(1000000, 1)) < 0 && err == nil && !date.After(now.UTC()) && now.UTC().Sub(date) < 8*24*time.Hour && !r.ObservedAt.IsZero() && !r.ObservedAt.After(now.Add(time.Minute))
}

// ConvertResearchMinor rounds the display estimate to the destination minor
// unit. Original observations, budget constraints and checkout money stay intact.
func ConvertResearchMinor(amount int64, from, to string, rate DailyExchangeRate, now time.Time) (int64, error) {
	if amount < 0 || amount > 9007199254740991 || (from != "USD" && from != "KRW") || (to != "USD" && to != "KRW") {
		return 0, ErrExchangeRate
	}
	if from == to {
		return amount, nil
	}
	if !rate.Valid(now) {
		return 0, ErrExchangeRate
	}
	factor, _ := new(big.Rat).SetString(rate.Rate)
	factor.Quo(factor, big.NewRat(100, 1))
	if from == "KRW" {
		factor.Inv(factor)
	}
	value := factor.Mul(factor, big.NewRat(amount, 1))
	whole, remainder := new(big.Int).QuoRem(value.Num(), value.Denom(), new(big.Int))
	if remainder.Mul(remainder, big.NewInt(2)).Cmp(value.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if !whole.IsInt64() || whole.Int64() > 9007199254740991 {
		return 0, ErrExchangeRate
	}
	return whole.Int64(), nil
}
