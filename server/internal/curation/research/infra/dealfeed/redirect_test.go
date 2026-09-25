package dealfeed

import (
	"context"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type trackedControl struct {
	a.CatalogProviderControlRepository
	reserved, finished int
	outcome            string
}

func (c *trackedControl) ReserveProviderCall(_ context.Context, source, operation string, _ time.Time) (string, error) {
	c.reserved++
	return "1", nil
}
func (c *trackedControl) CompleteProviderCall(_ context.Context, _ string, outcome string, _ int, _ int, _ time.Time) error {
	c.finished++
	c.outcome = outcome
	return nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTelegramBridgeOnlyParsesAllowlistedOriginalAndTracksCalls(t *testing.T) {
	for _, v := range []struct {
		location string
		ok       bool
	}{
		{"https://www.coupang.com/vp/products/7470273887?itemId=19491258982", true},
		{"https://127.0.0.1/private", false}, {"https://attacker.example/product/12345", false},
	} {
		c := &trackedControl{}
		calls := 0
		feed := &Telegram{Control: c, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Host != "link.coupang.com" {
				t.Fatal("followed untrusted redirect")
			}
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{v.location}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})}}
		ref, e := feed.resolveCoupang(context.Background(), "https://link.coupang.com/a/test")
		if (e == nil) != v.ok || calls != 1 || c.reserved != 1 || c.finished != 1 {
			t.Fatalf("ref=%+v err=%v calls=%d ledger=%+v", ref, e, calls, c)
		}
		if v.ok && ref.ProductID != "7470273887" {
			t.Fatalf("ref %+v", ref)
		}
		if _, e = feed.resolveCoupang(context.Background(), "http://127.0.0.1/private"); e == nil || calls != 1 {
			t.Fatal("untrusted bridge was requested")
		}
	}
}

func TestTelegramHTTPFailuresKeepSharedClassificationAndCloseLedger(t *testing.T) {
	for _, operation := range []string{"feed", "bridge"} {
		t.Run(operation, func(t *testing.T) {
			control := &trackedControl{}
			feed := &Telegram{Control: control, Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return nil, context.DeadlineExceeded
			})}}
			var err error
			if operation == "feed" {
				_, err = feed.Poll(context.Background())
			} else {
				_, err = feed.resolveCoupang(context.Background(), "https://link.coupang.com/a/test")
			}
			if fault.CodeOf(err) != fault.DeadlineExceeded || !fault.Retryable(err) {
				t.Fatalf("unclassified read failure: %v", err)
			}
			if control.reserved != 1 || control.finished != 1 || control.outcome != "CATALOG_NETWORK_FAILED" {
				t.Fatalf("unfinished failed provider call: %+v", control)
			}
		})
	}
}
