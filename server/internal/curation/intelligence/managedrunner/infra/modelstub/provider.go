// Package modelstub is a deterministic ModelPort for tests and local review.
//
// It exists so E2E can exercise the full managed path — claim, budget, steps,
// submission, auto-transition — without a network call or a bill. It proves the
// orchestration, NOT that a real model produces usable output; that requires
// a separate live-provider review.
//
// This provider must never be reachable in production. Enable() refuses to
// build one outside development and test.
package modelstub

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"

	intelligenceapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/app"
	runnerapp "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/app"
	runnerdomain "github.com/vitlane/vitlane/server/internal/curation/intelligence/managedrunner/domain"
)

type Provider struct{}

// New returns the stub only outside production. Returning an error rather than
// a disabled provider means a misconfigured production deploy fails at startup
// instead of silently serving fixture candidates to real users.
func New(appEnvironment string) (*Provider, error) {
	if appEnvironment == "production" {
		return nil, fmt.Errorf(
			"%w: the stub model provider is not allowed in production",
			runnerdomain.ErrDisabled,
		)
	}
	return &Provider{}, nil
}

// Complete answers from the request's schema name so each pipeline step gets a
// shape it can parse. Token usage is derived from the prompt length so budget
// arithmetic is still exercised end to end.
func (p *Provider) Complete(
	_ context.Context,
	request runnerapp.ModelRequest,
) (runnerapp.ModelResponse, error) {
	usage := runnerdomain.TokenUsage{
		InputTokens:  int64(len(request.UserPrompt) / 4),
		OutputTokens: 64,
	}
	var payload any
	switch request.SchemaName {
	case "vitlane_budget_estimates":
		var input struct {
			Currency string `json:"currency"`
			Targets  []struct {
				Quantity int `json:"quantity"`
			} `json:"targets"`
		}
		if err := json.Unmarshal([]byte(request.UserPrompt), &input); err != nil {
			return runnerapp.ModelResponse{}, err
		}
		estimates := []map[string]string{}
		for range input.Targets {
			amount := "50.00"
			if input.Currency == "KRW" {
				amount = "50000"
			}
			estimates = append(estimates, map[string]string{"amount": amount})
		}
		payload = map[string]any{"estimates": estimates}
	case "vitlane_planning_targets":
		payload = planningThreadTargets(request)
	case "vitlane_catalog_query":
		payload = catalogQuery(request.UserPrompt)
	case "vitlane_candidate_ranking":
		payload = candidateRanking(request.UserPrompt)
	case "vitlane_curation_thread_decision":
		payload = threadDecision(request.UserPrompt)
	case "vitlane_curation_combination_response":
		payload = combinationResponse(request.UserPrompt)
	case "vitlane_curation_thread_response":
		payload = threadResponse(request.UserPrompt)
	case "vitlane_curation_auto_resolution":
		payload = curationAutoResolution(request.UserPrompt)
	default:
		return runnerapp.ModelResponse{}, fmt.Errorf(
			"%w: unknown schema %q",
			runnerdomain.ErrModelResponse, request.SchemaName,
		)
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return runnerapp.ModelResponse{}, err
	}
	return runnerapp.ModelResponse{
		Content: string(content), Usage: usage,
	}, nil
}

type autoTarget struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	NormalizedIntent string `json:"normalizedIntent"`
	OrderIndex       int    `json:"orderIndex"`
	Researchable     bool   `json:"researchable"`
}

