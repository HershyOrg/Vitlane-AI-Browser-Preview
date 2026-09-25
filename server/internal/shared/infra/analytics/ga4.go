// Package analytics sends optional, consented behavior events. Business metrics
// are read from existing records and never enter this transport.
package analytics

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

type Config struct {
	Mode          string
	MeasurementID string
	APISecret     string
	IdentityKey   string
	Release       string
}

var measurementID = regexp.MustCompile(`^G-[A-Z0-9]{4,20}$`)
var clientID = regexp.MustCompile(`^[0-9]{1,20}\.[0-9]{1,20}$`)
var sessionID = regexp.MustCompile(`^[0-9]{1,20}$`)
var safeRelease = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

func (c Config) Validate(production bool) error {
	switch c.Mode {
	case "", "disabled":
		return nil
	case "debug":
		if production {
			return fmt.Errorf("analytics debug mode is development-only")
		}
	case "ga4":
		if !production {
			return fmt.Errorf("GA4 export is production-only; use debug for local verification")
		}
		if !measurementID.MatchString(c.MeasurementID) || strings.TrimSpace(c.APISecret) == "" || len(c.IdentityKey) < 32 {
			return fmt.Errorf("GA4 requires measurement ID, API secret and an identity key of at least 32 bytes")
		}
	default:
		return fmt.Errorf("invalid analytics mode")
	}
	return nil
}
func (c Config) UserID(user string) string {
	if c.Mode != "ga4" || user == "" {
		return ""
	}
	return c.Hash("user", user)
}
func (c Config) Hash(kind, value string) string {
	m := hmac.New(sha256.New, []byte(c.IdentityKey))
	m.Write([]byte("vitlane.analytics.v1\x00" + kind + "\x00" + value))
	return hex.EncodeToString(m.Sum(nil))
}
func (c Config) Public() map[string]any {
	mode := c.Mode
	if mode == "" {
		mode = "disabled"
	}
	release := c.Release
	if !safeRelease.MatchString(release) {
		release = "unknown"
	}
	id := ""
	if mode == "ga4" {
		id = c.MeasurementID
	}
	return map[string]any{"schemaVersion": "vitlane.analytics-config.v1", "mode": mode, "measurementId": id, "release": release}
}

type Packet struct {
	ClientID string            `json:"client_id"`
	UserID   string            `json:"user_id"`
	Consent  map[string]string `json:"consent"`
	Events   []Event           `json:"events"`
}
type Event struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}
type Context struct{ ClientID, SessionID, Locale string }

func Build(c Config, meta Context, e sharedapp.AnalyticsEvent) (Packet, bool) {
	if c.Mode != "ga4" || !clientID.MatchString(meta.ClientID) || !sessionID.MatchString(meta.SessionID) || e.UserID == "" || e.Key == "" {
		return Packet{}, false
	}
	switch e.Name {
	case "curation_created", "candidate_reacted", "cart_updated", "external_purchase_reported", "agency_order_issued":
	default:
		return Packet{}, false
	}
	if meta.Locale != "ko-KR" && meta.Locale != "en-US" {
		return Packet{}, false
	}
	session, err := strconv.ParseInt(meta.SessionID, 10, 64)
	if err != nil || session <= 0 {
		return Packet{}, false
	}
	eventID := c.Hash("event", e.Name+"\x00"+e.UserID+"\x00"+e.Key)
	params := map[string]any{"schema_version": 1, "event_id": eventID, "event_key": eventID, "surface": "app", "emitter": "server", "ui_locale": meta.Locale, "session_id": session}
	if safeRelease.MatchString(c.Release) {
		params["release"] = c.Release
	}
	if e.CurationID != "" {
		params["curation_key"] = c.Hash("curation", e.CurationID)
	}
	switch e.Source {
	case "SHOPIFY", "AMAZON", "COUPANG", "ELEVENST":
		params["source"] = e.Source
	}
	switch e.Action {
	case "LIKE", "DISLIKE", "NONE", "PIN", "UNPIN", "SET", "REPORTED", "RETRACTED", "PAYPAL_LIVE", "PAYPAL_SANDBOX", "TVITUSD":
		params["action"] = e.Action
	}
	return Packet{ClientID: meta.ClientID, UserID: c.UserID(e.UserID), Consent: map[string]string{"ad_user_data": "DENIED", "ad_personalization": "DENIED"}, Events: []Event{{Name: e.Name, Params: params}}}, true
}

// Sender has a bounded, process-local queue. No retries or durable storage:
// ambiguous transport failures must not silently multiply GA4 events.
type Sender struct {
	config  Config
	client  *http.Client
	queue   chan Packet
	dropped atomic.Uint64
	sent    atomic.Uint64
	failed  atomic.Uint64
}

func New(c Config, client *http.Client) *Sender {
	return &Sender{config: c, client: client, queue: make(chan Packet, 128)}
}
func (s *Sender) Submit(meta Context, e sharedapp.AnalyticsEvent) {
	if p, ok := Build(s.config, meta, e); ok {
		select {
		case s.queue <- p:
		default:
			s.dropped.Add(1)
		}
	}
}
func (s *Sender) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-s.queue:
			body, err := json.Marshal(p)
			if err != nil {
				s.failed.Add(1)
				continue
			}
			endpoint := "https://www.google-analytics.com/mp/collect?" + url.Values{"measurement_id": {s.config.MeasurementID}, "api_secret": {s.config.APISecret}}.Encode()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
			if err != nil {
				s.failed.Add(1)
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := sharedhttpclient.Do(ctx, s.client, req, sharedhttpclient.ExternalEffect)
			if err != nil {
				s.failed.Add(1)
				continue
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				s.sent.Add(1)
			} else {
				s.failed.Add(1)
			}
		}
	}
}
func (s *Sender) Stats() map[string]uint64 {
	return map[string]uint64{"sent": s.sent.Load(), "failed": s.failed.Load(), "dropped": s.dropped.Load()}
}
