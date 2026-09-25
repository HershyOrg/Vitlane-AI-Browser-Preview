package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPMetricsWindowsAndTotals(t *testing.T) {
	metrics := newHTTPMetrics()
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	metrics.record(base.Add(-70*time.Minute), 500) // outside both windows
	metrics.record(base.Add(-30*time.Minute), 200) // 60m window only
	metrics.record(base.Add(-30*time.Minute), 503) // 60m window only
	metrics.record(base.Add(-2*time.Minute), 200)  // both windows
	metrics.record(base.Add(-2*time.Minute), 500)  // both windows

	snapshot := metrics.snapshot(base)
	if snapshot.TotalRequests != 5 || snapshot.TotalServerErrors != 3 {
		t.Fatalf("totals=%#v", snapshot)
	}
	if snapshot.RequestsLast5m != 2 || snapshot.ServerErrorsLast5m != 1 {
		t.Fatalf("5m window=%#v", snapshot)
	}
	if snapshot.RequestsLast60m != 4 || snapshot.ServerErrorsLast60m != 2 {
		t.Fatalf("60m window=%#v", snapshot)
	}
}

func TestHTTPMetricsWrapCountsAndUnwraps(t *testing.T) {
	metrics := newHTTPMetrics()
	var seen http.ResponseWriter
	handler := metrics.Wrap(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			seen = w
			w.WriteHeader(http.StatusBadGateway)
		},
	))
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest("GET", "/", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	// The recorder must expose the underlying writer so
	// http.NewResponseController keeps working (WebSocket deadlines).
	recorder, ok := seen.(interface{ Unwrap() http.ResponseWriter })
	if !ok {
		t.Fatal("status recorder does not expose Unwrap")
	}
	if recorder.Unwrap() == nil || recorder.Unwrap() == seen {
		t.Fatal("status recorder unwrap does not reach the underlying writer")
	}

	snapshot := metrics.snapshot(time.Now())
	if snapshot.TotalRequests != 2 || snapshot.TotalServerErrors != 2 {
		t.Fatalf("wrap counting failed: %#v", snapshot)
	}
	if first.Code != http.StatusBadGateway {
		t.Fatalf("wrapped status=%d", first.Code)
	}
}
