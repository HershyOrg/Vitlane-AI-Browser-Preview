package app

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	researchdomain "github.com/vitlane/vitlane/server/internal/curation/research/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	"github.com/vitlane/vitlane/server/internal/shared/fault"
)

func TestCompileCandidateRankingV2FiltersAndRanksDeterministically(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC)
	intent := candidateRankingIntentV2(t)
	observations := []CandidateRankingObservationV2{
		candidateRankingObservedV2(
			"product-low",
			"Unused waterproof trail running shoes",
			"New catalog product.",
			0,
			12000,
			"USD",
			now,
		),
		candidateRankingObservedV2(
			"product-high",
			"Black lightweight waterproof trail running shoes",
			"New rubber footwear for wet weather.",
			2,
			14000,
			"USD",
			now,
		),
		candidateRankingObservedV2(
			"product-boundary",
			"Waterproofing trail running shoes",
			"New rubber footwear.",
			1,
			10000,
			"USD",
			now,
		),
		candidateRankingObservedV2(
			"product-excluded",
			"Used waterproof trail running shoes",
			"New rubber footwear.",
			3,
			9000,
			"USD",
			now,
		),
		candidateRankingObservedV2(
			"product-currency",
			"Waterproof trail running shoes",
			"New rubber footwear.",
			4,
			9000,
			"EUR",
			now,
		),
	}
	invalidLocator := candidateRankingObservedV2(
		"product-locator",
		"Waterproof trail running shoes",
		"New rubber footwear.",
		5,
		9000,
		"USD",
		now,
	)
	invalidLocator.Admission.Locator.ProductURL.CanonicalURL = "http://unsafe.example/p"
	invalidLocator.Admission.Observation.Locator = &invalidLocator.Admission.Locator
	observations = append(observations, invalidLocator)

	result, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       intent,
		SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: observations,
		TrustedNow:   now,
	})
	if err != nil {
		t.Fatalf("compile ranking: %v", err)
	}
	if got := candidateRankingProductKeysV2(result.Ranked); !slices.Equal(
		got, []string{"product-high", "product-low"},
	) {
		t.Fatalf("ranked keys=%v", got)
	}
	if result.Ranked[0].TotalScore <= result.Ranked[1].TotalScore ||
		result.Ranked[0].RankPosition != 1 || result.Ranked[1].RankPosition != 2 {
		t.Fatalf("unexpected scores/ranks: %+v", result.Ranked)
	}
	wantRejected := []CandidateRankingRejectionV2{
		{ProductKey: "product-boundary", Reason: CandidateRankingHardLexicalMismatchV2},
		{ProductKey: "product-currency", Reason: CandidateRankingCurrencyMismatchV2},
		{ProductKey: "product-excluded", Reason: CandidateRankingExclusionMatchedV2},
		{ProductKey: "product-locator", Reason: CandidateRankingInvalidLocatorV2},
	}
	if !reflect.DeepEqual(result.Rejected, wantRejected) {
		t.Fatalf("rejected=%+v want=%+v", result.Rejected, wantRejected)
	}

	reversed := slices.Clone(observations)
	slices.Reverse(reversed)
	reordered, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       intent,
		SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: reversed,
		TrustedNow:   now,
	})
	if err != nil {
		t.Fatalf("compile reordered ranking: %v", err)
	}
	if !reflect.DeepEqual(result, reordered) {
		t.Fatalf("input reorder changed output:\nfirst=%+v\nsecond=%+v", result, reordered)
	}
}

