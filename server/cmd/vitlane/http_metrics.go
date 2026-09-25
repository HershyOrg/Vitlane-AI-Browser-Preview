package main

import (
	"net/http"
	"sync"
	"time"
)

// httpMetrics keeps process-local request counters for the operator ops
// health readback (ADR-0040 §6). Facts only — alert thresholds live in the
// host watch layer, and nothing here identifies a user or a path.
type httpMetrics struct {
	mu                sync.Mutex
	totalRequests     uint64
	totalServerErrors uint64
	buckets           [minuteBucketCount]minuteBucket
}

const minuteBucketCount = 60

type minuteBucket struct {
	minute       int64
	requests     uint64
	serverErrors uint64
}

type httpMetricsSnapshot struct {
	TotalRequests       uint64 `json:"totalRequests"`
	TotalServerErrors   uint64 `json:"totalServerErrors"`
	RequestsLast5m      uint64 `json:"requestsLast5m"`
	ServerErrorsLast5m  uint64 `json:"serverErrorsLast5m"`
	RequestsLast60m     uint64 `json:"requestsLast60m"`
	ServerErrorsLast60m uint64 `json:"serverErrorsLast60m"`
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{}
}

func (m *httpMetrics) record(now time.Time, status int) {
	minute := now.Unix() / 60
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalRequests++
	bucket := &m.buckets[minute%minuteBucketCount]
	if bucket.minute != minute {
		*bucket = minuteBucket{minute: minute}
	}
	bucket.requests++
	if status >= 500 {
		m.totalServerErrors++
		bucket.serverErrors++
	}
}

func (m *httpMetrics) snapshot(now time.Time) httpMetricsSnapshot {
	minute := now.Unix() / 60
	m.mu.Lock()
	defer m.mu.Unlock()
	result := httpMetricsSnapshot{
		TotalRequests:     m.totalRequests,
		TotalServerErrors: m.totalServerErrors,
	}
	for _, bucket := range m.buckets {
		if bucket.minute == 0 || bucket.minute > minute {
			continue
		}
		age := minute - bucket.minute
		if age < 60 {
			result.RequestsLast60m += bucket.requests
			result.ServerErrorsLast60m += bucket.serverErrors
		}
		if age < 5 {
			result.RequestsLast5m += bucket.requests
			result.ServerErrorsLast5m += bucket.serverErrors
		}
	}
	return result
}

// Wrap counts every response the server writes, including middleware-written
// timeouts, so it belongs outside the shared request middleware.
func (m *httpMetrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		m.record(time.Now(), recorder.status)
	})
}

// statusRecorder exposes Unwrap so http.NewResponseController keeps reaching
// the underlying writer (WebSocket deadline release depends on this).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status = status
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(payload []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(payload)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