// curationAutoResolution is a narrow local-review fixture for the server-owned
// Auto contract. Its lexicon exercises Korean -> English normalization,
// deterministic target matching and exact action dispatch; it is not evidence
// that the production model understands arbitrary Korean.
func curationAutoResolution(prompt string) map[string]any {
	var input struct {
		OriginalRequest string       `json:"originalRequest"`
		Targets         []autoTarget `json:"targets"`
	}
	if err := json.Unmarshal([]byte(prompt), &input); err != nil {
		return autoSelection("UNKNOWN", "", nil, "STUB_PROMPT_INVALID")
	}
	request := strings.ToLower(strings.TrimSpace(input.OriginalRequest))
	operation := "UNKNOWN"
	switch {
	case containsAnyText(request, "추가", "새로운", "새 상품", "new target", "add target", "another target"):
		operation = "ADD"
	case containsAnyText(request, "재조사", "다시", "좀더", "좀 더", "더 조사", "research", "refine", "again", "cheaper", "more"):
		operation = "REFINE"
	}
	reference := stubEnglishProductReference(request)
	if operation == "ADD" {
		return autoDecision(operation, reference, nil, "ADD_TARGET", nil, "STUB_EXPLICIT_ADD")
	}
	var matched *autoTarget
	if reference != "" {
		for index := range input.Targets {
			target := &input.Targets[index]
			if target.Researchable && allWordsContained(reference, target.NormalizedIntent) {
				if matched != nil {
					return autoSelection(operation, reference, nil, "STUB_TARGET_AMBIGUOUS")
				}
				matched = target
			}
		}
	}
	if operation == "REFINE" && matched != nil {
		return autoDecision(operation, reference, nil, "RESEARCH_AGAIN", &matched.ID, "STUB_NORMALIZED_TARGET_MATCH")
	}
	if operation == "REFINE" && reference == "" {
		for index := range input.Targets {
			if !input.Targets[index].Researchable {
				continue
			}
			if matched != nil {
				return autoSelection(operation, reference, nil, "STUB_TARGET_REQUIRED")
			}
			matched = &input.Targets[index]
		}
		if matched != nil {
			return autoDecision(operation, reference, nil, "RESEARCH_AGAIN", &matched.ID, "STUB_ONLY_RESEARCHABLE_TARGET")
		}
	}
	if reference != "" && matched == nil {
		return autoDecision(operation, reference, nil, "ADD_TARGET", nil, "STUB_NEW_PRODUCT_REFERENCE")
	}
	return autoSelection(operation, reference, nil, "STUB_TARGET_REQUIRED")
}

func autoSelection(operation, reference string, ordinal *int, reason string) map[string]any {
	return autoDecision(operation, reference, ordinal, "NEEDS_SELECTION", nil, reason)
}

func autoDecision(
	operation, reference string,
	ordinal *int,
	decision string,
	targetID *string,
	reason string,
) map[string]any {
	return map[string]any{
		"operation": operation, "productReferenceEnglish": reference,
		"modifierTermsEnglish": []string{}, "referencedOrdinal": ordinal,
		"decision": decision, "targetId": targetID, "reasonCode": reason,
	}
}

func stubEnglishProductReference(request string) string {
	switch {
	case containsAnyText(request, "reading lamp", "독서등"):
		return "reading lamp"
	case containsAnyText(request, "어댑터", "어뎁터", "adapter", "adaptor"):
		return "travel adapter"
	case containsAnyText(request, "캐리어", "luggage", "suitcase"):
		return "travel luggage"
	case containsAnyText(request, "백팩", "backpack"):
		return "commuter backpack"
	case containsAnyText(request, "트레일 러닝화", "trail shoe"):
		return "trail running shoe"
	case containsAnyText(request, "랜턴", "lantern"):
		return "camping lantern"
	}
	return ""
}

func containsAnyText(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func allWordsContained(reference, candidate string) bool {
	candidate = strings.ToLower(candidate)
	for _, word := range strings.Fields(strings.ToLower(reference)) {
		if !strings.Contains(candidate, word) {
			return false
		}
	}
	return true
}

func planningTargets(prompt string) map[string]any {
	// The prompt carries the plan intent, so splitting it gives E2E a stable
	// way to assert that the user's words reached the model layer.
	titles := extractTitles(prompt)
	targets := make([]map[string]any, 0, len(titles))
	for _, title := range titles {
		targets = append(targets, map[string]any{
			"criteria": stubCriteria(title, englishRetailPhrase(title), prompt), "title": title,
			"category":    title,
			"searchQuery": englishRetailPhrase(title),
			"rationale":   title + " 조사 항목",
		})
	}
	return map[string]any{"targets": targets}
}

func catalogQuery(prompt string) map[string]any {
	titles := extractTitles(prompt)
	query := "product"
	if len(titles) > 0 {
		query = englishRetailPhrase(titles[0])
	}
	payload := map[string]any{
		"criteria": stubCriteria(query, query, prompt), "querySeeds": []string{query, "alternative " + query}, "query": query, "mustInclude": []string{}, "mustExclude": []string{},
	}
	if len(titles) > 0 {
		if vertical := productVertical(titles[0]); vertical != "" {
			payload["productVertical"] = vertical
		}
	}
	return payload
}

// productVertical answers the query step's optional field for the fixture
// lexicon only, so local review reaches the direct mall paths with the
// audited sample products: milk asks Kurly, pants ask Zigzag, a cable asks
// Lotte ON and Daiso Mall. Anything else stays general, exactly like a model
// that is unsure.
func productVertical(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "우유") || strings.Contains(value, "milk"):
		return "FOOD"
	case strings.Contains(value, "바지") || strings.Contains(value, "팬츠") || strings.Contains(value, "pants"):
		return "FASHION"
	case strings.Contains(value, "케이블") || strings.Contains(value, "cable"):
		return "LIVING"
	}
	return ""
}

