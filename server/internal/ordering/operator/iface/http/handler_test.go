package http

import (
	"net/http/httptest"
	"testing"
)

func TestRetryEffectRequiresAuthenticatedActor(t *testing.T) {
	h := NewHandler(nil)
	r := httptest.NewRequest("POST", "/", nil)
	w := httptest.NewRecorder()
	h.RetryProcessEffect(w, r)
	if w.Code != 401 {
		t.Fatalf("unauthenticated retry status=%d", w.Code)
	}
}
