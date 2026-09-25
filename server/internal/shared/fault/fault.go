// Package fault carries the shared failure taxonomy from the Phase 6 plan
// section 5.4. Callers distinguish these classes instead of parsing provider
// text, so a timeout is never mistaken for a rejection and an unknown external
// effect is never mistaken for a failure.
package fault

import (
	"errors"
	"time"
)

type Code string

const (
	InvalidInput          Code = "INVALID_INPUT"
	Conflict              Code = "CONFLICT"
	RateLimited           Code = "RATE_LIMITED"
	DeadlineExceeded      Code = "DEADLINE_EXCEEDED"
	CallerCancelled       Code = "CALLER_CANCELLED"
	ProviderUnavailable   Code = "PROVIDER_UNAVAILABLE"
	ProviderRejected      Code = "PROVIDER_REJECTED"
	QuotaExceeded         Code = "QUOTA_EXCEEDED"
	ExternalEffectUnknown Code = "EXTERNAL_EFFECT_UNKNOWN"
	InternalFailure       Code = "INTERNAL_FAILURE"
)

// Error carries a machine-readable class and a safe reason code. Provider raw
// text, user Intent and credentials never enter either field: everything here
// is designed to be logged and returned to the browser as-is.
type Error struct {
	Code       Code
	Reason     string
	Retryable  bool
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e.Reason != "" {
		return string(e.Code) + ": " + e.Reason
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error { return e.cause }

// New builds a fault without wrapping a cause. Use Wrap when the underlying
// error still matters for errors.Is checks upstream.
func New(code Code, reason string, retryable bool) *Error {
	return &Error{Code: code, Reason: reason, Retryable: retryable}
}

func Wrap(cause error, code Code, reason string, retryable bool) *Error {
	return &Error{Code: code, Reason: reason, Retryable: retryable, cause: cause}
}

// As reports the fault classification of err, if it has one.
func As(err error) (*Error, bool) {
	var fault *Error
	if errors.As(err, &fault) {
		return fault, true
	}
	return nil, false
}

// CodeOf classifies any error. An error that never passed through this package
// is an internal failure: guessing a friendlier class would hide a defect.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	if fault, ok := As(err); ok {
		return fault.Code
	}
	return InternalFailure
}

// Retryable reports whether retrying the same request is safe and worthwhile.
// An unclassified error is not retryable, because an unknown external effect
// must never be repeated on a guess.
func Retryable(err error) bool {
	if fault, ok := As(err); ok {
		return fault.Retryable
	}
	return false
}