// englishRetailPhrase is intentionally a small deterministic fixture lexicon.
// It lets local review exercise the real managed-provider and cost-ledger
// boundary without pretending that this stub is a general translator.
var retailWords = regexp.MustCompile(`[A-Za-z0-9]+(?:[-'][A-Za-z0-9]+)*`)

func englishRetailPhrase(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case strings.Contains(value, "라미") || strings.Contains(value, "만년필"):
		return "Lamy Safari fountain pen"
	case strings.Contains(value, "버즈") || strings.Contains(value, "이어폰"):
		return "Samsung Galaxy Buds3 Pro earbuds"
	case strings.Contains(value, "우유"):
		return "fresh milk 900ml"
	case strings.Contains(value, "바지") || strings.Contains(value, "팬츠"):
		return "velour banding pants"
	case strings.Contains(value, "케이블"):
		return "usb c to lightning cable"
	case strings.Contains(value, "캠핑") && strings.Contains(value, "의자"):
		return "camping chair"
	case strings.Contains(value, "랜턴"):
		return "rechargeable camping lantern"
	case strings.Contains(value, "코펠"):
		return "camping cookware set"
	case strings.Contains(value, "캠핑"):
		return "camping gear"
	}
	if isASCIIEnglish(value) {
		return strings.Join(retailWords.FindAllString(strings.ToLower(value), -1), " ")
	}
	return "alternative retail product"
}

func isASCIIEnglish(value string) bool {
	hasLetter := false
	for _, current := range value {
		if current > 127 {
			return false
		}
		if current >= 'A' && current <= 'Z' || current >= 'a' && current <= 'z' {
			hasLetter = true
		}
	}
	return hasLetter
}

// candidateRanking echoes back every observation id the prompt offered, in
// order. The pipeline discards ids it did not offer, so a stub that invented
// one would fail the same check a real model would.
func candidateRanking(prompt string) map[string]any {
	ranked := make([]map[string]any, 0)
	for line := range strings.SplitSeq(prompt, "\n") {
		id, ok := strings.CutPrefix(strings.TrimSpace(line), "observationId=")
		if !ok {
			continue
		}
		id, rest, _ := strings.Cut(id, " ")
		if id == "" {
			continue
		}
		// The product name is the same on every run; the observation id is not.
		_, name, _ := strings.Cut(rest, " name=")
		name, _, _ = strings.Cut(name, " description=")
		ranked = append(ranked, map[string]any{
			"axisScores": stubAxisScores(prompt, name), "observationId": id,
			"intentPoint":    "This observed product matches the requested use case.",
			"features":       []string{"Matches server-verified catalog requirements."},
			"specifications": []string{"Uses an observed catalog product identity."},
		})
	}
	return map[string]any{"ranked": ranked}
}

// extractTitles reads the marker lines the pipeline writes into the prompt.
// Falling back to a single generic target keeps the stub usable for intents
// that carry no explicit item list.
func extractTitles(prompt string) []string {
	var titles []string
	for line := range strings.SplitSeq(prompt, "\n") {
		if value, ok := strings.CutPrefix(
			strings.TrimSpace(line), "intentItem=",
		); ok && value != "" {
			titles = append(titles, value)
		}
	}
	if len(titles) == 0 {
		titles = []string{"추천 상품"}
	}
	return titles
}

// The stub uses a deliberately generic criterion, never a pretend production
// interpreter. Existing criteria are echoed so integration exercises snapshots.
func stubCriteria(label, product, prompt string) map[string]any {
	for line := range strings.SplitSeq(prompt, "\n") {
		if raw, ok := strings.CutPrefix(line, "currentCriteria="); ok && raw != "null" {
			var v map[string]any
			if json.Unmarshal([]byte(raw), &v) == nil && v != nil {
				return v
			}
		}
	}
	name := "General suitability"
	if strings.Contains(prompt, "contentLocale=ko-KR") {
		name = "기본 용도 적합성"
	}
	return map[string]any{"subject": map[string]any{"label": label, "productType": product}, "axes": []map[string]any{{"axisId": "stub-general", "label": name, "definition": name, "importance": 3, "usesPrice": false, "usesVisualEvidence": false, "origin": "REQUEST"}}, "exclusions": []string{}}
}

