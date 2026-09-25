package domain

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestAvailableCurationActionsArePhaseSpecificAndDeterministic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		phase   CurationActionPhase
		version int64
		want    []CurationActionType
	}{
		{
			name: "having intent", phase: CurationActionPhaseHavingIntent,
			version: 1,
			want:    []CurationActionType{CurationActionIntentNextStep},
		},
		{
			name: "planning", phase: CurationActionPhasePlanning, version: 3,
			want: []CurationActionType{
				CurationActionPlanningAddTargets,
				CurationActionPlanningStartCurating,
				CurationActionTargetRemove,
			},
		},
		{
			name: "curating", phase: CurationActionPhaseCurating, version: 7,
			want: []CurationActionType{
				CurationActionCurationAddTargets,
				CurationActionTargetResearchAgain,
				CurationActionTargetRemove,
				CurationActionSelectionMutation,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog, err := AvailableCurationActions(test.phase, test.version)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]CurationActionType, 0, len(catalog))
			for _, descriptor := range catalog {
				got = append(got, descriptor.ID)
				if !descriptor.Enabled || descriptor.UnavailableReason != "" {
					t.Fatalf("phase action is not available: %#v", descriptor)
				}
				if descriptor.ExpectedResourceVersion != test.version {
					t.Fatalf(
						"expected version %d, got %#v",
						test.version,
						descriptor,
					)
				}
				if descriptor.Alias == "" || descriptor.Alias[0] != '@' {
					t.Fatalf("invalid exact alias: %#v", descriptor)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("actions=%v want=%v", got, test.want)
			}
		})
	}
}

