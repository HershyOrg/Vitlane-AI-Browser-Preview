package domain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 이 테스트는 unit stage 어휘 계약의 골든 fixture를 소유한다(ADR-0057).
// fixture는 shared/openapi/fixtures에 있고 Web 테스트가 같은 파일을 파싱해
// FE 라벨 사전과 BE 파생이 어긋나면 CI가 잡는다. 갱신은
// UPDATE_CONTRACT_FIXTURES=1 go test ./internal/ordering/agencyorder/domain/ 로 한다.

const unitsFixtureName = "agency-order-units.v2.json"

type unitsFixtureCase struct {
	Name         string    `json:"name"`
	RefundStatus string    `json:"refundStatus"`
	Facts        UnitFacts `json:"facts"`
	Stage        UnitStage `json:"stage"`
}

type unitsFixture struct {
	SchemaVersion string             `json:"schemaVersion"`
	Stages        []UnitStage        `json:"stages"`
	Cases         []unitsFixtureCase `json:"cases"`
}

func unitsFixtureCases() []unitsFixtureCase {
	cases := []unitsFixtureCase{
		{Name: "결제 전·조달 계획 전 — 주문 접수", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{}},
		{Name: "조달 계획됨(자유 취소 구간)", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLANNED"}},
		{Name: "조달 실행 중", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACEMENT_PENDING"}},
		{Name: "조달 실패 — 해당 MO 환불 예정", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "FAILED"}},
		{Name: "취소로 대체됨", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{Fulfillment: "SUPERSEDED_BY_CANCELLATION"}},
		{Name: "배송 준비", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "AWAITING_EFFECT"}},
		{Name: "배송 중", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "IN_TRANSIT_EXPECTED"}},
		{Name: "수령 확인", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "DELIVERED_EXPECTED"}},
		{Name: "오배송 확인 중", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "WRONG_ACTUAL"}},
		{Name: "오배송 실물 회수 중", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "WRONG_ACTUAL",
				ReturnState: "RETURN_IN_TRANSIT"}},
		{Name: "환불 심사 중(MO 요청이 물류 상태를 이긴다)", RefundStatus: "REQUESTED",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "DELIVERED_EXPECTED"}},
		{Name: "환불 실행 중", RefundStatus: "REFUND_PENDING",
			Facts: UnitFacts{MerchantOrderState: "FAILED"}},
		{Name: "환불 완료", RefundStatus: "REFUNDED",
			Facts: UnitFacts{MerchantOrderState: "FAILED"}},
		{Name: "정상 수령 정정(판정 종결)", RefundStatus: "AVAILABLE",
			Facts: UnitFacts{MerchantOrderState: "PLACED", Fulfillment: "RESOLVED",
				ReturnState: "CLOSED"}},
	}
	for index := range cases {
		cases[index].Stage = DeriveUnitStage(cases[index].RefundStatus, cases[index].Facts)
	}
	return cases
}

func TestUnitsGoldenFixture(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve fixture path")
	}
	fixturePath := filepath.Clean(filepath.Join(filepath.Dir(filename),
		"../../../../../shared/openapi/fixtures", unitsFixtureName))
	fixture := unitsFixture{
		SchemaVersion: "vitlane.agency-order-units.v2",
		Stages: []UnitStage{
			UnitOrdered, UnitProcuring, UnitProcurementFailed, UnitCancelled,
			UnitAwaitingShipment, UnitInTransit, UnitDelivered, UnitException,
			UnitReturnInProgress, UnitRefundRequested, UnitRefundPending,
			UnitRefunded,
		},
		Cases: unitsFixtureCases(),
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(fixture); err != nil {
		t.Fatal(err)
	}
	want := buffer.Bytes()
	if os.Getenv("UPDATE_CONTRACT_FIXTURES") == "1" {
		if err := os.WriteFile(fixturePath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture (run with UPDATE_CONTRACT_FIXTURES=1 to create): %v", err)
	}
	if !bytes.Equal(stored, want) {
		t.Fatal("units fixture is stale — regenerate with UPDATE_CONTRACT_FIXTURES=1 " +
			"and review the FE contract impact")
	}
	// fixture 불변 조건: 모든 case의 stage는 선언된 어휘 안에 있다.
	declared := map[UnitStage]bool{}
	for _, stage := range fixture.Stages {
		declared[stage] = true
	}
	for _, c := range fixture.Cases {
		if !declared[c.Stage] {
			t.Fatalf("case %q stage %s not in declared vocabulary", c.Name, c.Stage)
		}
	}
}
