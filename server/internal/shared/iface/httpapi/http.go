package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type APIError struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	ReasonCode        string `json:"reasonCode"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
	RequestID         string `json:"requestId"`
	AgencyOrderID     string `json:"agencyOrderId,omitempty"`
	ItemTitle         string `json:"itemTitle,omitempty"`
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "요청 형식을 확인해 주세요.")
		return false
	}
	return true
}

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	retryAfterSeconds, _ := strconv.Atoi(strings.TrimSpace(
		w.Header().Get("Retry-After"),
	))
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 0
	}
	WriteJSON(w, status, ErrorResponse{Error: APIError{
		Code: code, Message: message, ReasonCode: code,
		Retryable:         status == http.StatusTooManyRequests && retryAfterSeconds > 0,
		RetryAfterSeconds: retryAfterSeconds,
		RequestID:         strings.TrimSpace(w.Header().Get("X-Request-Id")),
	}})
}

func WriteFault(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	message string,
) {
	failure, ok := fault.As(err)
	if !ok {
		failure = fault.New(fault.InternalFailure, "INTERNAL_FAILURE", false)
	}
	status := faultHTTPStatus(failure.Code)
	retryAfterSeconds := 0
	if failure.RetryAfter > 0 {
		retryAfterSeconds = int((failure.RetryAfter + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	requestID := strings.TrimSpace(w.Header().Get("X-Request-Id"))
	if r != nil {
		if contextRequestID := sharedapp.RequestID(r.Context()); contextRequestID != "" {
			requestID = contextRequestID
		}
	}
	reason := failure.Reason
	if reason == "" {
		reason = string(failure.Code)
	}
	WriteJSON(w, status, ErrorResponse{Error: APIError{
		Code: string(failure.Code), Message: message,
		ReasonCode: reason, Retryable: failure.Retryable,
		RetryAfterSeconds: retryAfterSeconds, RequestID: requestID,
	}})
}

// WriteFaultIfClassified preserves a database/provider/runtime classification
// before a legacy product-specific fallback turns an unclassified defect into
// its existing INTERNAL_ERROR response.
func WriteFaultIfClassified(
	w http.ResponseWriter,
	r *http.Request,
	err error,
	message string,
) bool {
	if _, ok := fault.As(err); !ok {
		return false
	}
	WriteFault(w, r, err, message)
	return true
}

func faultHTTPStatus(code fault.Code) int {
	switch code {
	case fault.InvalidInput:
		return http.StatusBadRequest
	case fault.Conflict:
		return http.StatusConflict
	case fault.RateLimited, fault.QuotaExceeded:
		return http.StatusTooManyRequests
	case fault.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case fault.CallerCancelled:
		return http.StatusRequestTimeout
	case fault.ProviderUnavailable:
		return http.StatusServiceUnavailable
	case fault.ProviderRejected:
		return http.StatusBadGateway
	default:
		// EXTERNAL_EFFECT_UNKNOWN requires a durable operation projection before
		// it can be represented as 202. The generic writer fails closed.
		return http.StatusInternalServerError
	}
}

func RequireAuthenticatedUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := sharedapp.AuthenticatedUserIDFrom(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "로그인이 필요합니다.")
		return "", false
	}
	return userID, true
}

// LimitConcurrentRequests bounds public API handler work without creating an
// in-process waiting queue. The common request middleware wraps this handler,
// so rejected requests retain the normal request ID and error contract.
func LimitConcurrentRequests(next http.Handler, maximum int) http.Handler {
	if maximum < 1 {
		panic("maximum concurrent HTTP requests must be positive")
	}
	slots := make(chan struct{}, maximum)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			failure := fault.New(
				fault.ProviderUnavailable,
				"HTTP_CONCURRENCY_LIMIT_REACHED",
				true,
			)
			failure.RetryAfter = time.Second
			WriteFault(w, r, failure, "요청이 많습니다. 잠시 후 다시 시도해 주세요.")
		}
	})
}

func Middleware(
	next http.Handler,
	ids sharedapp.IDGenerator,
	logger *slog.Logger,
	requestTimeout time.Duration,
	timeoutForRequest ...func(*http.Request) time.Duration,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
		if requestID == "" {
			requestID = ids.NewID()
		}
		w.Header().Set("X-Request-Id", requestID)
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(r.Context(), "http panic", "request_id", requestID, "error", recovered)
				WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "요청을 처리하지 못했습니다.")
			}
		}()
		requestContext := sharedapp.WithRequestID(r.Context(), requestID)
		requestContext = sharedapp.WithLocaleHint(requestContext, LocaleHintFromRequest(r))
		requestContext = runtimepolicy.WithWorkClass(
			requestContext, runtimepolicy.Interactive,
		)
		timeout := requestTimeout
		if len(timeoutForRequest) > 0 {
			if selected := timeoutForRequest[0](r); selected > 0 {
				timeout = selected
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(timeout + 5*time.Second))
			}
		}
		requestContext, cancel := runtimepolicy.WithTimeout(
			requestContext, timeout,
		)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(requestContext))
	})
}
