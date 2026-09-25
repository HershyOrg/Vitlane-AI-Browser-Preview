package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestRunSeparatesNavigationPreparationAndFinalUserPayment(t *testing.T) {
	now := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	candidate, err := NewCandidateReference(
		"user-1", "curation-1", "candidate-1",
		"https://SHOP.example/products/1?variant=2#tracking",
	)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.ProductURL != "https://shop.example/products/1?variant=2" ||
		candidate.MerchantOrigin != "https://shop.example" {
		t.Fatalf("canonical candidate=%+v", candidate)
	}
	run, err := NewRun("run-1", candidate, now)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != StateAwaitingNavigationApproval || run.ControlOwner != ControlNone {
		t.Fatalf("new run=%+v", run)
	}
	if err := run.ApproveNavigation(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.Preparation != nil || run.State != StateNavigationApproved {
		t.Fatalf("navigation approval must not approve preparation: %+v", run)
	}
	if err := run.RecordObservation(Observation{
		Revision: 1, Kind: ObservationPublicProduct,
		Origin: "https://shop.example", PageIdentityDigest: testDigest,
		SessionStateHint: SessionUnknown,
	}, true, false, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StateAwaitingPreparationApproval || run.ControlOwner != ControlNone {
		t.Fatalf("public product observation=%+v", run)
	}
	if err := run.ApprovePreparation(PreparationApproval{
		PlanRevision: 3, QuoteDigest: testDigest,
		AllowedPreparationSteps: []PreparationStep{
			StepOpenCheckoutReview, StepSelectVariant, StepAddToCart,
		},
		PriceCeilingMinor: 12900, PriceCurrency: "krw",
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StatePreparationApproved || run.ControlOwner != ControlAgent ||
		run.Preparation.PriceCurrency != "KRW" {
		t.Fatalf("preparation approval=%+v", run)
	}
	if err := run.RecordObservation(Observation{
		Revision: 2, Kind: ObservationCheckoutReview,
		Origin: "https://shop.example", PageIdentityDigest: testDigest,
		SessionStateHint: SessionAuthenticated,
	}, true, false, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StateReadyForUserPayment || run.ControlOwner != ControlUser ||
		run.HandoffReason != HandoffFinalPaymentRequired {
		t.Fatalf("checkout must hand final payment to user: %+v", run)
	}
	if err := run.RequestResume(now.Add(5 * time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("final payment must not resume agent control: %v", err)
	}
	if err := run.VerifyResult(ResultVerification{
		Outcome: OutcomeSucceeded, EvidenceSource: EvidenceUserReported,
	}, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StateCompleted || run.Result.EvidenceSource != EvidenceUserReported {
		t.Fatalf("user report attribution=%+v", run)
	}
}

func TestHandoffResumeRequiresLaterSanitizedObservation(t *testing.T) {
	run := preparedRun(t)
	now := run.UpdatedAt
	if err := run.RequireHandoff(HandoffSignInRequired, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StateUserControl || run.ResumeState != StatePreparationApproved {
		t.Fatalf("handoff=%+v", run)
	}
	if err := run.RequestResume(now.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StateResumeRequiresObservation || !run.FreshObservationRequired ||
		run.ControlOwner != ControlNone {
		t.Fatalf("resume request=%+v", run)
	}
	if err := run.RecordObservation(Observation{
		Revision: 2, Kind: ObservationPublicProduct,
		Origin: run.MerchantOrigin, PageIdentityDigest: testDigest,
		SessionStateHint: SessionUnknown,
	}, false, false, now.Add(3*time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsanitized observation must fail: %v", err)
	}
	if err := run.RecordObservation(Observation{
		Revision: 2, Kind: ObservationPublicProduct,
		Origin: run.MerchantOrigin, PageIdentityDigest: testDigest,
		SessionStateHint: SessionUnknown,
	}, true, false, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StatePreparationApproved || run.ControlOwner != ControlAgent ||
		run.LatestObservation.SessionStateHint != SessionUnknown {
		t.Fatalf("fresh public observation should resume without claiming login: %+v", run)
	}
}

func TestMerchantObservedResultNeedsMatchingFreshObservation(t *testing.T) {
	run := preparedRun(t)
	now := run.UpdatedAt
	if err := run.RecordObservation(Observation{
		Revision: 2, Kind: ObservationCheckoutReview,
		Origin: run.MerchantOrigin, PageIdentityDigest: testDigest,
	}, true, false, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := run.VerifyResult(ResultVerification{
		Outcome: OutcomeSucceeded, EvidenceSource: EvidenceMerchantObserved,
		EvidenceDigest: testDigest, ObservationRevision: 2,
	}, now.Add(2*time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("checkout observation is not result evidence: %v", err)
	}
	if err := run.RecordObservation(Observation{
		Revision: 3, Kind: ObservationResult,
		Origin: run.MerchantOrigin, PageIdentityDigest: testDigest,
		SessionStateHint: SessionAuthenticated,
	}, true, false, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := run.VerifyResult(ResultVerification{
		Outcome: OutcomeSucceeded, EvidenceSource: EvidenceMerchantObserved,
		EvidenceDigest: testDigest, ObservationRevision: 3,
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if run.State != StateCompleted || run.Result.EvidenceSource != EvidenceMerchantObserved {
		t.Fatalf("merchant result=%+v", run)
	}
}

func TestPreparationApprovalRejectsUnapprovedActions(t *testing.T) {
	run := preparedApprovalRun(t)
	err := run.ApprovePreparation(PreparationApproval{
		PlanRevision: 1, QuoteDigest: testDigest,
		AllowedPreparationSteps: []PreparationStep{"SUBMIT_PAYMENT"},
		PriceCeilingMinor:       100, PriceCurrency: "USD",
	}, run.UpdatedAt.Add(time.Second))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("payment action must be outside the closed preparation allowlist: %v", err)
	}
}

func TestCanonicalMerchantURLRejectsPrivateOrCredentialedTargets(t *testing.T) {
	for _, raw := range []string{
		"http://shop.example/item", "https://user:secret@shop.example/item",
		"https://localhost/item", "https://127.0.0.1/item", "https://merchant.local/item",
		"javascript:alert(1)",
	} {
		if _, _, _, err := CanonicalMerchantURL(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid merchant URL %q: %v", raw, err)
		}
	}
	if _, err := CanonicalOrigin("https://shop.example/path"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("origin with a path must fail: %v", err)
	}
}

func preparedApprovalRun(t *testing.T) Run {
	t.Helper()
	now := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	candidate, err := NewCandidateReference("user", "curation", "candidate", "https://shop.example/item")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := NewRun("run", candidate, now)
	if err := run.ApproveNavigation(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := run.RecordObservation(Observation{
		Revision: 1, Kind: ObservationPublicProduct,
		Origin: run.MerchantOrigin, PageIdentityDigest: testDigest,
	}, true, false, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	return run
}

func preparedRun(t *testing.T) Run {
	t.Helper()
	run := preparedApprovalRun(t)
	if err := run.ApprovePreparation(PreparationApproval{
		PlanRevision: 1, QuoteDigest: testDigest,
		AllowedPreparationSteps: []PreparationStep{StepAddToCart, StepOpenCheckoutReview},
		PriceCeilingMinor:       2000, PriceCurrency: "USD",
	}, run.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return run
}

func TestDigestFixtureIsCanonicalLength(t *testing.T) {
	if !strings.HasPrefix(testDigest, "sha256:") || !validDigest(testDigest) {
		t.Fatal("invalid test digest")
	}
}
