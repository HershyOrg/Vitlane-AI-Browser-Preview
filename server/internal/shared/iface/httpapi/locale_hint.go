package httpapi

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// Marketing and App share these cookies under .vitlane.com (ADR-0080). The
// legacy vt_locale cookie was overwritten by every page view and is not read.
const (
	LocaleChoiceCookie = "vt_locale_choice"
	LocaleSeenCookie   = "vt_locale_seen"
)

func LocaleHintFromRequest(r *http.Request) sharedapp.LocaleHint {
	return sharedapp.LocaleHint{
		Choice:  localeCookie(r, LocaleChoiceCookie),
		Seen:    localeCookie(r, LocaleSeenCookie),
		Browser: BrowserLocale(r.Header.Get("Accept-Language")),
	}
}

func localeCookie(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil || !sharedapp.ValidUILocale(cookie.Value) {
		return ""
	}
	return cookie.Value
}

// BrowserLocale walks Accept-Language in preference order and returns ko-KR or
// en-US for whichever of Korean and English comes first, or "" for neither.
// Crawlers send no Accept-Language and therefore resolve to "".
func BrowserLocale(header string) string {
	type language struct {
		primary string
		quality float64
	}
	var languages []language
	for part := range strings.SplitSeq(header, ",") {
		fields := strings.Split(part, ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				parsed = 0
			}
			quality = parsed
		}
		if quality <= 0 {
			continue
		}
		primary, _, _ := strings.Cut(tag, "-")
		languages = append(languages, language{primary: primary, quality: quality})
	}
	slices.SortStableFunc(languages, func(a, b language) int {
		return cmp.Compare(b.quality, a.quality)
	})
	for _, language := range languages {
		switch language.primary {
		case "ko":
			return sharedapp.UILocaleKorean
		case "en":
			return sharedapp.UILocaleEnglish
		}
	}
	return ""
}
