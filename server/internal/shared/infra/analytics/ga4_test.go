package analytics

import (
	"context"
	"encoding/json"
	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func fixture() (Config, Context, sharedapp.AnalyticsEvent) {
	return Config{Mode: "ga4", MeasurementID: "G-TEST1234", APISecret: "private-secret", IdentityKey: strings.Repeat("a", 32), Release: "release-1"}, Context{"123.456", "1234567890", "ko-KR"}, sharedapp.AnalyticsEvent{Name: "curation_created", UserID: "private-user", Key: "private-command", CurationID: "private-curation"}
}
func TestPacketPrivacyAndDeterministicDedup(t *testing.T) {
	c, m, e := fixture()
	p, ok := Build(c, m, e)
	if !ok {
		t.Fatal("valid rejected")
	}
	second, _ := Build(c, m, e)
	if p.Events[0].Params["event_key"] != p.Events[0].Params["event_id"] {
		t.Fatal("export key must preserve legacy event identity")
	}
	if p.Events[0].Params["event_id"] != second.Events[0].Params["event_id"] {
		t.Fatal("unstable event id")
	}
	raw, _ := json.Marshal(p)
	for _, secret := range []string{"private-user", "private-command", "private-curation", "private-secret", c.IdentityKey} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("raw identifier or secret leaked")
		}
	}
	if p.UserID == "" || p.UserID != c.UserID(e.UserID) || p.Consent["ad_user_data"] != "DENIED" {
		t.Fatal("identity/consent")
	}
	e.Source = "private merchant"
	e.Action = "private user input"
	p, _ = Build(c, m, e)
	if _, ok := p.Events[0].Params["source"]; ok {
		t.Fatal("unbounded source")
	}
	if _, ok := p.Events[0].Params["action"]; ok {
		t.Fatal("unbounded action")
	}
	if _, ok := p.Events[0].Params["engagement_time_msec"]; ok {
		t.Fatal("server is not user engagement")
	}
}
func TestConfigAndInputGates(t *testing.T) {
	c, m, e := fixture()
	if c.Validate(true) != nil || c.Validate(false) == nil {
		t.Fatal("environment gate")
	}
	for _, mode := range []string{"disabled", "debug"} {
		d := c
		d.Mode = mode
		if _, ok := Build(d, m, e); ok {
			t.Fatal(mode)
		}
	}
	for _, name := range []string{"ui_click", "purchase", "arbitrary"} {
		bad := e
		bad.Name = name
		if _, ok := Build(c, m, bad); ok {
			t.Fatal(name)
		}
	}
	for _, bad := range []Context{{"email@example.com", "12", "ko-KR"}, {"1.2", "0", "ko-KR"}, {"1.2", "99999999999999999999", "ko-KR"}, {"1.2", "12", "fr"}} {
		if _, ok := Build(c, bad, e); ok {
			t.Fatalf("accepted %+v", bad)
		}
	}
	c.Mode = "debug"
	if c.Validate(true) == nil {
		t.Fatal("production debug")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestQueueIsBoundedAndTransportFailureNeverReachesCommand(t *testing.T) {
	c, m, e := fixture()
	called := make(chan bool, 1)
	client, _ := sharedhttpclient.NewClient(roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.google-analytics.com" || r.URL.Query().Get("api_secret") != c.APISecret {
			t.Error("wrong destination")
		}
		select {
		case called <- true:
		default:
		}
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(""))}, nil
	}), time.Second)
	s := New(c, client)
	for i := 0; i < 140; i++ {
		s.Submit(m, e)
	}
	if s.Stats()["dropped"] != 12 {
		t.Fatal(s.Stats())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() { s.Run(ctx); done <- true }()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("no send")
	}
	cancel()
	<-done
	if s.Stats()["failed"] == 0 {
		t.Fatal("failure not counted")
	}
}
