package app

import (
	"context"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/procurement/domain"
)

type decisionNotificationRepository struct {
	Repository
	ManualReviewRepository
	record domain.DecisionRecord
}

func (r *decisionNotificationRepository) RecordManualDecision(
	_ context.Context,
	taskID, operatorUserID, _ string,
	decision domain.ManualDecision,
	publicRationale, internalNote, observedCondition string,
	evidenceSource domain.EvidenceSource,
	evidenceHash string,
	observedAt, now time.Time,
) (domain.DecisionRecord, bool, error) {
	r.record = domain.DecisionRecord{
		ID: "decision-1", MerchantOrderID: "merchant-order-1",
		AgencyOrderID: "agency-order-1", TaskID: taskID,
		Decision: decision, PublicRationale: publicRationale,
		InternalNote: internalNote, ObservedCondition: observedCondition,
		EvidenceSource: evidenceSource, EvidenceHash: evidenceHash,
		ObservedAt: observedAt, DecidedByUserID: operatorUserID,
		CreatedAt: now,
	}
	return r.record, false, nil
}

// 고객 카드 정책(정상·경미 판단은 내부 감사, 구매 불가만 카드)은 리듀서가
// procurement.decision.recorded 이벤트에서 판정한다(ADR-0070 §4.5 —
// process/domain 테스트). 여기서는 판단 기록이 세 값 모두에서 owner 사실로
// commit되는 것만 본다.
func TestManualDecisionRecordsEveryDecisionKind(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		decision domain.ManualDecision
	}{
		{decision: domain.DecisionWithinAuthorization},
		{decision: domain.DecisionImmaterialVariance},
		{decision: domain.DecisionUnableToPurchase},
	}
	for _, test := range tests {
		t.Run(string(test.decision), func(t *testing.T) {
			repository := &decisionNotificationRepository{}
			service := NewService(repository, nil, nil, fakeClock{now: now}, false)

			_, replay, err := service.RecordManualDecision(
				context.Background(), "task-1", "operator-1", "decision-key-0001",
				RecordManualDecisionInput{
					Decision:          test.decision,
					PublicRationale:   "This rationale is long enough for the audit record.",
					ObservedCondition: "The merchant condition was observed.",
					EvidenceSource:    domain.EvidenceMerchantPage,
					EvidenceHash:      "0123456789abcdef",
					ObservedAt:        now,
				},
			)
			if err != nil || replay {
				t.Fatalf("record decision: replay=%v err=%v", replay, err)
			}
			if repository.record.Decision != test.decision {
				t.Fatalf("recorded decision=%s want=%s", repository.record.Decision, test.decision)
			}
		})
	}
}

func TestMaterialDecisionRequiresCombinedCustomerRequestCommand(t *testing.T) {
	now := time.Date(2026, 8, 27, 16, 0, 0, 0, time.UTC)
	repository := &decisionNotificationRepository{}
	service := NewService(repository, nil, nil, fakeClock{now: now}, false)
	_, _, err := service.RecordManualDecision(
		context.Background(), "task-1", "operator-1", "decision-key-0001",
		RecordManualDecisionInput{
			Decision:          domain.DecisionMaterialCondition,
			PublicRationale:   "This material condition requires a customer response.",
			ObservedCondition: "The merchant condition was observed.",
			EvidenceSource:    domain.EvidenceMerchantPage,
			EvidenceHash:      "0123456789abcdef",
			ObservedAt:        now,
		},
	)
	if err != domain.ErrDecisionInvalid {
		t.Fatalf("standalone material decision error=%v", err)
	}
}
