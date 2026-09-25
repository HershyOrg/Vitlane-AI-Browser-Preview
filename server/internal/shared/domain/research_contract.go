package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ResearchContractVersion is fixed when a research aggregate is created.
// During the Phase 8 dormant rollout V1 and V2 data must never share a
// process, repository, or delivery message.
type ResearchContractVersion string

const (
	ResearchContractVersionV1Legacy  ResearchContractVersion = "V1_LEGACY"
	CatalogResearchContractVersionV2 ResearchContractVersion = "V2_PHASE8"
)

func (v ResearchContractVersion) Valid() bool {
	return v == ResearchContractVersionV1Legacy ||
		v == CatalogResearchContractVersionV2
}

func (v ResearchContractVersion) Validate() error {
	if !v.Valid() {
		return fmt.Errorf("%w: %q", ErrResearchContractVersionInvalid, v)
	}
	return nil
}

var (
	ErrResearchContractVersionInvalid = errors.New(
		"RESEARCH_CONTRACT_VERSION_INVALID",
	)
	ErrResearchPriceConstraintInvalid = errors.New(
		"RESEARCH_PRICE_CONSTRAINT_INVALID",
	)
	ErrMarketContextInvalid = errors.New("MARKET_CONTEXT_INVALID")
)

// MarketContext selects the market in which provider facts are observed. It
// is deliberately independent from ResearchPriceConstraint: USD does not
// imply that the user set a price ceiling.
type MarketContext struct {
	Country  CountryCode  `json:"country"`
	Currency CurrencyCode `json:"currency"`
}

func NewMarketContext(country, currency string) (MarketContext, error) {
	countryCode, err := NewCountryCode(country)
	if err != nil {
		return MarketContext{}, fmt.Errorf("%w: %v", ErrMarketContextInvalid, err)
	}
	currencyCode, err := NewCurrencyCode(currency)
	if err != nil {
		return MarketContext{}, fmt.Errorf("%w: %v", ErrMarketContextInvalid, err)
	}
	return MarketContext{Country: countryCode, Currency: currencyCode}, nil
}

func (m MarketContext) Validate() error {
	country, err := NewCountryCode(string(m.Country))
	if err != nil || country != m.Country {
		return fmt.Errorf("%w: country", ErrMarketContextInvalid)
	}
	currency, err := NewCurrencyCode(string(m.Currency))
	if err != nil || currency != m.Currency {
		return fmt.Errorf("%w: currency", ErrMarketContextInvalid)
	}
	return nil
}

type ResearchPriceConstraintKind string

const (
	ResearchPriceConstraintNone     ResearchPriceConstraintKind = "NONE"
	ResearchPriceConstraintExplicit ResearchPriceConstraintKind = "EXPLICIT"
)

// ResearchPriceConstraint distinguishes an absent constraint from an
// explicit zero bound. Do not infer NONE from a zero-value Money.
type ResearchPriceConstraint struct {
	Kind ResearchPriceConstraintKind `json:"kind"`
	Min  *Money                      `json:"min,omitempty"`
	Max  *Money                      `json:"max,omitempty"`
}

func NoResearchPriceConstraint() ResearchPriceConstraint {
	return ResearchPriceConstraint{Kind: ResearchPriceConstraintNone}
}

func NewExplicitResearchPriceConstraint(
	minimum, maximum *Money,
) (ResearchPriceConstraint, error) {
	constraint := ResearchPriceConstraint{
		Kind: ResearchPriceConstraintExplicit,
		Min:  cloneMoney(minimum),
		Max:  cloneMoney(maximum),
	}
	if err := constraint.Validate(); err != nil {
		return ResearchPriceConstraint{}, err
	}
	return constraint, nil
}

func (c ResearchPriceConstraint) Validate() error {
	switch c.Kind {
	case ResearchPriceConstraintNone:
		if c.Min != nil || c.Max != nil {
			return fmt.Errorf(
				"%w: NONE cannot carry bounds",
				ErrResearchPriceConstraintInvalid,
			)
		}
		return nil
	case ResearchPriceConstraintExplicit:
		if c.Min == nil && c.Max == nil {
			return fmt.Errorf(
				"%w: EXPLICIT requires a bound",
				ErrResearchPriceConstraintInvalid,
			)
		}
	default:
		return fmt.Errorf(
			"%w: kind %q",
			ErrResearchPriceConstraintInvalid,
			c.Kind,
		)
	}

	for name, bound := range map[string]*Money{"min": c.Min, "max": c.Max} {
		if bound == nil {
			continue
		}
		if bound.Sign() < 0 || strings.TrimSpace(bound.Amount) == "" {
			return fmt.Errorf(
				"%w: %s must be non-negative",
				ErrResearchPriceConstraintInvalid,
				name,
			)
		}
		canonical, err := NewMoney(bound.Amount, string(bound.Currency))
		if err != nil || canonical != *bound {
			return fmt.Errorf(
				"%w: %s is not canonical money",
				ErrResearchPriceConstraintInvalid,
				name,
			)
		}
	}

	if c.Min != nil && c.Max != nil {
		comparison, err := c.Min.Compare(*c.Max)
		if err != nil || comparison > 0 {
			return fmt.Errorf(
				"%w: min must not exceed max",
				ErrResearchPriceConstraintInvalid,
			)
		}
	}
	return nil
}

func (c ResearchPriceConstraint) ValidateForMarket(market MarketContext) error {
	if err := market.Validate(); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Min != nil && c.Min.Currency != market.Currency {
		return fmt.Errorf(
			"%w: min currency must match market",
			ErrResearchPriceConstraintInvalid,
		)
	}
	if c.Max != nil && c.Max.Currency != market.Currency {
		return fmt.Errorf(
			"%w: max currency must match market",
			ErrResearchPriceConstraintInvalid,
		)
	}
	return nil
}

func (c ResearchPriceConstraint) Equal(other ResearchPriceConstraint) bool {
	if c.Kind != other.Kind {
		return false
	}
	return equalMoney(c.Min, other.Min) && equalMoney(c.Max, other.Max)
}

func (c ResearchPriceConstraint) Clone() ResearchPriceConstraint {
	return ResearchPriceConstraint{
		Kind: c.Kind,
		Min:  cloneMoney(c.Min),
		Max:  cloneMoney(c.Max),
	}
}

func cloneMoney(value *Money) *Money {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func equalMoney(left, right *Money) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
