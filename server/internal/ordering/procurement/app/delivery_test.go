package app

import "testing"

// 선택 배송 파생(5차 C2) — 해석 실패는 빈 값이 아니라 Derived=false로 명시된다.
func TestDeriveDeliverySelection(t *testing.T) {
	resolved := deriveDeliverySelection([]byte(`{
		"deliveryGroups": [
			{"selectedOptionRef": "std", "options": [
				{"id": "std", "title": "Standard", "amountMinor": 500},
				{"id": "exp", "title": "Express", "amountMinor": 1500}
			]},
			{"selectedOptionRef": "pkg", "options": [
				{"id": "pkg", "title": "Package", "amountMinor": 200}
			]}
		]
	}`))
	if !resolved.Derived || resolved.Title != "Standard · Package" || resolved.AmountMinor != 700 {
		t.Fatalf("resolved=%+v", resolved)
	}

	// ref가 옵션 목록에 없으면(스냅샷 형태 차이) 전체를 파생 실패로 명시한다.
	unresolved := deriveDeliverySelection([]byte(`{
		"deliveryGroups": [
			{"selectedOptionRef": "gone", "options": [{"id": "std", "title": "Standard", "amountMinor": 500}]}
		]
	}`))
	if unresolved.Derived || unresolved.Title != "" || unresolved.AmountMinor != 0 {
		t.Fatalf("unresolved=%+v", unresolved)
	}
	if empty := deriveDeliverySelection([]byte(`{}`)); empty.Derived {
		t.Fatalf("empty snapshot must not derive: %+v", empty)
	}
	if bad := deriveDeliverySelection([]byte(`not-json`)); bad.Derived {
		t.Fatalf("invalid snapshot must not derive: %+v", bad)
	}
}
