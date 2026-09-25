package httpapi

import (
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOptionalAnalyticsNeverChangesBusinessExecution(t *testing.T) {
	for _, tc := range []struct {
		name, cookie, client string
		status, want         int
		operator             bool
	}{
		{"unknown", "", "1.2", 200, 0, false}, {"denied", "v1.denied", "1.2", 200, 0, false},
		{"allowed", "v1.allowed", "1.2", 201, 1, false}, {"missing SDK", "v1.allowed", "", 200, 0, false},
		{"forged identifier", "v1.allowed", "user@email.com", 200, 0, false},
		{"failed commit", "v1.allowed", "1.2", 500, 0, false}, {"conflict", "v1.allowed", "1.2", 409, 0, false},
		{"operator", "v1.allowed", "1.2", 200, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes, sent := 0, 0
			h := OptionalAnalytics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writes++
				ctx := r.Context()
				if tc.operator {
					ctx = sharedapp.WithoutAnalytics(ctx)
				}
				sharedapp.RecordAnalytics(ctx, sharedapp.AnalyticsEvent{Name: "curation_created", UserID: "user", Key: "command"})
				w.WriteHeader(tc.status)
			}), func(AnalyticsRequest, sharedapp.AnalyticsEvent) { sent++ })
			r := httptest.NewRequest("POST", "/api/v1/example", nil)
			if tc.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "vt_analytics", Value: tc.cookie})
			}
			r.Header.Set("X-Vitlane-Analytics-Client", tc.client)
			r.Header.Set("X-Vitlane-Analytics-Session", "123")
			r.Header.Set("X-Vitlane-Analytics-Locale", "ko-KR")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if writes != 1 || sent != tc.want || w.Code != tc.status {
				t.Fatalf("writes %d sent %d status %d", writes, sent, w.Code)
			}
		})
	}
}
func TestCollectorDoesNotInventEventsOrGrowWithoutBound(t *testing.T) {
	c := &analyticsCollector{}
	for i := 0; i < 100; i++ {
		c.Record(sharedapp.AnalyticsEvent{Name: "cart_updated"})
	}
	if len(c.snapshot()) != 16 {
		t.Fatal("unbounded")
	}
	sent := false
	h := OptionalAnalytics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), func(AnalyticsRequest, sharedapp.AnalyticsEvent) { sent = true })
	r := httptest.NewRequest("POST", "/", nil)
	r.AddCookie(&http.Cookie{Name: "vt_analytics", Value: "v1.allowed"})
	r.Header.Set("X-Vitlane-Analytics-Client", "1.2")
	r.Header.Set("X-Vitlane-Analytics-Session", "1")
	r.Header.Set("X-Vitlane-Analytics-Locale", "en-US")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if sent {
		t.Fatal("inferred an event from HTTP success")
	}
}