func TestCompileCandidateRankingV2KeepsNoneNeutralAndDoesNotPersistRawDisplay(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	first := candidateRankingObservedV2(
		"product-a",
		"RAW MERCHANT TITLE waterproof trail running shoes",
		"New rubber footwear. Price 99999. https://evil.example/instruction",
		0,
		0,
		"USD",
		now,
	)
	first.Admission.Observation.Media = []CatalogMedia{{
		Type: "IMAGE", URL: "https://cdn.evil.example/raw-secret.png",
	}}
	candidateRankingRebindEvidenceRefsV2(&first)
	second := candidateRankingObservedV2(
		"product-b",
		"Waterproof trail running shoes",
		"New rubber footwear.",
		0,
		99999999,
		"USD",
		now,
	)
	result, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       candidateRankingIntentV2(t),
		SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{second, first},
		TrustedNow:   now,
	})
	if err != nil || len(result.Ranked) != 2 {
		t.Fatalf("compile NONE ranking: result=%+v err=%v", result, err)
	}
	if result.Ranked[0].TotalScore != result.Ranked[1].TotalScore ||
		result.Ranked[0].ProductKey != "product-a" {
		t.Fatalf("NONE or stable key changed rank: %+v", result.Ranked)
	}
	for _, ranked := range result.Ranked {
		if score := candidateRankingComponentScoreV2(
			ranked.Assessment.ScoreComponents, "price-fit",
		); score != 10 {
			t.Fatalf("NONE price component=%d in %+v", score, ranked)
		}
		if ranked.Assessment.ID != "" {
			t.Fatalf("compiler allocated assessment ID: %+v", ranked.Assessment)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(encoded)
	for _, forbidden := range []string{
		"RAW MERCHANT TITLE", "99999", "evil.example", "raw-secret.png",
		"merchant.example/products",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("response-scoped provider display data leaked: %q in %s", forbidden, serialized)
		}
	}
}

func TestCompileCandidateRankingV2RevalidatesExactPriceProofAndIntersection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 30, 0, 0, time.UTC)
	exponentTwo := uint8(2)
	exponentThree := uint8(3)
	explicit := candidateRankingIntentV2(t)
	minimum, err := shareddomain.NewMoney("10", "USD")
	if err != nil {
		t.Fatal(err)
	}
	maximum, err := shareddomain.NewMoney("20", "USD")
	if err != nil {
		t.Fatal(err)
	}
	explicit.PriceConstraint, err = shareddomain.NewExplicitResearchPriceConstraint(
		&minimum, &maximum,
	)
	if err != nil {
		t.Fatal(err)
	}

	valid := candidateRankingObservedV2(
		"product-priced", "Black waterproof trail running shoes",
		"New rubber footwear.", 0, 1500, "USD", now,
	)
	candidateRankingApplyPriceProofV2(&valid, int64PointerV2(1000), int64PointerV2(2000), "USD")
	result, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent: explicit, SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{valid}, TrustedNow: now,
		CurrencyExponent: &exponentTwo,
	})
	if err != nil || len(result.Ranked) != 1 || len(result.Rejected) != 0 {
		t.Fatalf("valid explicit price was rejected: result=%+v err=%v", result, err)
	}

	tests := []struct {
		name        string
		observation CandidateRankingObservationV2
		exponent    *uint8
	}{
		{
			name: "explicit intent with absent proof",
			observation: candidateRankingObservedV2(
				"product-no-price", "Black waterproof trail running shoes",
				"New rubber footwear.", 0, 1500, "USD", now,
			),
			exponent: &exponentTwo,
		},
		{
			name: "wrong proof band",
			observation: candidateRankingPricedObservationV2(
				"product-wrong-band", now, 1500, "USD", 900, 1900, "USD",
			),
			exponent: &exponentTwo,
		},
		{
			name: "out of range observation",
			observation: candidateRankingPricedObservationV2(
				"product-outside", now, 2500, "USD", 1000, 2000, "USD",
			),
			exponent: &exponentTwo,
		},
		{
			name: "wrong proof currency",
			observation: candidateRankingPricedObservationV2(
				"product-wrong-currency", now, 1500, "USD", 1000, 2000, "EUR",
			),
			exponent: &exponentTwo,
		},
		{
			name: "wrong reviewed exponent",
			observation: candidateRankingPricedObservationV2(
				"product-wrong-exponent", now, 1500, "USD", 1000, 2000, "USD",
			),
			exponent: &exponentThree,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, compileErr := CompileCandidateRankingV2(
				CompileCandidateRankingV2Input{
					Intent:       explicit,
					SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
					Observations: []CandidateRankingObservationV2{test.observation},
					TrustedNow:   now, CurrencyExponent: test.exponent,
				},
			)
			if compileErr != nil || len(result.Ranked) != 0 ||
				len(result.Rejected) != 1 ||
				result.Rejected[0].Reason != CandidateRankingPriceMismatchV2 {
				t.Fatalf("price mismatch was admitted: result=%+v err=%v", result, compileErr)
			}
		})
	}
}

