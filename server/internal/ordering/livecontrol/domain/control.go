// Package domain owns the fail-closed runtime state for starting new Live
// order, PayPal-money, and merchant effects. Reconciliation and compensation
// deliberately do not consult this state.
package domain

import (
	"errors"
	"strings"
	"time"
)

type Scope string

const (
	ScopeAll            Scope = "ALL_NEW_LIVE_EFFECTS"
	ScopeOrderIssue     Scope = "LIVE_ORDER_ISSUE"
	ScopePayPalMoney    Scope = "PAYPAL_LIVE_MONEY_EFFECT"
	ScopeMerchantEffect Scope = "LIVE_MERCHANT_EFFECT"
)

var (
	ErrInvalid         = errors.New("live control input is invalid")
	ErrVersionConflict = errors.New("live control version conflict")
)

type State struct {
	Version              int64     `json:"version"`
	OrderIssueKilled     bool      `json:"orderIssueKilled"`
	PayPalMoneyKilled    bool      `json:"paypalMoneyKilled"`
	MerchantEffectKilled bool      `json:"merchantEffectKilled"`
	ChangedAt            time.Time `json:"changedAt"`
	ChangedBy            string    `json:"changedBy"`
	Reason               string    `json:"reason"`
}

func ParseScope(value string) (Scope, error) {
	scope := Scope(strings.ToUpper(strings.TrimSpace(value)))
	switch scope {
	case ScopeAll, ScopeOrderIssue, ScopePayPalMoney, ScopeMerchantEffect:
		return scope, nil
	default:
		return "", ErrInvalid
	}
}

func Confirmation(scope Scope, kill bool) string {
	verb := "KILL"
	if !kill {
		verb = "REACTIVATE"
	}
	suffix := map[Scope]string{
		ScopeAll: "ALL", ScopeOrderIssue: "ORDERS",
		ScopePayPalMoney: "MONEY", ScopeMerchantEffect: "MERCHANT",
	}[scope]
	return verb + " PAYPAL LIVE " + suffix
}

func (s State) Killed(scope Scope) bool {
	switch scope {
	case ScopeOrderIssue:
		return s.OrderIssueKilled
	case ScopePayPalMoney:
		return s.PayPalMoneyKilled
	case ScopeMerchantEffect:
		return s.MerchantEffectKilled
	case ScopeAll:
		return s.OrderIssueKilled && s.PayPalMoneyKilled && s.MerchantEffectKilled
	default:
		return true
	}
}
