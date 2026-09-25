package frankfurter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDailyRateAdapterValidatesPairAndPublicationDate(t *testing.T) {
	for _, pair := range []string{"KRW", "EUR"} {
		g := Gateway{Client: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.frankfurter.dev/v2/rate/USD/KRW" {
				t.Fatal("unexpected FX endpoint")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"base":"USD","quote":"` + pair + `","date":"` + time.Now().UTC().Format("2006-01-02") + `","rate":1340.18}`)), Header: http.Header{}}, nil
		})}}
		rate, err := g.FetchExchangeRate(context.Background())
		if pair == "KRW" && (err != nil || rate.Rate != "1340.18") {
			t.Fatal(rate, err)
		}
		if pair == "EUR" && err == nil {
			t.Fatal("unexpected currency accepted")
		}
	}
}
func TestLiveDailyRateAdapter(t *testing.T) {
	if os.Getenv("VITLANE_STEP2_LIVE_TEST") != "1" {
		t.Skip("explicit live test required")
	}
	g := Gateway{Client: &http.Client{Timeout: 9 * time.Second}}
	rate, err := g.FetchExchangeRate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("USD/KRW rate=%s asOf=%s", rate.Rate, rate.AsOf)
	if path := os.Getenv("VITLANE_STEP2_FX_EVIDENCE_PATH"); path != "" {
		raw, _ := json.MarshalIndent(rate, "", "  ")
		if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
