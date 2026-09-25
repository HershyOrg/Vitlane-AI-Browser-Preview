package dealfeed

import (
	"strings"
	"testing"
	"time"
)

func TestTelegramPreservesPriceAndOriginalIdentity(t *testing.T) {
	now := time.Now().UTC()
	html := `<div data-post="jirum/123"><div class="tgme_widget_message_text">특가 19,990원 무선 헤드폰 📌 정상가 30,000원 <a href="https://www.11st.co.kr/products/12345">원문</a></div><time datetime="` + now.Format(time.RFC3339) + `"></time></div>`
	items, e := ParseTelegram(html, now)
	if e != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, e)
	}
	p := items[0]
	if p.PriceMinor == nil || *p.PriceMinor != 19990 || p.ShippingMinor != nil || p.ProductRef == nil || p.Identity != "elevenst:KR:12345" {
		t.Fatalf("%+v", p)
	}
}
func TestTelegramUnresolvedLinkIsOnlyFindingNotCandidate(t *testing.T) {
	now := time.Now()
	items, e := ParseTelegram(`<div data-post="jirum/1"><div class="tgme_widget_message_text">특가 100원 상품 <a href="http://127.0.0.1">unsafe</a></div></div>`, now)
	if e != nil || len(items) != 1 || items[0].ProductRef != nil || items[0].URL != "https://t.me/jirum/1" {
		t.Fatalf("%+v %v", items, e)
	}
	if _, e = ParseTelegram("<html>login wall</html>", now); e == nil {
		t.Fatal("schema change silently accepted")
	}
}

func TestTelegramPreviewImageKeepsOnlyProviderPhotos(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct{ name, class, style, want string }{
		{"preview", "link_preview_image", `background-image:url('https://cdn4.telesco.pe/file/product.jpg')`, "https://cdn4.telesco.pe/file/product.jpg"},
		{"photo", "tgme_widget_message_photo_wrap", `background-image: url("https://cdn1.telesco.pe/file/photo.jpg")`, "https://cdn1.telesco.pe/file/photo.jpg"},
		{"avatar", "tgme_widget_message_user_photo", `background-image:url('https://cdn4.telesco.pe/file/avatar.jpg')`, ""},
		{"external", "link_preview_image", `background-image:url('https://example.test/photo.jpg')`, ""},
		{"fake suffix", "link_preview_image", `background-image:url('https://telesco.pe.example.test/photo.jpg')`, ""},
		{"local", "link_preview_image", `background-image:url('http://127.0.0.1/image')`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `<div data-post="jirum/1"><div class="tgme_widget_message_text">특가 100원 상품</div><i class="` + test.class + `" style="` + strings.ReplaceAll(test.style, `"`, `&quot;`) + `"></i></div>`
			products, err := ParseTelegram(body, now)
			if err != nil || len(products) != 1 || products[0].ImageURL != test.want {
				t.Fatalf("products=%+v err=%v", products, err)
			}
		})
	}
}