// stubScore spreads candidates over 40–89 by product name, so local review can tell a Pick from the
// rest: sorting, the budget-aware Pick and the product a response shows all need candidates that
// differ. It is a fixed function of the name and the axis, and says nothing about the product.
func stubScore(name, axisID string) int {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name + "|" + axisID))
	return 40 + int(hash.Sum32()%50)
}

func stubAxisScores(prompt, name string) []map[string]any {
	c := stubCriteria("product", "retail product", prompt)
	raw, _ := json.Marshal(c["axes"])
	var axes []struct {
		AxisID string `json:"axisId"`
	}
	_ = json.Unmarshal(raw, &axes)
	scores := []map[string]any{}
	for _, a := range axes {
		explanation := "Stub estimate; no additional evidence was observed."
		if strings.Contains(prompt, "contentLocale=ko-KR") {
			explanation = "테스트용 추정 점수입니다. 추가 근거는 확인하지 않았습니다."
		}
		scores = append(scores, map[string]any{"axisId": a.AxisID, "scorePercent": stubScore(name, a.AxisID), "basis": "UNKNOWN", "explanation": explanation, "factIds": []string{}})
	}
	return scores
}

// These fixtures exercise the contract only; production still refuses this provider.
func planningThreadTargets(request runnerapp.ModelRequest) map[string]any {
	out := planningTargets(request.UserPrompt)
	props, _ := request.Schema["properties"].(map[string]any)
	fields := map[string]string{}
	for _, line := range strings.Split(request.UserPrompt, "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = value
		}
	}
	currency := fields["budgetCurrency"]
	if currency == "" {
		currency = "USD"
	}
	var budget any
	kind := "KEEP"
	quote := ""
	if fields["budgetEnabled"] == "true" {
		budget = map[string]string{"amount": fields["budgetTotal"], "currency": currency}
	}
	if cap, ok := intelligenceapp.ExplicitSpendingCap(fields["requestText"]); ok {
		budget = map[string]string{"amount": cap.Amount, "currency": cap.Currency}
		currency = cap.Currency
		kind = "ENABLE"
		if fields["budgetEnabled"] == "true" {
			kind = "SET_TOTAL"
		}
		quote = fields["requestText"]
	}
	if intelligenceapp.UnlimitedBudget.MatchString(fields["requestText"]) {
		budget = nil
		kind = "DISABLE"
		quote = fields["requestText"]
	}
	if _, ok := props["budget"]; ok {
		out["budget"] = budget
	}
	if _, ok := props["budgetDecision"]; ok {
		out["budgetDecision"] = map[string]string{"kind": kind, "evidence": quote}
	}
	targets := out["targets"].([]map[string]any)
	var itemProps map[string]any
	if array, ok := props["targets"].(map[string]any); ok {
		if item, ok := array["items"].(map[string]any); ok {
			itemProps, _ = item["properties"].(map[string]any)
		}
	}
	estimates := []map[string]string{}
	for _, target := range targets {
		if _, ok := itemProps["quantity"]; ok {
			target["quantity"] = 1
		}
		amount := "50.00"
		if currency == "KRW" {
			amount = "50000"
		}
		estimates = append(estimates, map[string]string{"amount": amount})
	}
	if _, ok := props["estimates"]; ok {
		out["estimates"] = estimates
	}
	return out
}
func threadDecision(prompt string) map[string]any {
	var in struct {
		ManualAction *struct {
			TargetID string `json:"targetId"`
		} `json:"manualAction"`
		Thread struct {
			Request string `json:"request"`
		} `json:"thread"`
		Targets []struct {
			ID, Title, Intent string
			Researchable      bool
		} `json:"targets"`
	}
	_ = json.Unmarshal([]byte(prompt), &in)
	empty := func() map[string]any { return map[string]any{"actions": []any{}, "conditions": []any{}, "budget": nil} }
	// A manual criteria fixture is a no-change interpretation; live-model tests
	// independently exercise actual axis changes.
	if in.ManualAction != nil {
		return map[string]any{"plan": empty(), "question": nil}
	}
	plan := func(kind, id string) map[string]any {
		p := empty()
		p["actions"] = []any{map[string]any{"kind": kind, "targetId": id, "instruction": in.Thread.Request}}
		return p
	}
	request := strings.ToLower(in.Thread.Request)
	// A narrow question lexicon, only so the reply path can be exercised
	// locally. It is not evidence of how a live model separates a question
	// from a request.
	if containsAnyText(request, "?", "？") && containsAnyText(request, "뭐가", "어떤", "왜", "차이", "which", "why", "what", "difference", "how ") &&
		!containsAnyText(request, "찾아", "추가", "다시", "조사", "find", "add ", "again", "research") {
		return map[string]any{"answer": true, "plan": empty(), "question": nil}
	}
	if containsAnyText(request, "추가", "add ", "new product") {
		return map[string]any{"plan": plan("ADD_TARGET", ""), "question": nil}
	}
	ref := stubEnglishProductReference(request)
	options := []any{}
	for _, target := range in.Targets {
		if !target.Researchable {
			continue
		}
		if ref != "" && allWordsContained(ref, target.Intent) {
			return map[string]any{"plan": plan("RESEARCH_AGAIN", target.ID), "question": nil}
		}
		options = append(options, map[string]any{"label": target.Title, "plan": plan("RESEARCH_AGAIN", target.ID)})
	}
	if len(options) == 1 {
		return map[string]any{"plan": options[0].(map[string]any)["plan"], "question": nil}
	}
	if len(options) == 0 {
		return map[string]any{"plan": plan("ADD_TARGET", ""), "question": nil}
	}
	if len(options) > 3 {
		options = options[:3]
	}
	return map[string]any{"plan": empty(), "question": map[string]any{"prompt": "상품을 선택하세요 / Choose a product", "options": options}}
}

