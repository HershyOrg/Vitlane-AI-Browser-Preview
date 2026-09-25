package app

import (
	"context"
	"testing"
	"time"

	"github.com/vitlane/vitlane/server/internal/ordering/logistics/domain"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeRepository struct {
	Repository
	createCalls     int
	deliveredCalls  int
	eventCalls      int
	resolutionCalls int
	resolution      domain.DeliveryResolution
}

func (r *fakeRepository) ResolveDeliveryException(
	_ context.Context, _ string, _ domain.ResolutionDecision, note, _ string, _ time.Time,
) (domain.DeliveryResolution, bool, error) {
	r.resolutionCalls++
	r.resolution.Note = note
	return r.resolution, false, nil
}

func (r *fakeRepository) CreateShipment(context.Context, string, string, string, string, []string, time.Time) (domain.Shipment, error) {
	r.createCalls++
	return domain.Shipment{}, nil
}

func (r *fakeRepository) RecordEvent(context.Context, string, string, string, string, time.Time, time.Time) (domain.Shipment, error) {
	r.eventCalls++
	return domain.Shipment{}, nil
}

func (r *fakeRepository) ConfirmDelivered(context.Context, string, string, map[string]string, time.Time) (domain.Shipment, error) {
	r.deliveredCalls++
	return domain.Shipment{}, nil
}

// 빈 carrier/tracking은 repository 도달 전에 거절된다.
func TestCreateShipmentRejectsInvalidInput(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeClock{now: time.Unix(1700000000, 0)})
	if _, err := service.CreateShipment(
		context.Background(), "mo-1", " ", "TRACK-1", "operator-1", nil,
	); err != domain.ErrShipmentInvalid {
		t.Fatalf("expected ErrShipmentInvalid, got %v", err)
	}
	if repository.createCalls != 0 {
		t.Fatal("invalid input must not reach the repository")
	}
}

// 수령 예외는 MISSING/WRONG_ACTUAL 두 갈래만 허용한다(§9.1 — 그 외 해소는
// Step 5B의 resolution 경로다).
func TestConfirmDeliveredValidatesExceptionKinds(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeClock{now: time.Unix(1700000000, 0)})
	if _, err := service.ConfirmDelivered(
		context.Background(), "shipment-1", "operator-1",
		map[string]string{"unit-1": "LOST"},
	); err != domain.ErrShipmentInvalid {
		t.Fatalf("expected ErrShipmentInvalid for LOST exception, got %v", err)
	}
	if repository.deliveredCalls != 0 {
		t.Fatal("invalid exception must not reach the repository")
	}
	if _, err := service.ConfirmDelivered(
		context.Background(), "shipment-1", "operator-1",
		map[string]string{"unit-1": "MISSING", "unit-2": "WRONG_ACTUAL"},
	); err != nil {
		t.Fatalf("valid exceptions rejected: %v", err)
	}
	if repository.deliveredCalls != 1 {
		t.Fatalf("delivered calls = %d", repository.deliveredCalls)
	}
}

// tracking event는 status·발생 시각이 없으면 evidence로 성립하지 않는다.
func TestRecordEventRequiresStatusAndTime(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeClock{now: time.Unix(1700000000, 0)})
	if _, err := service.RecordEvent(
		context.Background(), "shipment-1", "  ", "", "operator-1",
		time.Unix(1700000100, 0),
	); err != domain.ErrShipmentInvalid {
		t.Fatalf("expected ErrShipmentInvalid for empty status, got %v", err)
	}
	if _, err := service.RecordEvent(
		context.Background(), "shipment-1", "in_transit", "", "operator-1",
		time.Time{},
	); err != domain.ErrShipmentInvalid {
		t.Fatalf("expected ErrShipmentInvalid for zero time, got %v", err)
	}
	if _, err := service.RecordEvent(
		context.Background(), "shipment-1", "in_transit", "", "operator-1",
		time.Unix(1700000100, 0),
	); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if repository.eventCalls != 1 {
		t.Fatalf("event calls = %d", repository.eventCalls)
	}
}

func TestResolveDeliveryExceptionRequiresPublicRationale(t *testing.T) {
	repository := &fakeRepository{resolution: domain.DeliveryResolution{
		ID: "resolution-1", AgencyOrderID: "order-1",
		Decision: domain.ResolutionDeliveredOK,
	}}
	service := NewService(repository, fakeClock{now: time.Unix(1700000000, 0)})

	if _, _, err := service.ResolveDeliveryException(
		context.Background(), "unit-1", "DELIVERED_OK", "  ", "operator-1",
	); err != domain.ErrResolutionInvalid {
		t.Fatalf("blank public rationale must fail, got %v", err)
	}
	if repository.resolutionCalls != 0 {
		t.Fatal("invalid rationale reached persistence")
	}
	resolution, replayed, err := service.ResolveDeliveryException(
		context.Background(), "unit-1", "DELIVERED_OK",
		"  Carrier evidence confirms normal receipt.  ", "operator-1",
	)
	if err != nil || replayed {
		t.Fatalf("resolution=%+v replayed=%v err=%v", resolution, replayed, err)
	}
	if resolution.Note != "Carrier evidence confirms normal receipt." || resolution.ID != "resolution-1" {
		t.Fatalf("resolution=%+v", resolution)
	}
}
