// Package httpclient provides the one reviewed connection pool and failure
// boundary for outbound HTTP integrations.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

type Config struct {
	ConnectTimeout         time.Duration
	KeepAlive              time.Duration
	TLSHandshakeTimeout    time.Duration
	ResponseHeaderTimeout  time.Duration
	ExpectContinueTimeout  time.Duration
	IdleConnectionTimeout  time.Duration
	MaxIdleConnections     int
	MaxIdleConnectionsHost int
	MaxConnectionsHost     int
}

func DefaultConfig() Config {
	return Config{
		ConnectTimeout: 5 * time.Second, KeepAlive: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: time.Second, IdleConnectionTimeout: 90 * time.Second,
		MaxIdleConnections: 100, MaxIdleConnectionsHost: 10, MaxConnectionsHost: 20,
	}
}

func (c Config) Validate() error {
	if c.ConnectTimeout <= 0 || c.KeepAlive <= 0 || c.TLSHandshakeTimeout <= 0 ||
		c.ResponseHeaderTimeout <= 0 || c.ExpectContinueTimeout <= 0 ||
		c.IdleConnectionTimeout <= 0 {
		return fmt.Errorf("HTTP transport timeouts must be positive")
	}
	if c.ConnectTimeout > 30*time.Second || c.KeepAlive > 5*time.Minute ||
		c.TLSHandshakeTimeout > 30*time.Second ||
		c.ResponseHeaderTimeout > time.Minute ||
		c.ExpectContinueTimeout > 10*time.Second ||
		c.IdleConnectionTimeout > 10*time.Minute {
		return fmt.Errorf("HTTP transport timeouts exceed operational upper bounds")
	}
	if c.MaxIdleConnections <= 0 || c.MaxIdleConnectionsHost <= 0 ||
		c.MaxConnectionsHost <= 0 ||
		c.MaxIdleConnectionsHost > c.MaxConnectionsHost ||
		c.MaxConnectionsHost > c.MaxIdleConnections ||
		c.MaxIdleConnections > 10_000 || c.MaxConnectionsHost > 1_000 {
		return fmt.Errorf("HTTP transport connection limits are invalid")
	}
	return nil
}

func NewTransport(config Config) (*http.Transport, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	dialer := &net.Dialer{
		Timeout: config.ConnectTimeout, KeepAlive: config.KeepAlive,
	}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: dialer.DialContext,
		ForceAttemptHTTP2: true, TLSHandshakeTimeout: config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.ResponseHeaderTimeout,
		ExpectContinueTimeout: config.ExpectContinueTimeout,
		IdleConnTimeout:       config.IdleConnectionTimeout,
		MaxIdleConns:          config.MaxIdleConnections,
		MaxIdleConnsPerHost:   config.MaxIdleConnectionsHost,
		MaxConnsPerHost:       config.MaxConnectionsHost,
	}, nil
}

func NewClient(transport http.RoundTripper, timeout time.Duration) (*http.Client, error) {
	if transport == nil {
		return nil, fmt.Errorf("HTTP transport is required")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("HTTP client timeout must be positive")
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		// A redirect is another outbound request. Following it implicitly could
		// forward credentials or replay a POST body, and would also make one
		// admitted provider call perform multiple RoundTrips. Callers receive the
		// original 3xx response and classify it at their protocol boundary.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

type Effect int

const (
	ReadOnly Effect = iota
	ExternalEffect
)

// Do distinguishes a failure before the request was written from an ambiguous
// failure after a paid or side-effecting request left this process.
func Do(
	ctx context.Context,
	client *http.Client,
	request *http.Request,
	effect Effect,
) (*http.Response, error) {
	if client == nil || request == nil {
		return nil, fault.New(fault.InternalFailure, "HTTP_CLIENT_INVALID", false)
	}
	var wroteRequest atomic.Bool
	trace := &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest.Store(true) },
	}
	request = request.WithContext(httptrace.WithClientTrace(ctx, trace))
	response, err := client.Do(request)
	if err == nil {
		return response, nil
	}
	if effect == ExternalEffect && wroteRequest.Load() {
		return nil, fault.Wrap(err, fault.ExternalEffectUnknown, "HTTP_EFFECT_UNKNOWN", false)
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return nil, fault.Wrap(err, fault.CallerCancelled, "HTTP_CALLER_CANCELLED", false)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fault.Wrap(err, fault.DeadlineExceeded, "HTTP_DEADLINE_EXCEEDED", true)
	}
	return nil, fault.Wrap(err, fault.ProviderUnavailable, "HTTP_PROVIDER_UNAVAILABLE", true)
}

var ErrResponseTooLarge = errors.New("HTTP_RESPONSE_TOO_LARGE")

func ReadBody(body io.Reader, limit int64) ([]byte, error) {
	if body == nil || limit <= 0 {
		return nil, ErrResponseTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, ErrResponseTooLarge
	}
	return content, nil
}

func StatusFault(status int, retryAfter string, safeRetry bool) error {
	switch {
	case status == http.StatusTooManyRequests:
		value := fault.New(fault.RateLimited, "PROVIDER_RATE_LIMITED", safeRetry)
		value.RetryAfter = ParseRetryAfter(retryAfter, time.Now())
		return value
	case status >= 500:
		return fault.New(fault.ProviderUnavailable, "PROVIDER_HTTP_5XX", safeRetry)
	default:
		return fault.New(fault.ProviderRejected, "PROVIDER_HTTP_REJECTED", false)
	}
}

func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}
