package app

import (
	"encoding/json"
	"testing"
)

func TestInitialAutoBudgetDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, request, baseline, payload string
		valid                            bool
	}{
		{"missed cap", "20만원 언더 만년필", "", `{"budget":null,"budgetDecision":{"kind":"DISABLE","evidence":""}}`, false},
		{"exact Korean cap", "20만원 언더 만년필", "", `{"budget":{"amount":"200000","currency":"KRW"},"budgetDecision":{"kind":"ENABLE","evidence":"20만원 언더"}}`, true},
		{"override baseline", "20만원 언더 만년필", "50000", `{"budget":{"amount":"200000","currency":"KRW"},"budgetDecision":{"kind":"SET_TOTAL","evidence":"20만원 언더"}}`, true},
		{"unrequested increase", "만년필 다시", "50000", `{"budget":{"amount":"200000","currency":"KRW"},"budgetDecision":{"kind":"SET_TOTAL","evidence":""}}`, false},
		{"keep baseline", "만년필", "50000", `{"budget":{"amount":"50000","currency":"KRW"},"budgetDecision":{"kind":"KEEP","evidence":""}}`, true},
		{"explicit unlimited", "예산은 제한 없음", "50000", `{"budget":null,"budgetDecision":{"kind":"DISABLE","evidence":"예산은 제한 없음"}}`, true},
		{"automatic initial", "만년필", "", `{"budget":{"amount":"50000","currency":"KRW"},"budgetDecision":{"kind":"ENABLE","evidence":""}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := PlanningContext{OriginalIntent: tc.request, BudgetCurrency: "KRW", BudgetEnabled: tc.baseline != "", TotalBudget: Money{tc.baseline, "KRW"}}
			var p PlanningTargetsPayload
			if err := json.Unmarshal([]byte(tc.payload), &p); err != nil {
				t.Fatal(err)
			}
			if err := applyInitialAutoBudget(&c, p); (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
func TestCapOmissionGuardDoesNotTreatQuantitiesOrModelNumbersAsMoney(t *testing.T) {
	for _, s := range []string{"Find 2 cheaper keyboard options", "MX 200 keyboard", "20원 동전 모양 펜", "아이폰 20", "개당 5만원 펜 2개"} {
		if _, ok := ExplicitSpendingCap(s); ok {
			t.Fatalf("false cap: %s", s)
		}
	}
	for _, s := range []string{"20만원 언더", "200,000원 이하", "under $200", "$200 or less"} {
		if _, ok := ExplicitSpendingCap(s); !ok {
			t.Fatalf("missed cap: %s", s)
		}
	}
}
