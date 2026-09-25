package app

import (
	"context"
	"time"

	sharedapp "github.com/vitlane/vitlane/server/internal/shared/app"
)

// GIWAIntakeRepository는 GIWA FINALIZED 수납의 중립 합류를 소유한다.
type GIWAIntakeRepository interface {
	// IntakeGIWAFinalized는 finalized GIWA 수납을 중립 CustomerPayment
	// (CAPTURED)+FundsReceipt로 멱등 합류시킨다. payment 소유 테이블에만 쓴다.
	IntakeGIWAFinalized(ctx context.Context, now time.Time) (int, error)
}

// GIWAIntake는 GIWA rail의 수납 합류 sweep이다(ADR-0055 §4 — 종전에는
// agencyorder lifecycle이 payment 테이블에 직접 INSERT했다). 합류된 CAPTURED
// 상태를 process 리듀서가 facts로 관찰해 stage를 전진시킨다.
type GIWAIntake struct {
	repository GIWAIntakeRepository
	clock      sharedapp.Clock
}

func NewGIWAIntake(repository GIWAIntakeRepository, clock sharedapp.Clock) *GIWAIntake {
	return &GIWAIntake{repository: repository, clock: clock}
}

func (s *GIWAIntake) Tick(ctx context.Context) error {
	_, err := s.repository.IntakeGIWAFinalized(ctx, s.clock.Now())
	return err
}
