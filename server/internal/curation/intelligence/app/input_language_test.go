package app

import "testing"

func TestClassifyInputLanguagePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []string
		want   inputLanguagePath
	}{
		{name: "plain English", values: []string{"running shoes under $150"}, want: inputLanguageEnglishPassthrough},
		{name: "accented Latin", values: []string{"café grinder"}, want: inputLanguageEnglishPassthrough},
		{name: "product code URL and emoji", values: []string{"https://example.com/AB-1200 👟"}, want: inputLanguageEnglishPassthrough},
		{name: "no letters", values: []string{"$150 / 2 👟"}, want: inputLanguageEnglishPassthrough},
		{name: "Hangul", values: []string{"running shoes 런닝화"}, want: inputLanguageNormalizeToEnglish},
		{name: "Cyrillic", values: []string{"кроссовки"}, want: inputLanguageNormalizeToEnglish},
		{name: "Japanese", values: []string{"ランニング shoes"}, want: inputLanguageNormalizeToEnglish},
		{name: "Arabic in feedback", values: []string{"running shoes", "أخف"}, want: inputLanguageNormalizeToEnglish},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyInputLanguagePath(test.values...); got != test.want {
				t.Fatalf("classifyInputLanguagePath(%q) = %q, want %q", test.values, got, test.want)
			}
		})
	}
}
