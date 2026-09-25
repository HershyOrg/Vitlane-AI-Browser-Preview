package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestReadBodyRejectsOverflow(t *testing.T) {
	if _, err := ReadBody(strings.NewReader("12345"), 4); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("overflow error = %v", err)
	}
	content, err := ReadBody(strings.NewReader("1234"), 4)
	if err != nil || string(content) != "1234" {
		t.Fatalf("bounded body = %q, %v", content, err)
	}
}

func TestTransportConfigRejectsUnsafeLimits(t *testing.T) {
	config := DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("default config: %v", err)
	}
	if config.ResponseHeaderTimeout != 20*time.Second {
		t.Fatalf("response header timeout = %s", config.ResponseHeaderTimeout)
	}
	config.MaxIdleConnectionsHost = config.MaxConnectionsHost + 1
	if err := config.Validate(); err == nil {
		t.Fatal("per-host idle connections exceeded the per-host pool")
	}
}

func TestNewClientDoesNotFollowRedirectsOrForwardCredentialsAndPayload(t *testing.T) {
	t.Parallel()
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var destinationCalls atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				r *http.Request,
			) {
				destinationCalls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(destination.Close)

			var sourceCalls atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(
				w http.ResponseWriter,
				r *http.Request,
			) {
				sourceCalls.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read source request: %v", err)
				}
				if r.Method != http.MethodPost ||
					r.Header.Get("Authorization") != "Bearer provider-secret" ||
					string(body) != `{"intent":"private buyer payload"}` {
					t.Errorf(
						"source request method=%q authorization=%q body=%q",
						r.Method, r.Header.Get("Authorization"), body,
					)
				}
				w.Header().Set("Location", destination.URL+"/redirect-target")
				w.WriteHeader(status)
			}))
			t.Cleanup(source.Close)

			client, err := NewClient(source.Client().Transport, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequest(
				http.MethodPost,
				source.URL+"/provider",
				strings.NewReader(`{"intent":"private buyer payload"}`),
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer provider-secret")
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("redirect response: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != status ||
				response.Request.URL.String() != source.URL+"/provider" {
				t.Fatalf(
					"response status=%d request URL=%q",
					response.StatusCode, response.Request.URL,
				)
			}
			if sourceCalls.Load() != 1 || destinationCalls.Load() != 0 {
				t.Fatalf(
					"source calls=%d destination calls=%d",
					sourceCalls.Load(), destinationCalls.Load(),
				)
			}
		})
	}
}

func TestDoClassifiesReadOnlyDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, "late")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	_, err := Do(ctx, server.Client(), request, ReadOnly)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.DeadlineExceeded || !failure.Retryable {
		t.Fatalf("deadline classification = %#v, %v", failure, err)
	}
}

func TestDoClassifiesWrittenExternalEffectAsUnknown(t *testing.T) {
	received := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(received)
		<-releaseHandler
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(
		ctx, http.MethodPost, server.URL, strings.NewReader("effect"),
	)
	errorChannel := make(chan error, 1)
	go func() {
		_, err := Do(ctx, server.Client(), request, ExternalEffect)
		errorChannel <- err
	}()
	<-received
	cancel()
	err := <-errorChannel
	close(releaseHandler)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable {
		t.Fatalf("effect classification = %#v, %v", failure, err)
	}
}

func TestStatusFaultCarriesRetryAfter(t *testing.T) {
	err := StatusFault(http.StatusTooManyRequests, "17", true)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.RateLimited || failure.RetryAfter != 17*time.Second {
		t.Fatalf("rate limit classification = %#v", failure)
	}
}