func TestCompileCandidateRankingV2PreservesExplicitZeroAndNonePriceAbsence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 45, 0, 0, time.UTC)
	exponent := uint8(2)
	zeroIntent := candidateRankingIntentV2(t)
	zero, err := shareddomain.NewMoney("0", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zeroIntent.PriceConstraint, err = shareddomain.NewExplicitResearchPriceConstraint(
		&zero, &zero,
	)
	if err != nil {
		t.Fatal(err)
	}
	zeroObserved := candidateRankingPricedObservationV2(
		"product-zero", now, 0, "USD", 0, 0, "USD",
	)
	result, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent: zeroIntent, SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{zeroObserved}, TrustedNow: now,
		CurrencyExponent: &exponent,
	})
	if err != nil || len(result.Ranked) != 1 {
		t.Fatalf("explicit zero lost: result=%+v err=%v", result, err)
	}

	none := candidateRankingIntentV2(t)
	injected := candidateRankingPricedObservationV2(
		"product-injected", now, 0, "USD", 0, 0, "USD",
	)
	result, err = CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent: none, SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{injected}, TrustedNow: now,
	})
	if err != nil || len(result.Ranked) != 0 || len(result.Rejected) != 1 ||
		result.Rejected[0].Reason != CandidateRankingPriceMismatchV2 {
		t.Fatalf("NONE accepted an injected price proof: result=%+v err=%v", result, err)
	}
}

func TestCompileCandidateRankingV2EmitsPolicySpecificDomainCompatibleAssessment(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	observed := candidateRankingObservedV2(
		"product-1",
		"Black lightweight waterproof trail running shoes",
		"New rubber footwear for wet weather.",
		0,
		7600,
		"USD",
		now.Add(-time.Minute),
	)
	for _, policy := range []researchdomain.CandidateAssessmentSourcePolicyV2{
		researchdomain.CandidateAssessmentResearchRankingV1,
		researchdomain.CandidateAssessmentExpansionV1,
	} {
		policy := policy
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			result, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
				Intent:       candidateRankingIntentV2(t),
				SourcePolicy: policy,
				Observations: []CandidateRankingObservationV2{observed},
				TrustedNow:   now,
			})
			if err != nil || len(result.Ranked) != 1 {
				t.Fatalf("compile policy %s: result=%+v err=%v", policy, result, err)
			}
			ranked := result.Ranked[0]
			if ranked.RankingPolicyVersion != string(policy) ||
				ranked.Assessment.SourcePolicy != policy ||
				!ranked.Assessment.SourceObservedAt.Equal(observed.ObservedAt) {
				t.Fatalf("policy/evidence mismatch: %+v", ranked)
			}
			if policy != researchdomain.CandidateAssessmentResearchRankingV1 {
				return
			}

			assessment := ranked.Assessment
			assessment.ID = "assessment-1"
			pool, poolErr := researchdomain.NewCandidatePoolV2("target-1")
			if poolErr != nil {
				t.Fatal(poolErr)
			}
			commit, commitErr := researchdomain.FinalizeCandidateBatchV2(
				researchdomain.CandidatePoolSnapshotV2{Pool: pool},
				researchdomain.CandidateBatchFinalizeInputV2{
					BatchID: "batch-1", ExpectedPoolVersion: pool.Version,
					ProcessSourceKind: researchdomain.CandidateBatchResearchRoundV2,
					ProcessSourceID:   "round-1",
					Outcome:           researchdomain.CandidateBatchFinalizeSuccessV2,
					CompletedAt:       now,
					Admissions: []researchdomain.CandidateAdmissionInputV2{{
						CandidateID: "candidate-1", IdentityKey: "SHOPIFY_UCP:product-1",
						InitialLocatorRef: "safe:product-1", DiscoveryID: "discovery-1",
						LocatorRef:              "safe:product-1",
						ObservationEvidenceRefs: []string{"sha256:evidence-a", "sha256:evidence-z"},
						RankPosition:            ranked.RankPosition,
						RankingPolicyVersion:    ranked.RankingPolicyVersion,
						DiscoveredAt:            now, Assessment: assessment,
					}},
				},
			)
			if commitErr != nil || len(commit.Assessments) != 1 {
				t.Fatalf("materialize generated assessment: commit=%+v err=%v", commit, commitErr)
			}
		})
	}
}

