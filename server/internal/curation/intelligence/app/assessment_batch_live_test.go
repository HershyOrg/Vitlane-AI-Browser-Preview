package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

// TestLiveAssessmentBatchCost measures, against the actual model, how splitting
// one Round's evaluation into batches changes token cost, budget reservation
// peaks and wall time. It is paid and opt-in, uses the production ranking
// prompt and schema on the same synthetic fixture as the 2026-09-17 50-item
// measurement, and writes no product state.
func TestLiveAssessmentBatchCost(t *testing.T) {
	if os.Getenv("VITLANE_ASSESSMENT_BATCH_LIVE") != "1" {
		t.Skip("explicit paid model opt-in required")
	}
	secret := os.Getenv("MANAGED_OPENAI_API_SECRET")
	if secret == "" {
		t.Fatal("model credential required")
	}
	sizes := envInts("VITLANE_ASSESSMENT_BATCH_SIZES", []int{12, 20, 30, 50})
	parallels := envInts("VITLANE_ASSESSMENT_BATCH_PARALLEL", []int{1, 2})
	total := 50
	if v, err := strconv.Atoi(os.Getenv("VITLANE_ASSESSMENT_BATCH_CANDIDATES")); err == nil && v > 0 {
		total = v
	}
	const axes = 8
	base := runnerdomain.DefaultModels()[1]
	criteria := &ResearchCriteria{Subject: ResearchSubject{Label: "검증용 펜", ProductType: "pen"}, Exclusions: []string{}}
	for i, label := range []string{"휴대성", "그립", "내구성", "필기감", "디자인", "관리 편의", "다용도", "선물 적합성"} {
		criteria.Axes = append(criteria.Axes, ResearchAxis{AxisID: fmt.Sprint("axis-", i), Label: label, Definition: label, Importance: 3, Origin: "REQUEST"})
	}
	c := ResearchContext{Criteria: criteria, ContentLocale: "ko-KR", Country: "KR"}
	observations := make([]ResearchCandidateObservation, total)
	for i := range observations {
		observations[i] = ResearchCandidateObservation{ObservationID: fmt.Sprint("synthetic-", i), Name: fmt.Sprintf("Synthetic blue pen %d", i), Description: "Synthetic fixture. Blue plastic pen, 14 cm. No other verified product information.", FactIDs: []string{}}
	}
	client := &http.Client{Timeout: 180 * time.Second}
	system := researchSystemPrompt(rankingSystemPrompt, c)
	runs := []map[string]any{}
	for _, parallel := range parallels {
		for _, size := range sizes {
			batches := liveBalancedBatches(total, size)
			t.Logf("config parallel=%d batchLimit=%d batches=%v", parallel, size, batches)
			run := runAssessmentBatchConfig(t, client, secret, base, system, c, observations, batches, parallel, axes)
			run["batchLimit"] = size
			run["parallel"] = parallel
			runs = append(runs, run)
		}
	}
	evidence := map[string]any{
		"scope":      "synthetic 50 observations × 8 axes, actual model, production ranking prompt/schema, batch-split cost measurement, no recommendation quality claim",
		"model":      base.ProviderModelID,
		"pricing":    map[string]any{"inputMicrosPerMTok": base.InputMicrosPerMTok, "outputMicrosPerMTok": base.OutputMicrosPerMTok},
		"candidates": total, "axes": axes, "observedAt": time.Now().UTC(), "runs": runs,
	}
	if path := os.Getenv("VITLANE_ASSESSMENT_BATCH_EVIDENCE"); path != "" {
		raw, _ := json.MarshalIndent(evidence, "", "  ")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func envInts(key string, fallback []int) []int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	out := []int{}
	for _, part := range strings.Split(raw, ",") {
		if v, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && v > 0 {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

// liveBalancedBatches splits total into the fewest batches of at most limit items,
// with sizes differing by at most one.
func liveBalancedBatches(total, limit int) []int {
	count := (total + limit - 1) / limit
	base, rem := total/count, total%count
	sizes := make([]int, count)
	for i := range sizes {
		sizes[i] = base
		if i < rem {
			sizes[i]++
		}
	}
	return sizes
}

type assessmentBatchCall struct {
	Index               int    `json:"index"`
	Wave                int    `json:"wave"`
	Size                int    `json:"size"`
	StartedOffsetMs     int64  `json:"startedOffsetMs"`
	DurationMs          int64  `json:"durationMs"`
	PromptTokens        int64  `json:"promptTokens"`
	CachedTokens        int64  `json:"cachedTokens"`
	CompletionTokens    int64  `json:"completionTokens"`
	ReasoningTokens     int64  `json:"reasoningTokens"`
	Returned            int    `json:"returned"`
	Missing             int    `json:"missing"`
	Invalid             int    `json:"invalid"`
	FinishReason        string `json:"finishReason"`
	MaxOutputTokens     int64  `json:"maxOutputTokens"`
	EstimatedInputBytes int64  `json:"estimatedInputBytes"`
	ActualCostMicros    int64  `json:"actualCostMicros"`
	ReserveMicros       int64  `json:"reserveMicrosCurrentFormula"`
	Error               string `json:"error,omitempty"`
}

func runAssessmentBatchConfig(t *testing.T, client *http.Client, secret string, base runnerdomain.Model, system string, c ResearchContext, observations []ResearchCandidateObservation, batches []int, parallel int, axes int) map[string]any {
	calls := make([]assessmentBatchCall, len(batches))
	offset := 0
	type job struct {
		index, wave int
		items       []ResearchCandidateObservation
	}
	jobs := make([]job, 0, len(batches))
	for i, size := range batches {
		jobs = append(jobs, job{index: i, wave: i / parallel, items: observations[offset : offset+size]})
		offset += size
	}
	started := time.Now()
	for wave := 0; wave*parallel < len(jobs); wave++ {
		var wg sync.WaitGroup
		for _, j := range jobs[wave*parallel : min(len(jobs), (wave+1)*parallel)] {
			wg.Add(1)
			go func(j job) {
				defer wg.Done()
				calls[j.index] = liveAssessmentCall(client, secret, base, system, c, j.items, axes, started, j.index, j.wave)
			}(j)
		}
		wg.Wait()
	}
	wallMs := time.Since(started).Milliseconds()
	summary := map[string]any{"batches": batches, "wallMs": wallMs, "calls": calls}
	var slotMs, prompt, cached, completion, reasoning, cost, reserveSum, maxCallMs int64
	var missing, invalid int
	peakByWave := map[int]int64{}
	for _, call := range calls {
		slotMs += call.DurationMs
		prompt += call.PromptTokens
		cached += call.CachedTokens
		completion += call.CompletionTokens
		reasoning += call.ReasoningTokens
		cost += call.ActualCostMicros
		reserveSum += call.ReserveMicros
		peakByWave[call.Wave] += call.ReserveMicros
		missing += call.Missing
		invalid += call.Invalid
		maxCallMs = max(maxCallMs, call.DurationMs)
		if call.Error != "" {
			t.Errorf("batch %d failed: %s", call.Index, call.Error)
		}
	}
	var peak int64
	for _, v := range peakByWave {
		peak = max(peak, v)
	}
	summary["slotMs"] = slotMs
	summary["maxCallMs"] = maxCallMs
	summary["promptTokens"] = prompt
	summary["cachedTokens"] = cached
	summary["completionTokens"] = completion
	summary["reasoningTokens"] = reasoning
	summary["actualCostMicros"] = cost
	summary["reserveSumMicrosCurrentFormula"] = reserveSum
	summary["reservePeakMicrosCurrentFormula"] = peak
	summary["missing"] = missing
	summary["invalid"] = invalid
	t.Logf("result parallel=%d batches=%v wallMs=%d slotMs=%d prompt=%d cached=%d completion=%d reasoning=%d costMicros=%d reservePeak=%d missing=%d", parallel, batches, wallMs, slotMs, prompt, cached, completion, reasoning, cost, peak, missing)
	return summary
}

// liveAssessmentCall mirrors the openaiapi client payload but keeps the usage
// detail fields the product client discards (cached and reasoning tokens).
func liveAssessmentCall(client *http.Client, secret string, base runnerdomain.Model, system string, c ResearchContext, items []ResearchCandidateObservation, axes int, started time.Time, index, wave int) assessmentBatchCall {
	model := base.AssessmentModel(len(items), axes)
	schema := CandidateRankingSchema(len(items), axes)
	user := rankingPrompt(c, items, len(items))
	schemaJSON, _ := json.Marshal(schema)
	call := assessmentBatchCall{Index: index, Wave: wave, Size: len(items), MaxOutputTokens: model.MaxOutputTokens,
		EstimatedInputBytes: int64(len(system) + len(user) + len(schemaJSON) + 128)}
	call.ReserveMicros = model.WorstCost(call.EstimatedInputBytes)
	payload := map[string]any{
		"model":                 model.ProviderModelID,
		"messages":              []map[string]any{{"role": "system", "content": system}, {"role": "user", "content": user}},
		"max_completion_tokens": model.MaxOutputTokens,
		"reasoning_effort":      model.ReasoningEffort,
		"response_format":       map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": SchemaCandidateRanking, "strict": true, "schema": schema}},
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		call.Error = err.Error()
		return call
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-Client-Request-Id", fmt.Sprintf("assessment-batch-live-%d-%d-%d", time.Now().Unix(), index, len(items)))
	call.StartedOffsetMs = time.Since(started).Milliseconds()
	begin := time.Now()
	response, err := client.Do(request)
	if err != nil {
		call.DurationMs = time.Since(begin).Milliseconds()
		call.Error = "transport: " + err.Error()
		return call
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	call.DurationMs = time.Since(begin).Milliseconds()
	if err != nil {
		call.Error = "body: " + err.Error()
		return call
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			CompletionTokens    int64 `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if response.StatusCode != http.StatusOK {
		call.Error = fmt.Sprintf("http %d", response.StatusCode)
		return call
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Error != nil || len(decoded.Choices) == 0 {
		call.Error = "response shape invalid"
		return call
	}
	call.PromptTokens = decoded.Usage.PromptTokens
	call.CachedTokens = decoded.Usage.PromptTokensDetails.CachedTokens
	call.CompletionTokens = decoded.Usage.CompletionTokens
	call.ReasoningTokens = decoded.Usage.CompletionTokensDetails.ReasoningTokens
	call.FinishReason = decoded.Choices[0].FinishReason
	call.ActualCostMicros = base.Cost(runnerdomain.TokenUsage{InputTokens: call.PromptTokens, OutputTokens: call.CompletionTokens})
	var payloadOut CandidateRankingPayload
	if err := json.Unmarshal([]byte(decoded.Choices[0].Message.Content), &payloadOut); err != nil {
		call.Error = "content decode: " + err.Error()
		return call
	}
	offered := map[string]bool{}
	for _, item := range items {
		offered[item.ObservationID] = true
	}
	seen := map[string]bool{}
	for _, row := range payloadOut.Ranked {
		if !offered[row.ObservationID] || seen[row.ObservationID] || len(row.AxisScores) != axes {
			call.Invalid++
			continue
		}
		seen[row.ObservationID] = true
	}
	call.Returned = len(seen)
	call.Missing = len(items) - len(seen)
	return call
}
