package analytics

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// This opt-in check uses Google's validation endpoint only: it creates no
// reporting events and cannot prove API-secret validity or reporting receipt.
func TestGA4LiveValidation(t *testing.T) {
	if os.Getenv("VITLANE_GA4_VALIDATE") != "1" {
		t.Skip("set VITLANE_GA4_VALIDATE=1 with GA4 environment to validate synthetic packets")
	}
	config := Config{
		Mode: "ga4", MeasurementID: os.Getenv("GA4_MEASUREMENT_ID"),
		APISecret: os.Getenv("GA4_API_SECRET"), IdentityKey: os.Getenv("ANALYTICS_IDENTITY_KEY"),
		Release: "analytics-validation",
	}
	if err := config.Validate(true); err != nil {
		t.Fatal("required GA4 settings are missing or invalid")
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, locale := range []string{"ko-KR", "en-US"} {
		for _, name := range []string{"curation_created", "candidate_reacted", "cart_updated", "external_purchase_reported", "agency_order_issued"} {
			t.Run(locale+"/"+name, func(t *testing.T) {
				packet, ok := Build(config, Context{ClientID: "1234567890." + now, SessionID: now, Locale: locale},
					sharedapp.AnalyticsEvent{Name: name, UserID: "synthetic-validation-user", Key: "synthetic-validation-command",
						CurationID: "synthetic-validation-curation", Source: "SHOPIFY", Action: "SET"})
				if !ok {
					t.Fatal("synthetic packet rejected")
				}
				body, err := json.Marshal(struct {
					Packet
					ValidationBehavior string `json:"validation_behavior"`
				}{packet, "ENFORCE_RECOMMENDATIONS"})
				if err != nil {
					t.Fatal("packet serialization failed")
				}
				endpoint := "https://www.google-analytics.com/debug/mp/collect?" + url.Values{
					"measurement_id": {config.MeasurementID}, "api_secret": {config.APISecret},
				}.Encode()
				req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
				if err != nil {
					t.Fatal("validation request construction failed")
				}
				req.Header.Set("Content-Type", "application/json")
				response, err := client.Do(req)
				if err != nil {
					// HTTP errors contain the secret-bearing URL; never print err.
					t.Fatal("Google validation transport failed (details suppressed)")
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("Google validation HTTP status: %d", response.StatusCode)
				}
				var result struct {
					ValidationMessages *[]struct {
						Code string `json:"validationCode"`
					} `json:"validationMessages"`
				}
				if err := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result); err != nil || result.ValidationMessages == nil {
					t.Fatal("unexpected validation response")
				}
				if len(*result.ValidationMessages) != 0 {
					t.Fatalf("Google rejected packet: %d validation messages (contents suppressed)", len(*result.ValidationMessages))
				}
			})
		}
	}
}