func TestCompileCandidateRankingV2RejectsUnsafeEvidenceAndAmbiguousInputs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	unsafe := candidateRankingObservedV2(
		"product-unsafe",
		"Waterproof trail running shoes",
		"New rubber footwear.",
		0,
		7600,
		"USD",
		now,
	)
	unsafe.EvidenceRefs = []string{"https://cdn.example/raw.png"}
	result, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       candidateRankingIntentV2(t),
		SourcePolicy: researchdomain.CandidateAssessmentExpansionV1,
		Observations: []CandidateRankingObservationV2{unsafe},
		TrustedNow:   now,
	})
	if err != nil || len(result.Ranked) != 0 || !reflect.DeepEqual(
		result.Rejected,
		[]CandidateRankingRejectionV2{{
			ProductKey: "product-unsafe", Reason: CandidateRankingFreshEvidenceRequiredV2,
		}},
	) {
		t.Fatalf("unsafe evidence result=%+v err=%v", result, err)
	}

	duplicate := candidateRankingObservedV2(
		"duplicate",
		"Waterproof trail running shoes",
		"New rubber footwear.",
		0,
		7600,
		"USD",
		now,
	)
	_, err = CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       candidateRankingIntentV2(t),
		SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{duplicate, duplicate},
		TrustedNow:   now,
	})
	classified, ok := fault.As(err)
	if !ok || classified.Code != fault.InvalidInput ||
		classified.Reason != candidateRankingCompileInvalidV2 {
		t.Fatalf("duplicate fault=%v", err)
	}
}

func TestCompileCandidateRankingV2RejectsStaleFutureAndMismatchedEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	base := candidateRankingObservedV2(
		"product-evidence", "Black waterproof trail running shoes",
		"New rubber footwear.", 0, 7600, "USD", now.Add(-time.Minute),
	)

	expired := base
	expired.ExpiresAt = now
	candidateRankingRebindEvidenceRefsV2(&expired)
	future := base
	future.ObservedAt = now.Add(time.Second)
	future.ExpiresAt = now.Add(time.Minute)
	candidateRankingRebindEvidenceRefsV2(&future)
	missingExactHash := base
	missingExactHash.EvidenceRefs = slices.DeleteFunc(
		slices.Clone(base.EvidenceRefs),
		func(value string) bool { return value == base.ObservationEvidenceHash },
	)
	mismatchedFilter := base
	mismatchedFilter.Admission.HardFilters.AppliedFilters.Filters.Conditions = []string{"used"}
	var err error
	mismatchedFilter.Admission.HardFilters.AppliedFilters.EvidenceHash, err =
		catalogAppliedFilterProofHashV2(
			mismatchedFilter.Admission.HardFilters.AppliedFilters,
		)
	if err != nil {
		t.Fatal(err)
	}
	physical, ok, err := newCatalogPhysicalEligibilityProofV2(
		mismatchedFilter.Admission.HardFilters.AppliedFilters,
		mismatchedFilter.Admission.Observation,
	)
	if err != nil || !ok {
		t.Fatalf("rebuild physical proof: ok=%v err=%v", ok, err)
	}
	mismatchedFilter.Admission.HardFilters.PhysicalEligibility = physical
	candidateRankingRebindEvidenceRefsV2(&mismatchedFilter)
	tamperedProduct := base
	tamperedProduct.Admission.Observation.PriceRange.Minimum.AmountMinor++

	tests := []struct {
		name        string
		observation CandidateRankingObservationV2
	}{
		{name: "expired", observation: expired},
		{name: "future", observation: future},
		{name: "missing exact observation hash", observation: missingExactHash},
		{name: "proof does not equal intent", observation: mismatchedFilter},
		{name: "product changed without new observation hash", observation: tamperedProduct},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, compileErr := CompileCandidateRankingV2(
				CompileCandidateRankingV2Input{
					Intent:       candidateRankingIntentV2(t),
					SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
					Observations: []CandidateRankingObservationV2{test.observation},
					TrustedNow:   now,
				},
			)
			if compileErr != nil || len(result.Ranked) != 0 ||
				!reflect.DeepEqual(result.Rejected, []CandidateRankingRejectionV2{{
					ProductKey: "product-evidence",
					Reason:     CandidateRankingFreshEvidenceRequiredV2,
				}}) {
				t.Fatalf("untrusted evidence was admitted: result=%+v err=%v", result, compileErr)
			}
		})
	}
}