func TestCurationActionCatalogCarriesDomainPolicy(t *testing.T) {
	t.Parallel()

	having, err := AvailableCurationActions(
		CurationActionPhaseHavingIntent,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	next := findCurationActionDescriptor(
		t,
		having,
		CurationActionIntentNextStep,
	)
	if next.Alias != "@Intent-NextStep" ||
		next.SubjectSchema.Type != CurationActionSubjectIntent ||
		next.SubjectSchema.IDRequired ||
		next.BodySchema != CurationActionBodyTextRequired ||
		next.EffectKind != CurationActionEffectIntelligence ||
		next.TranscriptPolicy != CurationActionTranscriptAppend ||
		next.RequestedTransitionTo == nil ||
		*next.RequestedTransitionTo != CurationPhasePlanning {
		t.Fatalf("unexpected NextStep descriptor: %#v", next)
	}

	planning, err := AvailableCurationActions(
		CurationActionPhasePlanning,
		4,
	)
	if err != nil {
		t.Fatal(err)
	}
	start := findCurationActionDescriptor(
		t,
		planning,
		CurationActionPlanningStartCurating,
	)
	if start.BodySchema != CurationActionBodyNone ||
		start.EffectKind != CurationActionEffectIntelligence ||
		start.TranscriptPolicy != CurationActionTranscriptAppend ||
		!start.SubjectSchema.IDRequired ||
		start.RequestedTransitionTo == nil ||
		*start.RequestedTransitionTo != CurationPhaseCurating {
		t.Fatalf("unexpected StartCurating descriptor: %#v", start)
	}
	remove := findCurationActionDescriptor(
		t,
		planning,
		CurationActionTargetRemove,
	)
	if remove.EffectKind != CurationActionEffectNone ||
		remove.TranscriptPolicy != CurationActionTranscriptPatchOnly ||
		remove.BodySchema != CurationActionBodyNone {
		t.Fatalf("unexpected TargetRemove descriptor: %#v", remove)
	}

}

func TestAvailableCurationActionsReturnsDefensiveTransitionValues(t *testing.T) {
	t.Parallel()
	first, err := AvailableCurationActions(
		CurationActionPhaseHavingIntent,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	*first[0].RequestedTransitionTo = CurationPhaseCurating

	second, err := AvailableCurationActions(
		CurationActionPhaseHavingIntent,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second[0].RequestedTransitionTo == nil ||
		*second[0].RequestedTransitionTo != CurationPhasePlanning {
		t.Fatalf("caller mutated catalog definition: %#v", second[0])
	}
}

func TestAvailableCurationActionsRejectsInvalidPhaseVersionPairs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		phase   CurationActionPhase
		version int64
		want    error
	}{
		{
			name:  "having intent action requires created curation v1",
			phase: CurationActionPhaseHavingIntent, version: 0,
			want: ErrCurationActionVersionInvalid,
		},
		{
			name:  "planning requires persisted version",
			phase: CurationActionPhasePlanning, version: 0,
			want: ErrCurationActionVersionInvalid,
		},
		{
			name:  "curating requires persisted version",
			phase: CurationActionPhaseCurating, version: -1,
			want: ErrCurationActionVersionInvalid,
		},
		{
			name:  "unknown phase",
			phase: "PURCHASE", version: 1,
			want: ErrCurationActionPhaseInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := AvailableCurationActions(test.phase, test.version)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestParseCurationActionCommandUsesExactPrefix(t *testing.T) {
	t.Parallel()
	curationID := "curation-1"
	targetID := "target-1"
	tests := []struct {
		command string
		want    ParsedCurationActionCommand
	}{
		{
			command: "@Intent-NextStep: 무선 헤드폰을 찾아줘",
			want: ParsedCurationActionCommand{
				Type: CurationActionIntentNextStep, Alias: "@Intent-NextStep",
				SubjectType: CurationActionSubjectIntent,
				Body:        "무선 헤드폰을 찾아줘",
			},
		},
		{
			command: "@TargetList:curation-1-AddTarget: 의자와 조명을 추가해줘",
			want: ParsedCurationActionCommand{
				Type:        CurationActionPlanningAddTargets,
				Alias:       "@TargetList:curation-1-AddTarget",
				SubjectType: CurationActionSubjectTargetList,
				SubjectID:   &curationID,
				Body:        "의자와 조명을 추가해줘",
			},
		},
		{
			command: "@Planning:curation-1-NextStep",
			want: ParsedCurationActionCommand{
				Type:        CurationActionPlanningStartCurating,
				Alias:       "@Planning:curation-1-NextStep",
				SubjectType: CurationActionSubjectCuration,
				SubjectID:   &curationID,
			},
		},
		{
			command: "@Curation:curation-1-AddTarget: 책상을 추가해줘",
			want: ParsedCurationActionCommand{
				Type:        CurationActionCurationAddTargets,
				Alias:       "@Curation:curation-1-AddTarget",
				SubjectType: CurationActionSubjectCuration,
				SubjectID:   &curationID,
				Body:        "책상을 추가해줘",
			},
		},
		{
			command: "@Target:target-1-ResearchAgain: https://example.com:8443 기준으로",
			want: ParsedCurationActionCommand{
				Type:        CurationActionTargetResearchAgain,
				Alias:       "@Target:target-1-ResearchAgain",
				SubjectType: CurationActionSubjectTarget,
				SubjectID:   &targetID,
				Body:        "https://example.com:8443 기준으로",
			},
		},
		{
			command: "@Target:target-1-Remove",
			want: ParsedCurationActionCommand{
				Type: CurationActionTargetRemove, Alias: "@Target:target-1-Remove",
				SubjectType: CurationActionSubjectTarget,
				SubjectID:   &targetID,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCurationActionCommand(test.command)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parsed=%#v want=%#v", got, test.want)
			}
		})
	}
}

func TestParseCurationActionCommandRejectsInferenceAndMalformedBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command string
		want    error
	}{
		{name: "empty", command: "", want: ErrCurationActionCommandInvalid},
		{
			name:    "natural language without prefix",
			command: "다음 단계로 진행해줘",
			want:    ErrCurationActionCommandInvalid,
		},
		{
			name:    "unknown exact alias",
			command: "@Intent-DoSomething: body",
			want:    ErrCurationActionAliasUnknown,
		},
		{
			name:    "removed direct purchase alias",
			command: "@Candidate:candidate-1-PurchaseDirect",
			want:    ErrCurationActionAliasUnknown,
		},
		{
			name:    "removed cart purchase alias",
			command: "@Curation:curation-1-PurchaseCart",
			want:    ErrCurationActionAliasUnknown,
		},
		{
			name:    "case differs",
			command: "@intent-NextStep: body",
			want:    ErrCurationActionAliasUnknown,
		},
		{
			name:    "required body omitted",
			command: "@Intent-NextStep",
			want:    ErrCurationActionBodyInvalid,
		},
		{
			name:    "body forbidden",
			command: "@Planning:curation-1-NextStep: now",
			want:    ErrCurationActionBodyInvalid,
		},
		{
			name:    "missing exact space",
			command: "@Intent-NextStep:body",
			want:    ErrCurationActionCommandInvalid,
		},
		{
			name:    "extra prefix-body space",
			command: "@Intent-NextStep:  body",
			want:    ErrCurationActionCommandInvalid,
		},
		{
			name:    "empty body",
			command: "@Intent-NextStep: ",
			want:    ErrCurationActionCommandInvalid,
		},
		{
			name:    "leading whitespace",
			command: " @Intent-NextStep: body",
			want:    ErrCurationActionCommandInvalid,
		},
		{
			name:    "trailing whitespace",
			command: "@Intent-NextStep: body ",
			want:    ErrCurationActionCommandInvalid,
		},
		{
			name:    "embedded nul",
			command: "@Intent-NextStep: body\x00tail",
			want:    ErrCurationActionCommandInvalid,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCurationActionCommand(test.command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestValidateCurationActionRequestChecksPhaseSubjectBodyAndVersion(
	t *testing.T,
) {
	t.Parallel()
	curationID := CurationID("curation-1")
	targetID := "target-1"
	curationSubjectID := string(curationID)

	tests := []struct {
		name    string
		context CurationActionValidationContext
		request CurationActionRequest
		want    error
	}{
		{
			name: "valid planning add",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningAddTargets,
				SubjectType: CurationActionSubjectTargetList,
				SubjectID:   &curationSubjectID,
				Body:        "조명을 추가해줘", ExpectedCurationVersion: 3,
			},
		},
		{
			name: "valid start curating",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningStartCurating,
				SubjectType: CurationActionSubjectCuration,
				SubjectID:   &curationSubjectID, ExpectedCurationVersion: 3,
			},
		},
		{
			name: "valid target patch",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhaseCurating,
				CurrentVersion: 5,
			},
			request: CurationActionRequest{
				Type:        CurationActionTargetRemove,
				SubjectType: CurationActionSubjectTarget,
				SubjectID:   &targetID, ExpectedCurationVersion: 5,
			},
		},
		{
			name: "valid having intent records created curation v1",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhaseHavingIntent,
				CurrentVersion: 1,
			},
			request: CurationActionRequest{
				Type:        CurationActionIntentNextStep,
				SubjectType: CurationActionSubjectIntent,
				Body:        "작업용 의자를 찾아줘", ExpectedCurationVersion: 1,
			},
		},
		{
			name: "unknown type",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type: "UNKNOWN", SubjectType: CurationActionSubjectTargetList,
				Body: "body", ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionTypeInvalid,
		},
		{
			name: "action unavailable in phase",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionTargetResearchAgain,
				SubjectType: CurationActionSubjectTarget,
				SubjectID:   &targetID, Body: "again",
				ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionUnavailable,
		},
		{
			name: "subject type mismatch",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhaseCurating,
				CurrentVersion: 5,
			},
			request: CurationActionRequest{
				Type:        CurationActionTargetResearchAgain,
				SubjectType: CurationActionSubjectCuration,
				SubjectID:   &targetID, Body: "again",
				ExpectedCurationVersion: 5,
			},
			want: ErrCurationActionSubjectInvalid,
		},
		{
			name: "required subject id missing",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhaseCurating,
				CurrentVersion: 5,
			},
			request: CurationActionRequest{
				Type:        CurationActionTargetResearchAgain,
				SubjectType: CurationActionSubjectTarget,
				Body:        "again", ExpectedCurationVersion: 5,
			},
			want: ErrCurationActionSubjectInvalid,
		},
		{
			name: "target list subject must match envelope curation",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningAddTargets,
				SubjectType: CurationActionSubjectTargetList,
				SubjectID:   &targetID, Body: "body",
				ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionSubjectInvalid,
		},
		{
			name: "curation subject must match envelope curation",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningStartCurating,
				SubjectType: CurationActionSubjectCuration,
				SubjectID:   &targetID, ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionSubjectInvalid,
		},
		{
			name: "required body missing",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:                    CurationActionPlanningAddTargets,
				SubjectType:             CurationActionSubjectTargetList,
				SubjectID:               &curationSubjectID,
				ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionBodyInvalid,
		},
		{
			name: "body forbidden",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningStartCurating,
				SubjectType: CurationActionSubjectCuration,
				SubjectID:   &curationSubjectID, Body: "unexpected",
				ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionBodyInvalid,
		},
		{
			name: "body must be canonical",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 3,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningAddTargets,
				SubjectType: CurationActionSubjectTargetList,
				SubjectID:   &curationSubjectID,
				Body:        " body ", ExpectedCurationVersion: 3,
			},
			want: ErrCurationActionBodyInvalid,
		},
		{
			name: "stale version",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 4,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningAddTargets,
				SubjectType: CurationActionSubjectTargetList,
				Body:        "body", ExpectedCurationVersion: 3,
			},
			want: ErrVersionConflict,
		},
		{
			name: "invalid persisted expected version",
			context: CurationActionValidationContext{
				CurationID: curationID, Phase: CurationActionPhasePlanning,
				CurrentVersion: 4,
			},
			request: CurationActionRequest{
				Type:        CurationActionPlanningAddTargets,
				SubjectType: CurationActionSubjectTargetList,
				Body:        "body", ExpectedCurationVersion: 0,
			},
			want: ErrCurationActionVersionInvalid,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			descriptor, err := ValidateCurationActionRequest(
				test.context,
				test.request,
			)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("error=%v want=%v", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if descriptor.ID != test.request.Type ||
				descriptor.ExpectedResourceVersion !=
					test.context.CurrentVersion {
				t.Fatalf("unexpected descriptor: %#v", descriptor)
			}
		})
	}
}

func TestNewCurationActionBuildsImmutableAppendEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 3, 4, 5, 0, time.UTC)
	targetID := "target-1"
	sum := sha256.Sum256([]byte("canonical request"))
	hash := append([]byte(nil), sum[:]...)
	input := NewCurationActionInput{
		ID: "action-1", CurationID: "curation-1", ActorUserID: "user-1",
		PhaseAtRequest:         CurationActionPhaseCurating,
		CurrentCurationVersion: 7,
		Request: CurationActionRequest{
			Type:        CurationActionTargetResearchAgain,
			SubjectType: CurationActionSubjectTarget,
			SubjectID:   &targetID, Body: "더 가벼운 제품으로",
			ExpectedCurationVersion: 7,
		},
		SourceRefType: "RESEARCH_FEEDBACK", SourceRefID: "feedback-1",
		RequestHash: hash, CreatedAt: now,
	}

	action, err := NewCurationAction(input)
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != CurationActionTargetResearchAgain ||
		action.EffectKind != CurationActionEffectIntelligence ||
		action.RequestedTransitionTo != nil ||
		action.SubjectID == nil ||
		*action.SubjectID != "target-1" ||
		action.ExpectedCurationVersion != 7 ||
		!action.CreatedAt.Equal(now) {
		t.Fatalf("unexpected action envelope: %#v", action)
	}

	targetID = "mutated-target"
	hash[0] ^= 0xff
	if *action.SubjectID != "target-1" || action.RequestHash != sum {
		t.Fatalf("constructor retained mutable input: %#v", action)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("valid envelope failed rehydration validation: %v", err)
	}
}