// threadResponse writes a deterministic reply from the refs the Server offered.
// It proves the RESPONSE Action's orchestration and validation, never the
// quality of a live model's wording.
func threadResponse(prompt string) map[string]any {
	var in struct {
		Kind     string `json:"kind"`
		Locale   string `json:"locale"`
		Products []struct {
			Researched bool `json:"researchedInThisRequest"`
			Candidates []struct {
				Ref string `json:"ref"`
			} `json:"candidates"`
		} `json:"products"`
	}
	_ = json.Unmarshal([]byte(prompt), &in)
	korean := in.Locale != "en-US"
	lines := []string{}
	for _, product := range in.Products {
		if len(product.Candidates) == 0 || (in.Kind != "ANSWER" && !product.Researched) {
			continue
		}
		// A reply names a candidate by its reference only. The stub planner keeps the
		// whole request as the product title, budget figure included, so echoing the
		// title would break the same price rule a live model must follow.
		switch {
		case in.Kind == "ANSWER" && korean:
			lines = append(lines, fmt.Sprintf("저장된 평가로 보면 [[%s]] 후보가 가장 앞서 있어요.", product.Candidates[0].Ref))
		case in.Kind == "ANSWER":
			lines = append(lines, fmt.Sprintf("On the saved assessments, [[%s]] is ahead.", product.Candidates[0].Ref))
		case korean:
			lines = append(lines, fmt.Sprintf("[[%s]] 후보가 저장된 기준 점수가 가장 높아 1순위로 뒀어요.", product.Candidates[0].Ref))
		default:
			lines = append(lines, fmt.Sprintf("[[%s]] ranks first on the saved criteria scores.", product.Candidates[0].Ref))
		}
	}
	switch {
	case len(lines) == 0 && korean:
		lines = append(lines, "아직 저장된 후보가 없어서 비교해 드릴 정보가 없어요. 찾고 싶은 상품을 알려 주시면 조사해 볼게요.")
	case len(lines) == 0:
		lines = append(lines, "There are no saved candidates to compare yet. Tell me what to look for and I will research it.")
	case in.Kind == "ANSWER" && korean:
		lines = append(lines, "테스트용 답변이라 질문의 세부 내용까지는 따지지 못했어요. 더 비교하려면 재조사나 기준 변경을 요청해 주세요.")
	case in.Kind == "ANSWER":
		lines = append(lines, "This is a test answer and does not weigh the details of the question. Ask for another research or a criteria change to compare further.")
	case korean:
		lines = append(lines, "테스트용 응답이며 실제 사용감은 상품 정보만으로 확인하지 못했어요.")
	default:
		lines = append(lines, "This is a test reply; real-world feel could not be verified from the listings.")
	}
	return map[string]any{"body": strings.Join(lines, " ")}
}