func TestCompileCandidateRankingV2RequiresTrustedClock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC)
	_, err := CompileCandidateRankingV2(CompileCandidateRankingV2Input{
		Intent:       candidateRankingIntentV2(t),
		SourcePolicy: researchdomain.CandidateAssessmentResearchRankingV1,
		Observations: []CandidateRankingObservationV2{candidateRankingObservedV2(
			"product-clock", "Black waterproof trail running shoes",
			"New rubber footwear.", 0, 7600, "USD", now,
		)},
	})
	classified, ok := fault.As(err)
	if !ok || classified.Code != fault.InvalidInput ||
		classified.Reason != candidateRankingCompileInvalidV2 {
		t.Fatalf("missing trusted clock fault=%v", err)
	}
}

func candidateRankingIntentV2(t *testing.T) researchdomain.TargetSearchIntentV2 {
	t.Helper()
	market, err := shareddomain.NewMarketContext("US", "USD")
	if err != nil {
		t.Fatal(err)
	}
	return researchdomain.TargetSearchIntentV2{
		ProductType:          "trail running shoes",
		UseCaseTerms:         []string{"wet weather"},
		MaterialTerms:        []string{"rubber"},
		HardLexicalTerms:     []string{"waterproof"},
		SoftLexicalTerms:     []string{"lightweight"},
		Exclusions:           []string{"used"},
		Colors:               []string{"Black"},
		Conditions:           []string{"NEW"},
		VerifiedCategories:   []string{"Footwear"},
		PriceConstraint:      shareddomain.NoResearchPriceConstraint(),
		MarketContext:        market,
		ShippingCountry:      market.Country,
		IntentSummaryEnglish: "Lightweight waterproof trail shoes for wet weather.",
		SourceProfileHash:    "sha256:profile-1",
	}
}

func candidateRankingObservedV2(
	productID, title, description string,
	providerOrder int,
	amountMinor int64,
	currency string,
	observedAt time.Time,
) CandidateRankingObservationV2 {
	locator := CatalogProductLocator{
		Kind: CatalogLocatorProductURL,
		ProductURL: &CatalogProductURLLocator{
			CanonicalURL: "https://merchant.example/products/" + productID,
		},
	}
	observation := CatalogProductObservation{
		ProviderProductID: productID,
		Title:             title,
		Description:       CatalogDescription{Plain: description},
		Locator:           &locator,
		PriceRange: CatalogPriceRange{
			Minimum: CatalogMoney{AmountMinor: amountMinor, Currency: currency},
			Maximum: CatalogMoney{AmountMinor: amountMinor, Currency: currency},
		},
		Categories:    []CatalogCategory{{Value: "Footwear", Taxonomy: "Shopify"}},
		ProviderOrder: providerOrder,
	}
	available := true
	request := CatalogProductSearchRequest{
		Query: "waterproof trail running shoes",
		Context: CatalogBuyerContext{
			Country: "US", Currency: "USD", Language: "en",
		},
		Filters: CatalogProductSearchFilters{
			Available: &available, ShipsTo: &CatalogDestination{Country: "US"},
			Categories: []string{"Footwear"}, Conditions: []string{"NEW"},
			Attributes: []CatalogAttributeFilter{{Name: "Color", Values: []string{"Black"}}},
		},
		Limit: 20,
	}
	applied, err := NewCatalogAppliedFilterProofV2(
		request, CatalogProviderShopifyGlobalV2, "2026-04-08", "catalog-request:"+productID,
		"dev.shopify.catalog.global", "2026-04-08", nil,
	)
	if err != nil {
		panic(err)
	}
	physical, ok, err := newCatalogPhysicalEligibilityProofV2(applied, observation)
	if err != nil || !ok {
		panic("candidate ranking physical evidence fixture is invalid")
	}
	observationID := "catalog-observation:" + productID
	expiresAt := observedAt.Add(10 * time.Minute)
	admission := CatalogAdmissionInputV2{
		Locator:     locator,
		Observation: observation,
		HardFilters: CatalogAdmissionHardFilterProofV2{
			AppliedFilters: applied, PhysicalEligibility: physical,
		},
	}
	observationHash, err := CatalogObservationEvidenceHashV2(
		admission, observationID, observedAt, expiresAt,
	)
	if err != nil {
		panic(err)
	}
	evidenceRefs := []string{
		observationID, observationHash, applied.EvidenceRef, applied.EvidenceHash,
		physical.EvidenceRef, physical.EvidenceHash,
	}
	return CandidateRankingObservationV2{
		Admission:             admission,
		ObservationEvidenceID: observationID, ObservationEvidenceHash: observationHash,
		EvidenceRefs: evidenceRefs, ObservedAt: observedAt,
		ExpiresAt: expiresAt,
	}
}

