package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var countryPattern = regexp.MustCompile(`^[A-Z]{2}$`)

var ErrInvalidLocation = errors.New("LOCATION_INVALID")

type CountryCode string

func NewCountryCode(value string) (CountryCode, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if !countryPattern.MatchString(value) {
		return "", fmt.Errorf("%w: country must be an ISO 3166-1 alpha-2 code", ErrInvalidLocation)
	}
	return CountryCode(value), nil
}

type LocationContext struct {
	Country CountryCode `json:"country"`
	City    string      `json:"city,omitempty"`
}

func NewLocationContext(country, city string) (LocationContext, error) {
	code, err := NewCountryCode(country)
	if err != nil {
		return LocationContext{}, err
	}
	return LocationContext{Country: code, City: strings.TrimSpace(city)}, nil
}
