package shopifyucp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const storefrontSecurityStatusV2 = 430

type catalogProviderErrorV2 struct {
	err               error
	batchSizeExceeded bool
}

func (failure *catalogProviderErrorV2) Error() string { return failure.err.Error() }
func (failure *catalogProviderErrorV2) Unwrap() error { return failure.err }

func newCatalogFaultV2(
	code fault.Code,
	reason researchapp.CatalogFailureReason,
	retryable bool,
	retryAfter time.Duration,
) error {
	failure := fault.New(code, string(reason), retryable)
	failure.RetryAfter = retryAfter
	return failure
}

func remapTransportErrorV2(err error) error {
	classified, ok := fault.As(err)
	if !ok {
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	}
	switch classified.Code {
	case fault.CallerCancelled:
		return err
	case fault.RateLimited:
		return newCatalogFaultV2(
			fault.RateLimited, researchapp.CatalogFailureRateLimited,
			true, classified.RetryAfter,
		)
	default:
		return fault.Wrap(
			err, fault.ProviderUnavailable,
			string(researchapp.CatalogFailureUnavailable), true,
		)
	}
}

func classifyHTTPFailureV2(status int, retryAfterHeader string, body []byte) error {
	signals := rpcErrorSignalsV2{}
	var decoded wireRPCResponseV2
	if json.Unmarshal(body, &decoded) == nil && decoded.Error != nil {
		signals = extractRPCErrorSignalsV2(decoded.Error.Data)
	}
	retryAfter := sharedhttpclient.ParseRetryAfter(retryAfterHeader, time.Now())
	if retryAfter == 0 {
		retryAfter = signals.retryAfter
	}

	switch {
	case status == 429:
		return newCatalogFaultV2(
			fault.RateLimited, researchapp.CatalogFailureRateLimited, true, retryAfter,
		)
	case status == storefrontSecurityStatusV2:
		return newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureSecurityRejected, false, 0,
		)
	case status == 401 || status == 403 || status == 424:
		return newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureProfileOrAuth, false, 0,
		)
	case status == 503 || status >= 500:
		return newCatalogFaultV2(
			fault.ProviderUnavailable, researchapp.CatalogFailureUnavailable, true, retryAfter,
		)
	case status == 400 || status == 422:
		if signals.hasAny(profileOrAuthCodesV2...) {
			return newCatalogFaultV2(
				fault.ProviderRejected, researchapp.CatalogFailureProfileOrAuth, false, 0,
			)
		}
		if signals.hasAny(schemaMismatchCodesV2...) {
			return newCatalogFaultV2(
				fault.ProviderRejected, researchapp.CatalogFailureSchemaMismatch, false, 0,
			)
		}
		validation := newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureValidation, false, 0,
		)
		if signals.explicitBatchSizeFailure() {
			return &catalogProviderErrorV2{err: validation, batchSizeExceeded: true}
		}
		return validation
	default:
		return newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureProtocolRejected, false, 0,
		)
	}
}

func classifyRPCFailureV2(responseError *wireRPCErrorV2) error {
	signals := extractRPCErrorSignalsV2(responseError.Data)
	switch {
	case signals.hasAny(rateLimitCodesV2...):
		return newCatalogFaultV2(
			fault.RateLimited, researchapp.CatalogFailureRateLimited,
			true, signals.retryAfter,
		)
	case responseError.Code == -32001 || signals.hasAny(profileOrAuthCodesV2...):
		return newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureProfileOrAuth, false, 0,
		)
	case signals.hasAny(schemaMismatchCodesV2...):
		return newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureSchemaMismatch, false, 0,
		)
	case responseError.Code == -32602:
		validation := newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureValidation, false, 0,
		)
		if signals.explicitBatchSizeFailure() {
			return &catalogProviderErrorV2{err: validation, batchSizeExceeded: true}
		}
		return validation
	case responseError.Code == -32603:
		return newCatalogFaultV2(
			fault.ProviderUnavailable, researchapp.CatalogFailureUnavailable, true,
			signals.retryAfter,
		)
	default:
		return newCatalogFaultV2(
			fault.ProviderRejected, researchapp.CatalogFailureProtocolRejected, false, 0,
		)
	}
}

