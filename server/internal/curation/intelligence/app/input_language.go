package app

import "unicode"

type inputLanguagePath string

const (
	inputLanguageEnglishPassthrough inputLanguagePath = "ENGLISH_PASSTHROUGH"
	inputLanguageNormalizeToEnglish inputLanguagePath = "NORMALIZE_TO_ENGLISH"
)

// classifyInputLanguagePath is intentionally binary and deterministic. The
// original input is never changed here. Any Unicode letter outside the Latin
// script sends the model through the English-normalization instruction; all
// other input, including digits, punctuation, URLs and emoji, passes through.
func classifyInputLanguagePath(values ...string) inputLanguagePath {
	for _, value := range values {
		for _, current := range value {
			if unicode.IsLetter(current) && !unicode.In(current, unicode.Latin) {
				return inputLanguageNormalizeToEnglish
			}
		}
	}
	return inputLanguageEnglishPassthrough
}
