package httpapi

import (
	"net/http"
	"regexp"
	"sync"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type AnalyticsRequest struct{ ClientID, SessionID, Locale string }
type AnalyticsSubmit func(AnalyticsRequest, sharedapp.AnalyticsEvent)

var analyticsClientID = regexp.MustCompile(`^[0-9]{1,20}\.[0-9]{1,20}$`)
var analyticsSessionID = regexp.MustCompile(`^[0-9]{1,20}$`)

// OptionalAnalytics buffers explicit app results until the whole HTTP command
// succeeds, including any outer transaction. It does not inspect bodies or infer
// success events from URLs, clicks or a generic HTTP 200.
func OptionalAnalytics(next http.Handler, submit AnalyticsSubmit) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("vt_analytics")
		meta := AnalyticsRequest{r.Header.Get("X-Vitlane-Analytics-Client"), r.Header.Get("X-Vitlane-Analytics-Session"), r.Header.Get("X-Vitlane-Analytics-Locale")}
		if submit == nil || err != nil || cookie.Value != "v1.allowed" || !analyticsClientID.MatchString(meta.ClientID) || !analyticsSessionID.MatchString(meta.SessionID) || (meta.Locale != "ko-KR" && meta.Locale != "en-US") {
			next.ServeHTTP(w, r)
			return
		}
		collector := &analyticsCollector{}
		writer := &analyticsResponse{ResponseWriter: w, status: 200}
		next.ServeHTTP(writer, r.WithContext(sharedapp.WithAnalyticsSink(r.Context(), collector)))
		if writer.status >= 200 && writer.status < 300 && r.Context().Err() == nil {
			for _, event := range collector.snapshot() {
				submit(meta, event)
			}
		}
	})
}

type analyticsCollector struct {
	mu     sync.Mutex
	events []sharedapp.AnalyticsEvent
}

func (c *analyticsCollector) Record(e sharedapp.AnalyticsEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) < 16 {
		c.events = append(c.events, e)
	}
}
func (c *analyticsCollector) snapshot() []sharedapp.AnalyticsEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sharedapp.AnalyticsEvent(nil), c.events...)
}

type analyticsResponse struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *analyticsResponse) WriteHeader(code int) {
	if w.written {
		return
	}
	if code >= 200 {
		w.status = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *analyticsResponse) Write(p []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *analyticsResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
