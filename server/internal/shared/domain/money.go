package domain

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var (
	ErrInvalidMoney     = errors.New("MONEY_INVALID")
	ErrCurrencyMismatch = errors.New("CURRENCY_MISMATCH")
	currencyPattern     = regexp.MustCompile(`^[A-Z]{3}$`)
	decimalPattern      = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

type CurrencyCode string

func NewCurrencyCode(value string) (CurrencyCode, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !currencyPattern.MatchString(value) {
		return "", fmt.Errorf("%w: currency must be an ISO 4217 code", ErrInvalidMoney)
	}
	return CurrencyCode(value), nil
}

type Money struct {
	Amount   string       `json:"amount"`
	Currency CurrencyCode `json:"currency"`
}

func NewMoney(amount, currency string) (Money, error) {
	amount = strings.TrimSpace(amount)
	if !decimalPattern.MatchString(amount) {
		return Money{}, fmt.Errorf("%w: amount must be a decimal string", ErrInvalidMoney)
	}
	code, err := NewCurrencyCode(currency)
	if err != nil {
		return Money{}, err
	}
	rat, ok := new(big.Rat).SetString(amount)
	if !ok {
		return Money{}, ErrInvalidMoney
	}
	return Money{Amount: canonicalDecimal(rat, amount), Currency: code}, nil
}

func (m Money) Sign() int {
	value, ok := new(big.Rat).SetString(m.Amount)
	if !ok {
		return 0
	}
	return value.Sign()
}

func (m Money) Compare(other Money) (int, error) {
	if m.Currency != other.Currency {
		return 0, ErrCurrencyMismatch
	}
	left, leftOK := new(big.Rat).SetString(m.Amount)
	right, rightOK := new(big.Rat).SetString(other.Amount)
	if !leftOK || !rightOK {
		return 0, ErrInvalidMoney
	}
	return left.Cmp(right), nil
}

func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	left, leftOK := new(big.Rat).SetString(m.Amount)
	right, rightOK := new(big.Rat).SetString(other.Amount)
	if !leftOK || !rightOK {
		return Money{}, ErrInvalidMoney
	}
	sum := new(big.Rat).Add(left, right)
	scale := 0
	if parts := strings.SplitN(m.Amount, ".", 2); len(parts) == 2 {
		scale = len(parts[1])
	}
	if parts := strings.SplitN(other.Amount, ".", 2); len(parts) == 2 && len(parts[1]) > scale {
		scale = len(parts[1])
	}
	return Money{
		Amount:   canonicalDecimal(sum, sum.FloatString(scale)),
		Currency: m.Currency,
	}, nil
}

func canonicalDecimal(value *big.Rat, original string) string {
	parts := strings.SplitN(original, ".", 2)
	if len(parts) == 1 {
		return value.Num().String()
	}
	result := value.FloatString(len(parts[1]))
	result = strings.TrimRight(result, "0")
	result = strings.TrimRight(result, ".")
	if result == "-0" {
		return "0"
	}
	return result
}