func TestNewCurationActionDerivesTransitionsFromClosedCatalog(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 3, 4, 5, 0, time.UTC)
	sum := sha256.Sum256([]byte("next step"))
	next, err := NewCurationAction(NewCurationActionInput{
		ID: "action-next", CurationID: "curation-1", ActorUserID: "user-1",
		PhaseAtRequest:         CurationActionPhaseHavingIntent,
		CurrentCurationVersion: 1,
		Request: CurationActionRequest{
			Type:        CurationActionIntentNextStep,
			SubjectType: CurationActionSubjectIntent,
			Body:        "작업용 의자를 찾아줘", ExpectedCurationVersion: 1,
		},
		SourceRefType: "SHOPPING_PLAN", SourceRefID: "plan-1",
		RequestHash: sum[:], CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.RequestedTransitionTo == nil ||
		*next.RequestedTransitionTo != CurationPhasePlanning ||
		next.ExpectedCurationVersion != 1 {
		t.Fatalf("unexpected NextStep transition: %#v", next)
	}

	curationSubjectID := "curation-1"
	sum = sha256.Sum256([]byte("start curating"))
	start, err := NewCurationAction(NewCurationActionInput{
		ID: "action-start", CurationID: "curation-1",
		ActorUserID:            "user-1",
		PhaseAtRequest:         CurationActionPhasePlanning,
		CurrentCurationVersion: 2,
		Request: CurationActionRequest{
			Type:        CurationActionPlanningStartCurating,
			SubjectType: CurationActionSubjectCuration,
			SubjectID:   &curationSubjectID, ExpectedCurationVersion: 2,
		},
		SourceRefType: "CURATION_RUN", SourceRefID: "run-1",
		RequestHash: sum[:], CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if start.RequestedTransitionTo == nil ||
		*start.RequestedTransitionTo != CurationPhaseCurating {
		t.Fatalf("unexpected StartCurating transition: %#v", start)
	}
}

func TestNewCurationActionDoesNotPersistPatchOnlyActions(t *testing.T) {
	t.Parallel()
	targetID := "target-1"
	sum := sha256.Sum256([]byte("remove"))
	_, err := NewCurationAction(NewCurationActionInput{
		ID: "action-remove", CurationID: "curation-1",
		ActorUserID:            "user-1",
		PhaseAtRequest:         CurationActionPhasePlanning,
		CurrentCurationVersion: 2,
		Request: CurationActionRequest{
			Type:        CurationActionTargetRemove,
			SubjectType: CurationActionSubjectTarget,
			SubjectID:   &targetID, ExpectedCurationVersion: 2,
		},
		SourceRefType: "CURATION_RUN", SourceRefID: "run-1",
		RequestHash: sum[:], CreatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, ErrCurationActionNotAppendable) {
		t.Fatalf("error=%v want=%v", err, ErrCurationActionNotAppendable)
	}
}

func TestNewOwnedPatchCurationActionBuildsAuditWithoutTranscript(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("selection mutation"))
	selectionID := "selection-1"
	action, err := NewOwnedPatchCurationAction(NewCurationActionInput{
		ID: "action-selection", CurationID: "curation-1",
		ActorUserID:            "user-1",
		PhaseAtRequest:         CurationActionPhaseCurating,
		CurrentCurationVersion: 3,
		Request: CurationActionRequest{
			Type:                    CurationActionSelectionMutation,
			SubjectType:             CurationActionSubjectSelection,
			SubjectID:               &selectionID,
			Body:                    `{"action":"UPDATE_QUANTITY"}`,
			ExpectedCurationVersion: 3,
		},
		SourceRefType: CurationActionSourceSelectionCommand,
		SourceRefID:   "action-selection",
		RequestHash:   sum[:],
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != CurationActionSelectionMutation ||
		CurationActionAppendsTranscript(action.Type) {
		t.Fatalf("unexpected owned patch action=%#v", action)
	}

	action.SourceRefID = "different-command"
	if err := action.Validate(); !errors.Is(err, ErrCurationActionInvalid) {
		t.Fatalf("tampered source error=%v", err)
	}
}

func TestCurationActionValidateRejectsTamperedEnvelope(t *testing.T) {
	t.Parallel()
	sum := sha256.Sum256([]byte("canonical request"))
	curationID := "curation-1"
	base, err := NewCurationAction(NewCurationActionInput{
		ID: "action-1", CurationID: "curation-1", ActorUserID: "user-1",
		PhaseAtRequest:         CurationActionPhasePlanning,
		CurrentCurationVersion: 3,
		Request: CurationActionRequest{
			Type:        CurationActionPlanningAddTargets,
			SubjectType: CurationActionSubjectTargetList,
			SubjectID:   &curationID,
			Body:        "조명을 추가해줘", ExpectedCurationVersion: 3,
		},
		SourceRefType: "CURATION_RUN", SourceRefID: "run-1",
		RequestHash: sum[:], CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	curating := CurationPhaseCurating
	tests := []struct {
		name   string
		mutate func(*CurationAction)
		want   error
	}{
		{
			name: "unknown type",
			mutate: func(action *CurationAction) {
				action.Type = "UNKNOWN"
			},
			want: ErrCurationActionTypeInvalid,
		},
		{
			name: "effect mismatch",
			mutate: func(action *CurationAction) {
				action.EffectKind = CurationActionEffectNone
			},
			want: ErrCurationActionInvalid,
		},
		{
			name: "invented transition",
			mutate: func(action *CurationAction) {
				action.RequestedTransitionTo = &curating
			},
			want: ErrCurationActionInvalid,
		},
		{
			name: "bodyless subject gains id",
			mutate: func(action *CurationAction) {
				subjectID := "target-1"
				action.SubjectID = &subjectID
			},
			want: ErrCurationActionSubjectInvalid,
		},
		{
			name: "zero hash",
			mutate: func(action *CurationAction) {
				action.RequestHash = CurationActionRequestHash{}
			},
			want: ErrCurationActionInvalid,
		},
		{
			name: "invalid reference type",
			mutate: func(action *CurationAction) {
				action.SourceRefType = "curation-run"
			},
			want: ErrCurationActionInvalid,
		},
		{
			name: "phase mismatch",
			mutate: func(action *CurationAction) {
				action.PhaseAtRequest = CurationActionPhaseCurating
			},
			want: ErrCurationActionUnavailable,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			action := base
			test.mutate(&action)
			if err := action.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v action=%#v", err, test.want, action)
			}
		})
	}
}

func findCurationActionDescriptor(
	t *testing.T,
	catalog []CurationActionDescriptor,
	actionType CurationActionType,
) CurationActionDescriptor {
	t.Helper()
	for _, descriptor := range catalog {
		if descriptor.ID == actionType {
			return descriptor
		}
	}
	t.Fatalf("action %s not found in %#v", actionType, catalog)
	return CurationActionDescriptor{}
}

func TestResearchAgainActionAllowsNoFeedbackButStillValidatesPresentBody(t *testing.T) {
	target := "target-1"
	context := CurationActionValidationContext{CurationID: "curation-1", Phase: CurationActionPhaseCurating, CurrentVersion: 3}
	request := CurationActionRequest{Type: CurationActionTargetResearchAgain, SubjectType: CurationActionSubjectTarget, SubjectID: &target, ExpectedCurationVersion: 3}
	if _, err := ValidateCurationActionRequest(context, request); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCurationActionCommand("@Target:target-1-ResearchAgain"); err != nil {
		t.Fatal(err)
	}
	request.Body = " bad "
	if _, err := ValidateCurationActionRequest(context, request); err == nil {
		t.Fatal("noncanonical present feedback accepted")
	}
}
