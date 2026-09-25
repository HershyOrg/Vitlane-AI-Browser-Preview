package modelstub

import (
	"encoding/json"
	"strings"
)

func combinationResponse(raw string) any {
	var input struct {
		Locale       string `json:"locale"`
		Combinations []struct {
			ID    string `json:"id"`
			Items []struct {
				Ref string `json:"ref"`
			} `json:"items"`
		} `json:"combinations"`
	}
	_ = json.Unmarshal([]byte(raw), &input)
	id := ""
	refs := []string{}
	if len(input.Combinations) > 0 {
		id = input.Combinations[0].ID
		for _, item := range input.Combinations[0].Items {
			refs = append(refs, "[["+item.Ref+"]]")
		}
	}
	body := "Consider " + strings.Join(refs, ", ") + " together. Check the options before adding your selections."
	reason := "These candidates preserve your current choices and use the saved evaluations."
	caution := "Compatibility has not been independently verified."
	advice := "There is no need to spend the entire merchandise budget."
	label, tip := "Before choosing", "Compare the required specifications and the selected options."
	if strings.HasPrefix(input.Locale, "ko") {
		body = strings.Join(refs, ", ") + " 조합을 검토해 보세요. 담기 전에 필요한 옵션을 확인해 주세요."
		reason = "기존 선택을 유지하고 저장된 평가를 참고한 구성이에요."
		caution = "함께 쓰는 조건은 별도 확인이 필요해요."
		advice = "상품 예산을 모두 채워서 쓸 필요는 없어요."
		label = "선택 전에"
		tip = "필요한 사양과 선택한 옵션을 함께 확인해 보세요."
	}
	return map[string]any{"body": body, "combinationId": id, "compatibility": "UNVERIFIED", "reasons": []string{reason}, "tips": []map[string]string{{"label": label, "body": tip}}, "cautions": []string{caution}, "budgetAdvice": advice}
}
