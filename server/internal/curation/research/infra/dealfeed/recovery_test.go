package dealfeed

import (
	"context"
	"fmt"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func feedPage(high, count int) string {
	var b strings.Builder
	for id := high; id > high-count && id > 0; id-- {
		fmt.Fprintf(&b, `<div class="tgme_widget_message" data-post="jirum/%d"><div class="tgme_widget_message_text">Headphones 10,000원 <a href="https://link.coupang.com/a/item%d">Buy</a></div><time datetime="%s"></time></div>`, id, id, time.Now().UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, `<a href="/s/jirum?before=%d">Older</a>`, high-count+1)
	return b.String()
}
func TestTelegramScanResumesHundredPostWindowAndStopsAtLast(t *testing.T) {
	calls := 0
	feed := &Telegram{Control: &trackedControl{}, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		high := 250
		if v := r.URL.Query().Get("before"); v != "" {
			n, _ := strconv.Atoi(v)
			high = n - 1
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(feedPage(high, 20))), Header: http.Header{}, Request: r}, nil
	})}}
	first, err := feed.Scan(context.Background(), d.FeedCheckpoint{LastID: 100})
	if err != nil || calls != 5 || len(first.Products) != 100 || first.Checkpoint.LastID != 100 || first.Checkpoint.HeadID != 250 || first.Checkpoint.BeforeID != 151 {
		t.Fatalf("first=%+v calls=%d err=%v", first.Checkpoint, calls, err)
	}
	second, err := feed.Scan(context.Background(), first.Checkpoint)
	if err != nil || calls != 8 || len(second.Products) != 50 || second.Checkpoint.LastID != 250 || second.Checkpoint.HeadID != 0 || second.Checkpoint.BeforeID != 0 {
		t.Fatalf("second=%+v products=%d calls=%d err=%v", second.Checkpoint, len(second.Products), calls, err)
	}
	ids := map[string]bool{}
	for _, p := range append(first.Products, second.Products...) {
		if ids[p.ExternalID] {
			t.Fatal("duplicate post")
		}
		ids[p.ExternalID] = true
	}
	if len(ids) != 150 || len(first.Links) != 100 {
		t.Fatal("missing durable product/link")
	}
}
func TestTelegramScanBootstrapAndPartialFailure(t *testing.T) {
	for _, bootstrap := range []bool{true, false} {
		calls := 0
		feed := &Telegram{Control: &trackedControl{}, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 2 {
				return nil, context.DeadlineExceeded
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(feedPage(150, 20))), Header: http.Header{}, Request: r}, nil
		})}}
		cp := d.FeedCheckpoint{LastID: 100}
		if bootstrap {
			cp = d.FeedCheckpoint{}
		}
		batch, err := feed.Scan(context.Background(), cp)
		if bootstrap {
			if err != nil || calls != 1 || batch.Checkpoint.LastID != 150 {
				t.Fatalf("bootstrap=%+v %v", batch, err)
			}
		} else if err == nil || len(batch.Products) != 0 || batch.Checkpoint.LastID != 0 {
			t.Fatal("partial fetch returned committable progress")
		}
	}
}
func TestTelegramScanCountsInvalidPostsAndHonorsFivePages(t *testing.T) {
	calls := 0
	feed := &Telegram{Control: &trackedControl{}, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		high := 200
		if v := r.URL.Query().Get("before"); v != "" {
			n, _ := strconv.Atoi(v)
			high = n - 1
		}
		body := fmt.Sprintf(`<div class="tgme_widget_message" data-post="jirum/%d"></div><a href="/s/jirum?before=%d">Older</a>`, high, high)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})}}
	b, err := feed.Scan(context.Background(), d.FeedCheckpoint{LastID: 100})
	if err != nil || calls != 5 || len(b.Products) != 0 || b.Checkpoint.LastID != 100 || b.Checkpoint.BeforeID != 196 {
		t.Fatalf("%+v %v calls=%d", b, err, calls)
	}
}
func TestTelegramLinkRetryClassification(t *testing.T) {
	for _, status := range []int{404, 429, 503} {
		calls := 0
		feed := &Telegram{Control: &trackedControl{}, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"7200"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})}}
		result := feed.ResolveLink(context.Background(), "https://link.coupang.com/a/test")
		if calls != 1 || !result.Attempted || result.Retryable != (status != 404) || (status == 429 && result.RetryAfter != 2*time.Hour) {
			t.Fatalf("%d: %+v calls=%d", status, result, calls)
		}
	}
}
