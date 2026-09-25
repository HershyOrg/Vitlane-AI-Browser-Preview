// Package policy는 주문 계열(ordering)의 환불·수수료·지연 rule 정책의 유일한
// 코드 표현이다(ADR-0055 §2). 버전 붙은 상수와 순수 함수만 담는 의존성 0
// 패키지이며 런타임 서비스가 아니다 — 각 제품이 import해 같은 전표를 읽는다.
//
// 정책 절 앵커: ADR-0052 §1(3-등급 게이트), ADR-0065(immutable whole-MO
// gross compensation·rail별 MO 수수료 allocation).
package policy

import (
	"errors"
	"math"
	"math/bits"
	"slices"
	"sort"
	"strings"
)

var (
	ErrUnknownRail     = errors.New("ORDERING_POLICY_UNKNOWN_RAIL")
	ErrInvalidFeeInput = errors.New("ORDERING_POLICY_INVALID_FEE_INPUT")
	ErrFeeOverflow     = errors.New("ORDERING_POLICY_FEE_OVERFLOW")
)

// ---------------------------------------------------------------------------
// MO compensation 금액 basis (ADR-0065)
// ---------------------------------------------------------------------------

// RefundBasis is intentionally single-valued in the clean-cut model. Every
// accepted cancellation/refund returns the immutable whole-MO customer gross,
// including its allocated Vitlane fee.
type RefundBasis string

const BasisGross RefundBasis = "GROSS"

const DisclosureVersionMOGross = "AGENCY_ORDER_TEST_DISCLOSURE_2026_08_27_MO_GROSS_V1"

// CustomerCancelBasisFor는 고객 사유 취소(pre-effect)의 basis를 주문 발행
// 시점의 disclosure version에서 파생한다.
func CustomerCancelBasisFor(_ string) RefundBasis {
	return BasisGross
}

// ---------------------------------------------------------------------------
// owner process state cause→whole-MO basis 전표 (ADR-0065)
// ---------------------------------------------------------------------------

// cause는 각 owner process state가 compensation command로 투영하는 발생 경로다.
// Payment의 저장 어휘는 더 좁은 MOCompensationCause이며 amount basis는 항상 Gross다.
const (
	// CauseOrderFailure — 특정 MerchantOrder의 이행 실패.
	// mandatory GROSS(ADR-0042 §9 불변).
	CauseOrderFailure = "ORDER_FAILURE"
	// CauseCustomerRequest — 필수 사유 환불 요청의 운영자 승인. GROSS.
	CauseCustomerRequest = "CUSTOMER_REQUEST"
	// CauseCustomerCancelPreEffect — 결제 후·첫 merchant effect 전 자유 취소.
	// basis는 disclosure version으로 파생한다(CustomerCancelBasisFor).
	CauseCustomerCancelPreEffect = "CUSTOMER_CANCEL_PRE_EFFECT"
	// CauseCustomerCancelAfterPlacementConfirmed — 고객이 Shop 주문 뒤 취소를
	// 요청했고 Shop이 이를 승인한 경우다. merchant 과실이 아니며 고객 사유
	// 수수료 정책을 따른다.
	CauseCustomerCancelAfterPlacementConfirmed = "CUSTOMER_CANCEL_AFTER_PLACEMENT_CONFIRMED"
	// CauseMerchantFault — 누락·오배송·분실 판정. 손실 감수 GROSS(ADR-0052 §2.5).
	CauseMerchantFault = "MERCHANT_FAULT"
	// CauseDelayRuleCancel — FTC 30일 지연 rule에 따른 whole-MO 취소.
	CauseDelayRuleCancel = "DELAY_RULE_CANCEL"
)

// BasisForCause remains as the shared policy entry point, but cause and
// disclosure can no longer change the amount basis.
func BasisForCause(_, _ string) RefundBasis {
	return BasisGross
}

// ---------------------------------------------------------------------------
// 대행 수수료와 immutable MO allocation (ADR-0065)
// ---------------------------------------------------------------------------

const (
	PayPalMOFeePolicyVersion = "PAYPAL_MO_PASS_THROUGH_540BPS_PLUS_30C_V1"
	TVITUSDCFeePolicyVersion = "TVITUSD_ORDER_PASS_THROUGH_100BPS_MO_ALLOC_V1"
)

// MerchantFeeBase는 발행 순서가 고정된 한 Shop checkout(MO)의 고객 승인
// pass-through다. CheckoutOrdinal은 1부터 시작하며 같은 주문에서 유일하다.
type MerchantFeeBase struct {
	CheckoutOrdinal  int
	PassThroughMinor int64
}

