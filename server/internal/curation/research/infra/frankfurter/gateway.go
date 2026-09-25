package frankfurter

import (
	"context"
	"encoding/json"
	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	"io"
	"net/http"
	"time"
)

type Gateway struct{ Client *http.Client }

func (g Gateway) FetchExchangeRate(ctx context.Context) (researchdomain.DailyExchangeRate, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	result := researchdomain.DailyExchangeRate{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.frankfurter.dev/v2/rate/USD/KRW", nil)
	if err != nil {
		return result, researchdomain.ErrExchangeRate
	}
	client := *g.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := sharedhttpclient.Do(ctx, &client, req, sharedhttpclient.ReadOnly)
	if err != nil {
		return result, researchdomain.ErrExchangeRate
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return result, researchdomain.ErrExchangeRate
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(raw) > 16384 {
		return result, researchdomain.ErrExchangeRate
	}
	var body struct {
		Base  string      `json:"base"`
		Quote string      `json:"quote"`
		Date  string      `json:"date"`
		Rate  json.Number `json:"rate"`
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		return result, researchdomain.ErrExchangeRate
	}
	result = researchdomain.DailyExchangeRate{Base: body.Base, Quote: body.Quote, Rate: body.Rate.String(), AsOf: body.Date, ObservedAt: time.Now().UTC(), Source: "https://frankfurter.dev/"}
	if !result.Valid(result.ObservedAt) {
		return researchdomain.DailyExchangeRate{}, researchdomain.ErrExchangeRate
	}
	return result, nil
}
