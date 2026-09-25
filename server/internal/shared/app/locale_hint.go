package app

import "context"

const (
	UILocaleEnglish = "en-US"
	UILocaleKorean  = "ko-KR"
)

type localeHintContextKey struct{}

// LocaleHint is the browser's presentation-language evidence for one request
// (ADR-0080). It decides only what an unselected account sees and never enters
// a command body, idempotency key or stored selection.
type LocaleHint struct {
	// Choice is the latest explicit language selection made in this browser.
	Choice string
	// Seen is the language this browser last displayed Vitlane in.
	Seen string
	// Browser is the browser language preference: ko-KR when Korean precedes
	// English, en-US when English does, otherwise empty.
	Browser string
}

// UILocale resolves an explicit choice, then the last displayed language,
// then the browser preference, and otherwise English.
func (h LocaleHint) UILocale() string {
	for _, locale := range []string{h.Choice, h.Seen, h.Browser} {
		if ValidUILocale(locale) {
			return locale
		}
	}
	return UILocaleEnglish
}

// BrowserLocale is the browser preference alone. Regional defaults such as
// currency and research country follow it so that switching the display
// language never moves them.
func (h LocaleHint) BrowserLocale() string {
	if ValidUILocale(h.Browser) {
		return h.Browser
	}
	return UILocaleEnglish
}

func ValidUILocale(locale string) bool {
	return locale == UILocaleEnglish || locale == UILocaleKorean
}

func WithLocaleHint(ctx context.Context, hint LocaleHint) context.Context {
	return context.WithValue(ctx, localeHintContextKey{}, hint)
}

// LocaleHintFrom reports false outside a Web request, for example in a worker.
func LocaleHintFrom(ctx context.Context) (LocaleHint, bool) {
	hint, ok := ctx.Value(localeHintContextKey{}).(LocaleHint)
	return hint, ok
}
