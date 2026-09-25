package openaiapi

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
)

// TestLiveManagedModelConformance is an opt-in, low-cost credential/model
// release gate. Full research orchestration remains covered by deterministic
// integration tests; this proves that the production adapter can obtain a
// schema-bound response and authoritative usage from the configured provider.
func TestLiveManagedModelConformance(t *testing.T) {
	if os.Getenv("MANAGED_OPENAI_LIVE") != "1" {
		t.Skip("set MANAGED_OPENAI_LIVE=1 to run the managed-provider conformance gate")
	}
	secret := strings.TrimSpace(os.Getenv("MANAGED_OPENAI_API_SECRET"))
	if secret == "" {
		t.Fatal("MANAGED_OPENAI_API_SECRET is required")
	}
	transport, err := sharedhttpclient.NewTransport(sharedhttpclient.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	httpClient, err := sharedhttpclient.NewClient(transport, 45*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient("", secret, httpClient)
	model := runnerdomain.Model{
		Key: "live-gpt-5-nano", ProviderModelID: "gpt-5-nano",
		InputMicrosPerMTok: 50_000, OutputMicrosPerMTok: 400_000,
		MaxOutputTokens: 1_024, ReasoningEffort: "low",
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	response, err := client.Complete(ctx, runnerapp.ModelRequest{
		Model: model, RequestKey: "managed-live-conformance",
		SystemPrompt: "Return only JSON that satisfies the supplied schema.",
		UserPrompt:   "Confirm adapter conformance with ok=true and a short summary.",
		SchemaName:   "managed_live_conformance",
		Schema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"ok":      map[string]any{"type": "boolean"},
				"summary": map[string]any{"type": "string"},
			},
			"required": []string{"ok", "summary"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		OK      bool   `json:"ok"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(response.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK || strings.TrimSpace(decoded.Summary) == "" ||
		response.Usage.InputTokens <= 0 || response.Usage.OutputTokens <= 0 {
		t.Fatalf("managed live response failed conformance: ok=%t summary=%t usage=%+v",
			decoded.OK, strings.TrimSpace(decoded.Summary) != "", response.Usage)
	}
}
