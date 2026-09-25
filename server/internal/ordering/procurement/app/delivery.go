package app

import (
	"encoding/json"
	"strings"
)

// 조달 지시의 선택 배송 파생(운영정합 5차 C2). checkout 스냅샷의
// deliveryGroups에서 selectedOptionRef가 가리키는 옵션을 찾는다 — 그룹이
// 하나라도 해석되지 않으면 Derived=false로 명시한다(무언 강등 금지: 화면은
// "정보 없음"이 아니라 "파생 실패"를 말하고 스냅샷 원문 확인을 안내한다).
type deliverySnapshot struct {
	DeliveryGroups []struct {
		SelectedOptionRef string `json:"selectedOptionRef"`
		Options           []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			AmountMinor int64  `json:"amountMinor"`
		} `json:"options"`
	} `json:"deliveryGroups"`
}

func deriveDeliverySelection(raw []byte) DeliverySelection {
	var snapshot deliverySnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || len(snapshot.DeliveryGroups) == 0 {
		return DeliverySelection{}
	}
	titles := make([]string, 0, len(snapshot.DeliveryGroups))
	var amount int64
	for _, group := range snapshot.DeliveryGroups {
		resolved := false
		for _, option := range group.Options {
			if option.ID != "" && option.ID == group.SelectedOptionRef {
				titles = append(titles, option.Title)
				amount += option.AmountMinor
				resolved = true
				break
			}
		}
		if !resolved {
			return DeliverySelection{}
		}
	}
	return DeliverySelection{
		Derived: true, Title: strings.Join(titles, " · "), AmountMinor: amount,
	}
}

func withDeliverySelection(item QueueItem) QueueItem {
	item.Delivery = deriveDeliverySelection(item.MerchantOrder.CheckoutSnapshot)
	return item
}