// MerchantFeeAllocation은 환불·capture·운영 장부가 재계산 없이 소비하는 MO별
// immutable 수수료 전표다.
type MerchantFeeAllocation struct {
	CheckoutOrdinal      int
	PassThroughMinor     int64
	VariableMinor        int64
	FixedMinor           int64
	TotalMinor           int64
	CustomerPayableMinor int64
}

// AgencyFeeQuote는 주문 합계와 MO별 allocation을 함께 보존한다. PayPal은 MO마다
// 540bps를 cent 올림하고 30 cent를 더한다. tVITUSDC는 주문 합계에 100bps를 한
// 번 cent 올림한 뒤 pass-through 비율 largest-remainder로 MO에 배분한다.
type AgencyFeeQuote struct {
	PassThroughMinor     int64
	VariableMinor        int64
	FixedMinor           int64
	TotalMinor           int64
	CustomerPayableMinor int64
	PolicyVersion        string
	MerchantOrders       []MerchantFeeAllocation
}

// CalculateAgencyFee는 rail별 수수료와 MO별 immutable allocation을 계산한다.
// 종전 order-total 단일 PayPal API는 의도적으로 제공하지 않는다. PayPal fixed
// fee가 MO 수에 종속되므로 호출자는 exact MerchantCheckout 집합을 전달해야 한다.
func CalculateAgencyFee(rail string, bases []MerchantFeeBase) (AgencyFeeQuote, error) {
	if len(bases) == 0 {
		return AgencyFeeQuote{}, ErrInvalidFeeInput
	}
	ordered := append([]MerchantFeeBase(nil), bases...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].CheckoutOrdinal < ordered[j].CheckoutOrdinal
	})
	var passThroughTotal int64
	for index, base := range ordered {
		if base.CheckoutOrdinal <= 0 || base.PassThroughMinor <= 0 ||
			(index > 0 && ordered[index-1].CheckoutOrdinal == base.CheckoutOrdinal) {
			return AgencyFeeQuote{}, ErrInvalidFeeInput
		}
		var err error
		passThroughTotal, err = checkedFeeAdd(passThroughTotal, base.PassThroughMinor)
		if err != nil {
			return AgencyFeeQuote{}, err
		}
	}

	quote := AgencyFeeQuote{
		PassThroughMinor: passThroughTotal,
		MerchantOrders:   make([]MerchantFeeAllocation, len(ordered)),
	}
	switch rail {
	case "PAYPAL_SANDBOX", "PAYPAL_LIVE":
		quote.PolicyVersion = PayPalMOFeePolicyVersion
		for index, base := range ordered {
			variable, err := ceilBasisPoints(base.PassThroughMinor, 540)
			if err != nil {
				return AgencyFeeQuote{}, err
			}
			total, err := checkedFeeAdd(variable, 30)
			if err != nil {
				return AgencyFeeQuote{}, err
			}
			payable, err := checkedFeeAdd(base.PassThroughMinor, total)
			if err != nil {
				return AgencyFeeQuote{}, err
			}
			quote.MerchantOrders[index] = MerchantFeeAllocation{
				CheckoutOrdinal: base.CheckoutOrdinal, PassThroughMinor: base.PassThroughMinor,
				VariableMinor: variable, FixedMinor: 30, TotalMinor: total,
				CustomerPayableMinor: payable,
			}
			quote.VariableMinor, err = checkedFeeAdd(quote.VariableMinor, variable)
			if err != nil {
				return AgencyFeeQuote{}, err
			}
			quote.FixedMinor, err = checkedFeeAdd(quote.FixedMinor, 30)
			if err != nil {
				return AgencyFeeQuote{}, err
			}
		}
	case "TVITUSD":
		quote.PolicyVersion = TVITUSDCFeePolicyVersion
		variable, err := ceilBasisPoints(passThroughTotal, 100)
		if err != nil {
			return AgencyFeeQuote{}, err
		}
		quote.VariableMinor = variable
		shares := allocateFeeLargestRemainder(variable, ordered, passThroughTotal)
		for index, base := range ordered {
			payable, addErr := checkedFeeAdd(base.PassThroughMinor, shares[index])
			if addErr != nil {
				return AgencyFeeQuote{}, addErr
			}
			quote.MerchantOrders[index] = MerchantFeeAllocation{
				CheckoutOrdinal: base.CheckoutOrdinal, PassThroughMinor: base.PassThroughMinor,
				VariableMinor: shares[index], TotalMinor: shares[index],
				CustomerPayableMinor: payable,
			}
		}
	default:
		return AgencyFeeQuote{}, ErrUnknownRail
	}
	var err error
	quote.TotalMinor, err = checkedFeeAdd(quote.VariableMinor, quote.FixedMinor)
	if err != nil {
		return AgencyFeeQuote{}, err
	}
	quote.CustomerPayableMinor, err = checkedFeeAdd(quote.PassThroughMinor, quote.TotalMinor)
	if err != nil {
		return AgencyFeeQuote{}, err
	}
	return quote, nil
}

