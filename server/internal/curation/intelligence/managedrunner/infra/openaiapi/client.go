// Package openaiapi adapts the OpenAI Chat Completions HTTP API to the
// managedrunner ModelPort. It is the only package in the product that knows a
// provider exists; swapping providers means adding a sibling package, not
// touching domain, app, or Web.
package openaiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

const defaultBaseURL = "https://api.openai.com/v1"

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewClient(baseURL string, apiKey string, httpClient *http.Client) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: httpClient,
	}
}

type chatMessage struct {
	Role    string                 `json:"role"`
	Content string                 `json:"content"`
	Images  []runnerapp.ModelImage `json:"-"`
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	MaxCompletionTokens int64           `json:"max_completion_tokens"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ResponseFormat      *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *jsonSchema `json:"json_schema,omitempty"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Complete sends one prompt and returns the raw JSON content plus token usage.
// Errors deliberately carry no provider text: the message could echo the user's
// intent back into logs, and the caller only needs a reason code.
func (c *Client) Complete(
	ctx context.Context,
	request runnerapp.ModelRequest,
) (runnerapp.ModelResponse, error) {
	if c.apiKey == "" {
		return runnerapp.ModelResponse{}, runnerdomain.ErrDisabled
	}
	if !request.Model.Valid() {
		return runnerapp.ModelResponse{}, runnerdomain.ErrModelUnknown
	}
	payload := chatRequest{
		Model: request.Model.ProviderModelID,
		Messages: []chatMessage{
			{Role: "system", Content: request.SystemPrompt},
			{Role: "user", Content: request.UserPrompt, Images: request.Images},
		},
		MaxCompletionTokens: request.Model.MaxOutputTokens,
		ReasoningEffort:     request.Model.ReasoningEffort,
	}
	if len(request.Schema) > 0 {
		payload.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchema{
				Name: request.SchemaName, Strict: true, Schema: request.Schema,
			},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return runnerapp.ModelResponse{}, err
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return runnerapp.ModelResponse{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	if request.RequestKey != "" {
		// Correlation only. The adapter does not assume that the provider
		// deduplicates this header; ambiguous outcomes remain UNKNOWN.
		httpRequest.Header.Set("X-Client-Request-Id", request.RequestKey)
	}

	response, err := sharedhttpclient.Do(
		ctx, c.httpClient, httpRequest, sharedhttpclient.ExternalEffect,
	)
	if err != nil {
		return runnerapp.ModelResponse{}, err
	}
	defer response.Body.Close()
	raw, err := sharedhttpclient.ReadBody(response.Body, 4<<20)
	if err != nil {
		return runnerapp.ModelResponse{}, fault.Wrap(
			fmt.Errorf("%w: body", runnerdomain.ErrModelResponse),
			fault.ExternalEffectUnknown, "MANAGED_MODEL_USAGE_UNKNOWN", false,
		)
	}
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusBadRequest && len(request.Images) > 0 {
			var problem struct {
				Error struct {
					Code string `json:"code"`
				}
			}
			if json.Unmarshal(raw, &problem) == nil {
				switch problem.Error.Code {
				case "invalid_image", "invalid_image_url", "image_too_large", "image_parse_error":
					return runnerapp.ModelResponse{}, fault.New(fault.ProviderRejected, "MANAGED_IMAGE_REJECTED", false)
				}
			}
		}
		if response.StatusCode >= http.StatusInternalServerError {
			// A 5xx proves that the provider answered, but not that a paid model
			// invocation was never started. Without authoritative usage data the
			// reservation must remain conservative.
			return runnerapp.ModelResponse{}, fault.New(
				fault.ExternalEffectUnknown,
				"MANAGED_MODEL_HTTP_5XX_UNKNOWN", false,
			)
		}
		return runnerapp.ModelResponse{}, sharedhttpclient.StatusFault(
			response.StatusCode, response.Header.Get("Retry-After"), false,
		)
	}
	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return runnerapp.ModelResponse{}, fault.Wrap(
			fmt.Errorf("%w: decode", runnerdomain.ErrModelResponse),
			fault.ExternalEffectUnknown, "MANAGED_MODEL_USAGE_UNKNOWN", false,
		)
	}
	if decoded.Error != nil || len(decoded.Choices) == 0 {
		return runnerapp.ModelResponse{}, fault.Wrap(
			fmt.Errorf("%w: no choice", runnerdomain.ErrModelResponse),
			fault.ExternalEffectUnknown, "MANAGED_MODEL_USAGE_UNKNOWN", false,
		)
	}
	usage := runnerdomain.TokenUsage{
		InputTokens:  decoded.Usage.PromptTokens,
		OutputTokens: decoded.Usage.CompletionTokens,
	}
	// A truncated response is reported with its usage so the caller can still
	// settle the budget for tokens the provider already billed.
	if decoded.Choices[0].FinishReason == "length" {
		return runnerapp.ModelResponse{Usage: usage}, fmt.Errorf(
			"%w: truncated", runnerdomain.ErrModelResponse,
		)
	}
	return runnerapp.ModelResponse{
		Content: decoded.Choices[0].Message.Content, Usage: usage,
	}, nil
}

func (m chatMessage) MarshalJSON() ([]byte, error) {
	var content any = m.Content
	if len(m.Images) > 0 {
		parts := []map[string]any{{"type": "text", "text": m.Content}}
		for _, i := range m.Images {
			parts = append(parts, map[string]any{"type": "text", "text": "Representative image for observationId=" + i.ObservationID}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": i.URL, "detail": "low"}})
		}
		content = parts
	}
	return json.Marshal(struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}{m.Role, content})
}
