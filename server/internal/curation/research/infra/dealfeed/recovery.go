package dealfeed

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	"golang.org/x/net/html"
)

// Poll remains a raw feed adapter entry; production uses Scan + durable link jobs.
func (t *Telegram) Poll(ctx context.Context) ([]d.DealProduct, error) {
	batch, err := t.Scan(ctx, d.FeedCheckpoint{})
	return batch.Products, err
}
func (t *Telegram) readPage(ctx context.Context, before int64) (string, error) {
	id, err := t.Control.ReserveProviderCall(ctx, t.Name(), "FEED_PAGE", time.Now())
	if err != nil {
		return "", err
	}
	outcome, status, retry := "CATALOG_NETWORK_FAILED", 0, 0
	defer func() {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = t.Control.CompleteProviderCall(finish, id, outcome, status, retry, time.Now())
	}()
	address := "https://t.me/s/jirum"
	if before > 0 {
		address += "?before=" + strconv.FormatInt(before, 10)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Vitlane/1.0 (public deal feed)")
	client := *t.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := sharedhttpclient.Do(ctx, &client, req, sharedhttpclient.ReadOnly)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	status = res.StatusCode
	if status != http.StatusOK {
		outcome = "CATALOG_UPSTREAM_FAILED"
		e := sharedhttpclient.StatusFault(status, res.Header.Get("Retry-After"), true)
		if f, ok := fault.As(e); ok {
			retry = int(f.RetryAfter.Seconds())
		}
		return "", e
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return "", err
	}
	if len(body) > 2<<20 {
		outcome = "CATALOG_SCHEMA_MISMATCH"
		return "", fault.New(fault.ProviderRejected, "TELEGRAM_FEED_TOO_LARGE", false)
	}
	outcome = "SUCCESS"
	return string(body), nil
}
func (t *Telegram) Scan(ctx context.Context, checkpoint d.FeedCheckpoint) (d.FeedBatch, error) {
	batch := d.FeedBatch{Checkpoint: checkpoint, Products: []d.DealProduct{}, Links: map[string]string{}}
	seen := map[int64]bool{}
	bootstrap := checkpoint.LastID == 0 && checkpoint.HeadID == 0
	finish := func() {
		batch.Checkpoint.LastID = max(checkpoint.LastID, batch.Checkpoint.HeadID)
		batch.Checkpoint.HeadID = 0
		batch.Checkpoint.BeforeID = 0
	}
	for page := 0; page < 5 && len(seen) < 100; page++ {
		body, err := t.readPage(ctx, batch.Checkpoint.BeforeID)
		if err != nil {
			return d.FeedBatch{}, err
		} // No progress is committed on partial fetch failure.
		products, err := ParseTelegram(body, time.Now().UTC())
		if err != nil {
			return d.FeedBatch{}, err
		}
		byID := map[string]d.DealProduct{}
		for _, p := range products {
			byID[p.ExternalID] = p
		}
		tree, err := html.Parse(strings.NewReader(body))
		if err != nil {
			return d.FeedBatch{}, err
		}
		ids := []int64{}
		links := map[string]string{}
		walk(tree, func(n *html.Node) {
			post := attr(n, "data-post")
			if !strings.HasPrefix(post, "jirum/") {
				return
			}
			id, err := strconv.ParseInt(strings.TrimPrefix(post, "jirum/"), 10, 64)
			if err != nil || id <= 0 {
				return
			}
			ids = append(ids, id)
			walk(n, func(el *html.Node) {
				raw := attr(el, "href")
				if d.FeedLinkURLAllowed(t.Name(), raw) {
					links[post] = raw
				}
			})
		})
		sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
		if len(ids) == 0 {
			finish()
			return batch, nil
		}
		if batch.Checkpoint.HeadID == 0 {
			batch.Checkpoint.HeadID = ids[0]
		}
		before := batch.Checkpoint.BeforeID
		for _, id := range ids {
			if id <= checkpoint.LastID {
				finish()
				return batch, nil
			}
			if seen[id] || (before > 0 && id >= before) || id > batch.Checkpoint.HeadID {
				continue
			}
			seen[id] = true
			batch.Checkpoint.BeforeID = id
			post := "jirum/" + strconv.FormatInt(id, 10)
			if p, ok := byID[post]; ok {
				batch.Products = append(batch.Products, p)
				if p.ProductRef == nil && links[post] != "" {
					batch.Links[post] = links[post]
				}
			}
			if len(seen) == 100 {
				return batch, nil
			}
		}
		if bootstrap {
			finish()
			return batch, nil
		} // Start at the current page, not an unbounded historical import.
		if before > 0 && batch.Checkpoint.BeforeID >= before {
			return d.FeedBatch{}, fault.New(fault.ProviderRejected, "TELEGRAM_CURSOR_STALLED", false)
		}
		hasOlder := false
		walk(tree, func(n *html.Node) {
			u, err := url.Parse(attr(n, "href"))
			if err != nil {
				return
			}
			if u.Host != "" && u.Host != "t.me" {
				return
			}
			if u.Path != "/s/jirum" {
				return
			}
			cursorID, err := strconv.ParseInt(u.Query().Get("before"), 10, 64)
			if err == nil && cursorID > 0 && cursorID <= batch.Checkpoint.BeforeID {
				hasOlder = true
			}
		})
		if !hasOlder {
			finish()
			return batch, nil
		}
	}
	return batch, nil
}

func (t *Telegram) ResolveLink(ctx context.Context, raw string) d.FeedLinkResult {
	ref, err, attempted := t.resolveLinkAttempt(ctx, raw)
	result := d.FeedLinkResult{Attempted: attempted}
	if err == nil {
		result.ProductRef = &ref
		result.Reason = "SUCCESS"
		return result
	}
	result.Reason = "TELEGRAM_LINK_FAILED"
	if f, ok := fault.As(err); ok {
		result.Reason = f.Reason
		result.Retryable = f.Retryable
		result.RetryAfter = f.RetryAfter
	}
	// Local denial is postponed, not a consumed attempt or permanent link failure.
	if !attempted {
		result.Retryable = true
	}
	return result
}
func (t *Telegram) resolveCoupang(ctx context.Context, raw string) (d.SourceProductRef, error) {
	ref, err, _ := t.resolveLinkAttempt(ctx, raw)
	return ref, err
}
func (t *Telegram) resolveLinkAttempt(ctx context.Context, raw string) (d.SourceProductRef, error, bool) {
	empty := d.SourceProductRef{}
	if !d.FeedLinkURLAllowed(t.Name(), raw) {
		return empty, fault.New(fault.InvalidInput, "TELEGRAM_LINK_INVALID", false), false
	}
	id, err := t.Control.ReserveProviderCall(ctx, t.Name(), "LINK_RESOLVE", time.Now())
	if err != nil {
		return empty, err, false
	}
	status, retry := 0, 0
	outcome := "CATALOG_NETWORK_FAILED"
	defer func() {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = t.Control.CompleteProviderCall(finish, id, outcome, status, retry, time.Now())
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return empty, err, false
	}
	req.Header.Set("User-Agent", "Vitlane/1.0 (public deal feed)")
	client := *t.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := sharedhttpclient.Do(ctx, &client, req, sharedhttpclient.ReadOnly)
	if err != nil {
		return empty, err, true
	}
	defer res.Body.Close()
	status = res.StatusCode
	if status < 300 || status >= 400 {
		outcome = "CATALOG_UPSTREAM_FAILED"
		e := sharedhttpclient.StatusFault(status, res.Header.Get("Retry-After"), true)
		if f, ok := fault.As(e); ok {
			retry = int(f.RetryAfter.Seconds())
		}
		return empty, e, true
	}
	ref, err := d.SourceProductFromURL(res.Header.Get("Location"))
	if err != nil || ref.Source != d.SourceCoupang {
		outcome = "CATALOG_SCHEMA_MISMATCH"
		return empty, fault.New(fault.ProviderRejected, "TELEGRAM_LINK_UNRESOLVED", false), true
	}
	outcome = "SUCCESS"
	return ref, nil, true
}
