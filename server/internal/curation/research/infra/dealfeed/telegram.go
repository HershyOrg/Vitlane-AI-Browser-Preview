// Package dealfeed reads shared public deal feeds, never subscriber queries.
package dealfeed

import (
	"fmt"
	a "github.com/vitlane/vitlane/server/internal/curation/research/app"
	d "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	"golang.org/x/net/html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Telegram struct {
	Client  *http.Client
	Control a.CatalogProviderControlRepository
}

func (*Telegram) Name() string { return "TELEGRAM_JIRUM" }
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func text(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	s := ""
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s += text(c) + " "
	}
	return strings.Join(strings.Fields(s), " ")
}
func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

var won = regexp.MustCompile(`([0-9][0-9,]*)\s*원`)
var previewImage = regexp.MustCompile(`(?i)background-image\s*:\s*url\(\s*['"]?(https://[^'"\)\s]+)['"]?\s*\)`)

// Keep the provider's preview URL only. The server does not fetch image bytes.
func telegramImage(n *html.Node) string {
	class := attr(n, "class")
	if !strings.Contains(class, "tgme_widget_message_photo_wrap") && !strings.Contains(class, "link_preview_image") {
		return ""
	}
	match := previewImage.FindStringSubmatch(attr(n, "style"))
	if len(match) != 2 || len(match[1]) > 2048 {
		return ""
	}
	u, err := url.Parse(match[1])
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "telesco.pe" && !strings.HasSuffix(host, ".telesco.pe") {
		return ""
	}
	return u.String()
}

func ParseTelegram(body string, now time.Time) ([]d.DealProduct, error) {
	tree, e := html.Parse(strings.NewReader(body))
	if e != nil {
		return nil, e
	}
	out := []d.DealProduct{}
	walk(tree, func(n *html.Node) {
		post := attr(n, "data-post")
		if !strings.HasPrefix(post, "jirum/") {
			return
		}
		p := d.DealProduct{SchemaVersion: "vitlane.deal-product.v1", Provider: "TELEGRAM_JIRUM", ExternalID: post, Identity: "telegram:" + post, URL: "https://t.me/" + post, Country: "KR", Currency: "KRW", ObservedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
		walk(n, func(el *html.Node) {
			if p.ImageURL == "" {
				p.ImageURL = telegramImage(el)
			}
			if strings.Contains(attr(el, "class"), "tgme_widget_message_text") {
				content := text(el)
				p.Description = content
				title := strings.Split(content, "📌")[0]
				if title == "" {
					title = content
				}
				p.Title = title
				if m := won.FindStringSubmatch(content); len(m) == 2 {
					if price, e := strconv.ParseInt(strings.ReplaceAll(m[1], ",", ""), 10, 64); e == nil {
						p.PriceMinor = &price
					}
				}
				walk(el, func(link *html.Node) {
					raw := attr(link, "href")
					if ref, e := d.SourceProductFromURL(raw); e == nil {
						u, _ := ref.ExternalProductURL()
						p.ProductRef = &ref
						p.URL = u
						p.Identity = ref.IdentityKey()
					}
				})
			}
			if el.Data == "time" {
				if at, e := time.Parse(time.RFC3339, attr(el, "datetime")); e == nil {
					p.ObservedAt = at
					p.ExpiresAt = at.Add(24 * time.Hour)
				}
			}
		})
		// Oversized or malformed posts never become model input.
		if p.Valid(now) && len(out) < 100 {
			out = append(out, p)
		}
	})
	if len(out) == 0 && !strings.Contains(body, "tgme_widget_message") {
		return nil, fmt.Errorf("TELEGRAM_FEED_SCHEMA_CHANGED")
	}
	return out, nil
}
