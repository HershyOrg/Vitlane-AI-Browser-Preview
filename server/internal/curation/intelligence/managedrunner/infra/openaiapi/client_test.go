package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func testModel() runnerdomain.Model {
	return runnerdomain.Model{
		Key: "test", ProviderModelID: "provider-model",
		InputMicrosPerMTok: 1, OutputMicrosPerMTok: 1,
		MaxOutputTokens: 100,
	}
}

func TestCompleteCarriesStableCorrelationAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Client-Request-Id") != "request-key" {
			t.Errorf("request key = %q", r.Header.Get("X-Client-Request-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", server.Client())
	result, err := client.Complete(context.Background(), runnerapp.ModelRequest{
		Model: testModel(), RequestKey: "request-key",
	})
	if err != nil || result.Content != "{}" || result.Usage.InputTokens != 3 ||
		result.Usage.OutputTokens != 2 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCompleteTreatsWrittenTimeoutAsUnknownEffect(t *testing.T) {
	received := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(received)
		<-releaseHandler
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(server.URL, "secret", server.Client())
	errorChannel := make(chan error, 1)
	go func() {
		_, err := client.Complete(ctx, runnerapp.ModelRequest{Model: testModel()})
		errorChannel <- err
	}()
	<-received
	cancel()
	err := <-errorChannel
	close(releaseHandler)
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable {
		t.Fatalf("classification=%#v err=%v", failure, err)
	}
}

func TestCompleteRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", server.Client())
	_, err := client.Complete(context.Background(), runnerapp.ModelRequest{Model: testModel()})
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable ||
		!errors.Is(err, runnerdomain.ErrModelResponse) {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestCompleteTreatsProvider5xxAsUnknownEffect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"sensitive provider text"}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "secret", server.Client())
	_, err := client.Complete(context.Background(), runnerapp.ModelRequest{Model: testModel()})
	failure, ok := fault.As(err)
	if !ok || failure.Code != fault.ExternalEffectUnknown || failure.Retryable ||
		strings.Contains(err.Error(), "sensitive provider text") {
		t.Fatalf("5xx error = %v", err)
	}
}

func TestAssessmentImagesUseLowDetailAndKnownInvalidImageIsClassified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(string(body.Messages[1].Content), `"detail":"low"`) || !strings.Contains(string(body.Messages[1].Content), `image_url`) || !strings.Contains(string(body.Messages[1].Content), `observed-one`) {
			t.Errorf("missing correlated low image: %+v", body)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_image_url","message":"untrusted provider message"}}`))
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "test", server.Client()).Complete(context.Background(), runnerapp.ModelRequest{Model: testModel(), Images: []runnerapp.ModelImage{{ObservationID: "observed-one", URL: "https://example.com/image.jpg"}}})
	f, ok := fault.As(err)
	if !ok || f.Reason != "MANAGED_IMAGE_REJECTED" || f.Code == fault.ExternalEffectUnknown || strings.Contains(err.Error(), "untrusted provider message") {
		t.Fatalf("classification %v", err)
	}
}
