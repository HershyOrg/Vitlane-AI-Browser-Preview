package domain

import "testing"

// 상태는 금액에서만 파생된다(운영자 상태 입력 없음) — 소유자 단순화안의 계약.
func TestDeriveRecoveryState(t *testing.T) {
	cases := []struct {
		name               string
		expected, received int64
		want               string
	}{
		{"수취 전", 1000, 0, "EXPECTED"},
		{"부분 수취도 수취", 1000, 400, "RECEIVED"},
		{"정확 수취", 1000, 1000, "RECEIVED"},
		{"기대 초과", 1000, 1200, "OVER_RECOVERED"},
		{"기대 미기재 수취", 0, 700, "RECEIVED"},
		{"둘 다 0", 0, 0, "EXPECTED"},
	}
	for _, c := range cases {
		if got := DeriveRecoveryState(c.expected, c.received); got != c.want {
			t.Fatalf("%s: state=%s want %s", c.name, got, c.want)
		}
	}
}

func TestValidManualRecoveryCause(t *testing.T) {
	for _, cause := range []string{"MERCHANT_CANCEL", "COST_ADJUSTMENT", "RETURN", "OTHER"} {
		if !ValidManualRecoveryCause(cause) {
			t.Fatalf("cause %s must be manual-creatable", cause)
		}
	}
	// CHARGE_WITHOUT_ORDER는 LIVE 대사 자동 전용 — 수동 생성 금지.
	for _, cause := range []string{"CHARGE_WITHOUT_ORDER", "", "refund", "RETURN "} {
		if cause == "RETURN " {
			// trim 후 유효 — 공백 허용을 명시 확인.
			if !ValidManualRecoveryCause(cause) {
				t.Fatalf("trimmed cause must pass: %q", cause)
			}
			continue
		}
		if ValidManualRecoveryCause(cause) {
			t.Fatalf("cause %q must be rejected", cause)
		}
	}
}

func TestValidateRecoveryAmountsAndNote(t *testing.T) {
	if err := ValidateRecoveryAmounts(-1, 0); err == nil {
		t.Fatal("negative expected must be rejected")
	}
	if err := ValidateRecoveryAmounts(0, -1); err == nil {
		t.Fatal("negative received must be rejected")
	}
	if err := ValidateRecoveryAmounts(0, 0); err != nil {
		t.Fatalf("zero amounts are valid: %v", err)
	}
	long := make([]rune, 501)
	for index := range long {
		long[index] = '가'
	}
	if err := ValidateRecoveryNote(string(long)); err == nil {
		t.Fatal("501-rune note must be rejected")
	}
	if err := ValidateRecoveryNote("상점이 부분 환불함"); err != nil {
		t.Fatalf("short note is valid: %v", err)
	}
}
