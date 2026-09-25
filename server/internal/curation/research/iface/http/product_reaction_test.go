package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	researchapp "github.com/vitlane/vitlane/server/internal/curation/research/app"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
)

type productReactionHTTPFake struct {
	catalogWorkspaceServiceV2
	input researchapp.SaveProductReactionInput
	calls int
}

func (s *productReactionHTTPFake) SaveProductReaction(_ context.Context, input researchapp.SaveProductReactionInput) (researchdomain.ProductReaction, error) {
	s.input = input
	s.calls++
	return researchdomain.ProductReaction{Version: 1}, nil
}
func TestProductReactionHTTPRequiresVersionAndPreservesAuthenticatedIdentity(t *testing.T) {
	valid := `{"schemaVersion":"vitlane.product-reaction.v1","productRef":{"source":"ELEVENST","marketplace":"KR","productId":"12345"},"pinned":true,"sentiment":"LIKE","expectedVersion":0}`
	for _, body := range []string{valid, strings.Replace(valid, `,"expectedVersion":0`, "", 1), strings.Replace(valid, `"pinned":true,`, "", 1)} {
		s := &productReactionHTTPFake{}
		h := &LiveCatalogReviewHandlerV2{workspace: s}
		r := catalogWorkspaceHTTPRequestV2(body, "")
		r.Method = http.MethodPut
		r.SetPathValue("candidateId", "candidate")
		w := httptest.NewRecorder()
		h.ProductReaction(w, r)
		if body == valid {
			if w.Code != 200 || s.calls != 1 || s.input.UserID != "user-1" || s.input.CandidateID != "candidate" {
				t.Fatalf("status=%d input=%+v", w.Code, s.input)
			}
		} else if w.Code != 400 || s.calls != 0 {
			t.Fatalf("invalid status=%d calls=%d", w.Code, s.calls)
		}
	}
	s := &productReactionHTTPFake{}
	w := httptest.NewRecorder()
	(&LiveCatalogReviewHandlerV2{workspace: s}).ProductReaction(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(valid)))
	if w.Code != 401 || s.calls != 0 {
		t.Fatalf("anonymous status=%d calls=%d", w.Code, s.calls)
	}
}