func candidateRankingProductKeysV2(
	values []RankedCandidateAssessmentV2,
) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ProductKey
	}
	return result
}

func candidateRankingRebindEvidenceRefsV2(observed *CandidateRankingObservationV2) {
	if observed == nil {
		return
	}
	applied := observed.Admission.HardFilters.AppliedFilters
	physical := observed.Admission.HardFilters.PhysicalEligibility
	hash, err := CatalogObservationEvidenceHashV2(
		observed.Admission, observed.ObservationEvidenceID,
		observed.ObservedAt, observed.ExpiresAt,
	)
	if err != nil {
		panic(err)
	}
	observed.ObservationEvidenceHash = hash
	observed.EvidenceRefs = []string{
		observed.ObservationEvidenceID, observed.ObservationEvidenceHash,
		applied.EvidenceRef, applied.EvidenceHash,
		physical.EvidenceRef, physical.EvidenceHash,
	}
}

func candidateRankingPricedObservationV2(
	productID string,
	observedAt time.Time,
	amountMinor int64,
	observationCurrency string,
	minimumMinor int64,
	maximumMinor int64,
	proofCurrency string,
) CandidateRankingObservationV2 {
	observed := candidateRankingObservedV2(
		productID, "Black waterproof trail running shoes",
		"New rubber footwear.", 0, amountMinor, observationCurrency, observedAt,
	)
	candidateRankingApplyPriceProofV2(
		&observed, int64PointerV2(minimumMinor), int64PointerV2(maximumMinor),
		proofCurrency,
	)
	return observed
}

func candidateRankingApplyPriceProofV2(
	observed *CandidateRankingObservationV2,
	minimumMinor *int64,
	maximumMinor *int64,
	currency string,
) {
	if observed == nil {
		panic("nil ranking observation")
	}
	available := true
	request := CatalogProductSearchRequest{
		Query: "waterproof trail running shoes",
		Context: CatalogBuyerContext{
			Country: "US", Currency: currency, Language: "en",
		},
		Filters: CatalogProductSearchFilters{
			Available: &available, ShipsTo: &CatalogDestination{Country: "US"},
			Categories: []string{"Footwear"}, Conditions: []string{"NEW"},
			Attributes: []CatalogAttributeFilter{{Name: "Color", Values: []string{"Black"}}},
			Price: &CatalogPriceFilter{
				MinimumMinor: minimumMinor, MaximumMinor: maximumMinor,
			},
		},
		Limit: 20,
	}
	applied, err := NewCatalogAppliedFilterProofV2(
		request, CatalogProviderShopifyGlobalV2, "2026-04-08",
		"catalog-request:"+observed.Admission.Observation.ProviderProductID,
		CatalogShopifyGlobalCapabilityV2, "2026-04-08", nil,
	)
	if err != nil {
		panic(err)
	}
	physical, ok, err := newCatalogPhysicalEligibilityProofV2(
		applied, observed.Admission.Observation,
	)
	if err != nil || !ok {
		panic("priced ranking physical evidence fixture is invalid")
	}
	observed.Admission.HardFilters = CatalogAdmissionHardFilterProofV2{
		AppliedFilters: applied, PhysicalEligibility: physical,
	}
	candidateRankingRebindEvidenceRefsV2(observed)
}

func int64PointerV2(value int64) *int64 {
	return &value
}

func candidateRankingComponentScoreV2(
	components []researchdomain.CandidateScoreComponentV2,
	name string,
) int {
	for _, component := range components {
		if component.Name == name {
			return component.Score
		}
	}
	return -1
}