func ceilBasisPoints(amount int64, bps int64) (int64, error) {
	if amount <= 0 || bps <= 0 || bps > 10_000 {
		return 0, ErrInvalidFeeInput
	}
	// quotient/remainder 계산으로 amount*bps의 int64 overflow를 피한다.
	quotient, remainder := amount/10_000, amount%10_000
	if quotient > math.MaxInt64/bps {
		return 0, ErrFeeOverflow
	}
	whole := quotient * bps
	fraction := (remainder*bps + 9_999) / 10_000
	return checkedFeeAdd(whole, fraction)
}

func checkedFeeAdd(left, right int64) (int64, error) {
	if left < 0 || right < 0 {
		return 0, ErrInvalidFeeInput
	}
	if left > math.MaxInt64-right {
		return 0, ErrFeeOverflow
	}
	return left + right, nil
}

func allocateFeeLargestRemainder(
	total int64,
	bases []MerchantFeeBase,
	passThroughTotal int64,
) []int64 {
	shares := make([]int64, len(bases))
	if total == 0 {
		return shares
	}
	type remainder struct {
		index   int
		value   uint64
		ordinal int
	}
	remainders := make([]remainder, len(bases))
	var assigned int64
	for index, base := range bases {
		high, low := bits.Mul64(uint64(total), uint64(base.PassThroughMinor))
		quotient, rest := bits.Div64(high, low, uint64(passThroughTotal))
		shares[index] = int64(quotient)
		assigned += shares[index]
		remainders[index] = remainder{index: index, value: rest, ordinal: base.CheckoutOrdinal}
	}
	sort.Slice(remainders, func(i, j int) bool {
		if remainders[i].value == remainders[j].value {
			return remainders[i].ordinal < remainders[j].ordinal
		}
		return remainders[i].value > remainders[j].value
	})
	for index := int64(0); index < total-assigned; index++ {
		shares[remainders[index].index]++
	}
	return shares
}

// ---------------------------------------------------------------------------
// FTC 30일 지연 rule (16 CFR 435 — ADR-0052 §1·§2.5)
// ---------------------------------------------------------------------------

// DelayRuleWindowDays는 발행 후 배송 미완 시 무료 취소 창이 열리는 고지·취소
// 기준일이다. 고지(process)와 취소 자격(procurement)이 같은 값을 쓴다.
const DelayRuleWindowDays = 30

// ---------------------------------------------------------------------------
// 고객 환불 요청 사유 어휘 (ADR-0052 §1 — 단순변심 OFF 게이트)
// ---------------------------------------------------------------------------

// RefundReasonCodes는 접수 가능한 typed 사유다. CHANGE_OF_MIND는 어휘에 없어
// 단순변심 요청이 구조적으로 성립하지 않는다(OFF는 기능 부재가 아니라 명시
// 게시가 요건 — 게시 문구는 disclosure가 소유한다).
var RefundReasonCodes = []string{
	"ITEM_NOT_RECEIVED", "ITEM_DAMAGED_DEFECTIVE", "WRONG_ITEM_RECEIVED",
	"ORDER_DELAYED", "OTHER_SERVICE_FAULT",
}

func ValidRefundReasonCode(code string) bool {
	return slices.Contains(RefundReasonCodes, code)
}

// ---------------------------------------------------------------------------
// 조달 실패 사유 어휘 (발행 스냅샷 밖 재량 사유 금지)
// ---------------------------------------------------------------------------

var ProcurementFailureCodes = []string{
	"VARIANT_UNAVAILABLE", "VARIANT_COMBINATION_INVALID", "VARIANT_AMBIGUOUS",
	"VARIANT_REQUIRES_CLARIFICATION", "PRODUCT_UNAVAILABLE", "PRICE_CHANGED",
	"SHIPPING_UNAVAILABLE",
}

func ValidProcurementFailureCode(code string) bool {
	return slices.Contains(ProcurementFailureCodes, strings.ToUpper(strings.TrimSpace(code)))
}