func isExplicitBatchSizeFailureV2(err error) bool {
	failure, ok := err.(*catalogProviderErrorV2)
	return ok && failure.batchSizeExceeded
}

var rateLimitCodesV2 = []string{
	"RATE_LIMITED", "THROTTLED", "TOO_MANY_REQUESTS",
}

var profileOrAuthCodesV2 = []string{
	"UNAUTHORIZED", "FORBIDDEN", "ACCESS_DENIED", "PROFILE_INVALID",
	"PROFILE_NOT_FOUND", "PROFILE_REJECTED", "AUTHENTICATION_REQUIRED",
}

var schemaMismatchCodesV2 = []string{
	"CAPABILITIES_INCOMPATIBLE", "CAPABILITY_INCOMPATIBLE", "VERSION_INCOMPATIBLE",
	"SCHEMA_MISMATCH", "UNSUPPORTED_CAPABILITY",
}

var batchSizeCodesV2 = []string{
	"BATCH_SIZE_EXCEEDED", "TOO_MANY_IDS", "MAX_IDS_EXCEEDED",
	"MAX_ITEMS_EXCEEDED", "ARRAY_TOO_LONG",
}

type rpcErrorSignalsV2 struct {
	codes      map[string]struct{}
	paths      []string
	hasLimit   bool
	retryAfter time.Duration
}

func (signals rpcErrorSignalsV2) hasAny(values ...string) bool {
	for _, value := range values {
		if _, exists := signals.codes[value]; exists {
			return true
		}
	}
	return false
}

func (signals rpcErrorSignalsV2) explicitBatchSizeFailure() bool {
	if signals.hasAny(batchSizeCodesV2...) {
		return true
	}
	if !signals.hasLimit {
		return false
	}
	for _, path := range signals.paths {
		normalized := strings.ToLower(strings.TrimSpace(path))
		if normalized == "catalog.ids" || normalized == "$.catalog.ids" ||
			strings.HasSuffix(normalized, "/catalog/ids") ||
			strings.HasSuffix(normalized, "/ids") {
			return true
		}
	}
	return false
}

func extractRPCErrorSignalsV2(raw json.RawMessage) rpcErrorSignalsV2 {
	signals := rpcErrorSignalsV2{codes: map[string]struct{}{}}
	if len(raw) == 0 {
		return signals
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return signals
	}
	walkRPCErrorDataV2(value, "", &signals)
	return signals
}

func walkRPCErrorDataV2(value any, parentKey string, signals *rpcErrorSignalsV2) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalizedKey := strings.ToLower(strings.TrimSpace(key))
			switch normalizedKey {
			case "code", "reason", "type", "category", "error_code":
				if text, ok := child.(string); ok {
					signals.codes[normalizeSignalTokenV2(text)] = struct{}{}
				}
			case "path", "field", "parameter":
				if text, ok := child.(string); ok {
					signals.paths = append(signals.paths, text)
				}
			case "max", "maximum", "limit", "max_items", "max_ids", "allowed":
				if _, ok := numberValueV2(child); ok {
					signals.hasLimit = true
				}
			case "retry_after", "retryafter":
				if duration, ok := retryAfterValueV2(child); ok {
					signals.retryAfter = duration
				}
			}
			walkRPCErrorDataV2(child, normalizedKey, signals)
		}
	case []any:
		for _, child := range typed {
			walkRPCErrorDataV2(child, parentKey, signals)
		}
	}
}

func normalizeSignalTokenV2(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(value)
	return value
}

func numberValueV2(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func retryAfterValueV2(value any) (time.Duration, bool) {
	if number, ok := numberValueV2(value); ok && number > 0 {
		return time.Duration(number * float64(time.Second)), true
	}
	text, ok := value.(string)
	if !ok {
		return 0, false
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}
