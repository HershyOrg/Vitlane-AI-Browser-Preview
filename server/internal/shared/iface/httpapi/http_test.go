package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

type fixedIDs struct{}

func (fixedIDs) NewID() string { return "request-fixed" }

func TestMiddlewarePropagatesRequestIDClassAndDeadline(t *testing.T) {
	handler := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sharedapp.RequestID(r.Context()) != "request-fixed" {
			t.Errorf("request id not propagated")
		}
		if runtimepolicy.WorkClassFrom(r.Context()) != runtimepolicy.Interactive {
			t.Errorf("work class not propagated")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Errorf("request deadline missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}), fixedIDs{}, slog.Default(), time.Second)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Request-Id") != "request-fixed" {
		t.Fatalf("response request id = %q", response.Header().Get("X-Request-Id"))
	}
}

func TestConcurrentRequestLimitRejectsWithoutWaitingAndReleasesSlot(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var calls atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Middleware(
		LimitConcurrentRequests(next, 1),
		fixedIDs{}, slog.Default(), time.Second,
	)

	firstResponse := httptest.NewRecorder()
	go func() {
		defer close(done)
		handler.ServeHTTP(
			firstResponse, httptest.NewRequest(http.MethodGet, "/first", nil),
		)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not enter the handler")
	}

	overflowRequest := httptest.NewRequest(http.MethodGet, "/overflow", nil)
	overflowRequest.Header.Set("X-Request-Id", "request-overflow")
	overflowResponse := httptest.NewRecorder()
	handler.ServeHTTP(overflowResponse, overflowRequest)
	if overflowResponse.Code != http.StatusServiceUnavailable ||
		overflowResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf(
			"overflow status=%d Retry-After=%q",
			overflowResponse.Code, overflowResponse.Header().Get("Retry-After"),
		)
	}
	var decoded ErrorResponse
	if err := json.Unmarshal(overflowResponse.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.ReasonCode != "HTTP_CONCURRENCY_LIMIT_REACHED" ||
		!decoded.Error.Retryable || decoded.Error.RetryAfterSeconds != 1 ||
		decoded.Error.RequestID != "request-overflow" {
		t.Fatalf("overflow response = %#v", decoded.Error)
	}

	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	if firstResponse.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d", firstResponse.Code)
	}
	afterRelease := httptest.NewRecorder()
	handler.ServeHTTP(
		afterRelease, httptest.NewRequest(http.MethodGet, "/after-release", nil),
	)
	if afterRelease.Code != http.StatusNoContent {
		t.Fatalf("request after release status = %d", afterRelease.Code)
	}
}

func TestConcurrentRequestLimitReleasesSlotAfterPanic(t *testing.T) {
	var calls atomic.Int32
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			panic("test panic")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Middleware(
		LimitConcurrentRequests(next, 1),
		fixedIDs{}, slog.Default(), time.Second,
	)

	panicResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		panicResponse, httptest.NewRequest(http.MethodGet, "/panic", nil),
	)
	if panicResponse.Code != http.StatusInternalServerError {
		t.Fatalf("panic response status = %d", panicResponse.Code)
	}
	afterPanic := httptest.NewRecorder()
	handler.ServeHTTP(
		afterPanic, httptest.NewRequest(http.MethodGet, "/after-panic", nil),
	)
	if afterPanic.Code != http.StatusNoContent {
		t.Fatalf("request after panic status = %d", afterPanic.Code)
	}
}

func TestWriteFaultProducesMachineReadableRetryPolicy(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(sharedapp.WithRequestID(context.Background(), "request-1"))
	response := httptest.NewRecorder()
	failure := fault.New(fault.RateLimited, "PROVIDER_RATE_LIMITED", true)
	failure.RetryAfter = 1500 * time.Millisecond
	WriteFault(response, request, failure, "잠시 후 다시 시도해 주세요.")
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "2" {
		t.Fatalf("status=%d Retry-After=%q", response.Code, response.Header().Get("Retry-After"))
	}
	var decoded ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error.ReasonCode != "PROVIDER_RATE_LIMITED" ||
		!decoded.Error.Retryable || decoded.Error.RequestID != "request-1" {
		t.Fatalf("fault response = %#v", decoded.Error)
	}
}

func TestWriteErrorReflectsExistingRetryAfter(t *testing.T) {
	response := httptest.NewRecorder()
	response.Header().Set("Retry-After", "42")
	response.Header().Set("X-Request-Id", "request-legacy")
	WriteError(response, http.StatusTooManyRequests, "RATE_LIMITED", "잠시 후 다시 시도해 주세요.")
	var decoded ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Error.Retryable || decoded.Error.RetryAfterSeconds != 42 ||
		decoded.Error.RequestID != "request-legacy" {
		t.Fatalf("rate limit response = %#v", decoded.Error)
	}
}

func TestWriteFaultIfClassifiedDoesNotHideDatabaseTimeout(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	classified := fault.New(
		fault.DeadlineExceeded, "POSTGRES_STATEMENT_TIMEOUT", true,
	)
	if !WriteFaultIfClassified(response, request, classified, "요청 시간이 초과되었습니다.") {
		t.Fatal("classified fault was not written")
	}
	var decoded ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusGatewayTimeout ||
		decoded.Error.ReasonCode != "POSTGRES_STATEMENT_TIMEOUT" {
		t.Fatalf("response=%d %#v", response.Code, decoded.Error)
	}

	unclassified := httptest.NewRecorder()
	if WriteFaultIfClassified(
		unclassified, request, context.Canceled, "취소되었습니다.",
	) || unclassified.Body.Len() != 0 {
		t.Fatal("unclassified error consumed the legacy fallback")
	}
}
