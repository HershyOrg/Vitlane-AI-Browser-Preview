package app

import (
	"context"
	"errors"
	"testing"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

type actionAdmissionCreator struct {
	lockCalls   int
	planActive  bool
	activeCount int
}

func (c *actionAdmissionCreator) CreatePlanningJob(
	context.Context,
	CreatePlanningJobInput,
) (IntelligenceJobRef, error) {
	return IntelligenceJobRef{}, nil
}

func (c *actionAdmissionCreator) LockActionAdmission(
	context.Context,
	string,
) error {
	c.lockCalls++
	return nil
}

func (c *actionAdmissionCreator) PlanHasActiveWork(
	context.Context,
	string,
	string,
) (bool, error) {
	return c.planActive, nil
}

func (c *actionAdmissionCreator) ActiveActionCount(
	context.Context,
	string,
) (int, error) {
	return c.activeCount, nil
}

func TestGuardActionConcurrencyLocksBeforePlanAndUserChecks(t *testing.T) {
	creator := &actionAdmissionCreator{
		activeCount: curationdomain.MaxConcurrentUserActions,
	}
	service := &Service{intelligenceWork: creator}

	err := service.guardActionConcurrency(
		context.Background(), "user-1", "plan-1",
	)
	if !errors.Is(err, curationdomain.ErrTooManyActiveActions) {
		t.Fatalf("error = %v", err)
	}
	if creator.lockCalls != 1 {
		t.Fatalf("admission lock calls = %d", creator.lockCalls)
	}
	if code := ReasonCode(err); code != curationdomain.ErrTooManyActiveActions.Error() {
		t.Fatalf("reason code = %q", code)
	}
}

func TestGuardActionConcurrencyRejectsBusyPlanAfterLock(t *testing.T) {
	creator := &actionAdmissionCreator{planActive: true}
	service := &Service{intelligenceWork: creator}

	err := service.guardActionConcurrency(
		context.Background(), "user-1", "plan-1",
	)
	if !errors.Is(err, curationdomain.ErrExpansionInProgress) {
		t.Fatalf("error = %v", err)
	}
	if creator.lockCalls != 1 {
		t.Fatalf("admission lock calls = %d", creator.lockCalls)
	}
}
