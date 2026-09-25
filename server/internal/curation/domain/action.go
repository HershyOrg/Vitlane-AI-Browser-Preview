package domain

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// CurationActionPhase includes the browser-only HAVING_INTENT request context.
// Persisted Curation.Phase deliberately remains limited to PLANNING | CURATING.
// The HAVING_INTENT action itself is stored only after its transaction creates
// Curation version 1.
type CurationActionPhase string

const (
	CurationActionPhaseHavingIntent CurationActionPhase = "HAVING_INTENT"
	CurationActionPhasePlanning     CurationActionPhase = "PLANNING"
	CurationActionPhaseCurating     CurationActionPhase = "CURATING"
)

// CurationActionType is a closed set. Natural-language bodies never select an
// action type; callers must resolve one of these exact types first.
type CurationActionType string

const (
	CurationActionAutoStart      CurationActionType = "AUTO_START"
	CurationActionBudgetChange   CurationActionType = "BUDGET_CHANGE"
	CurationActionCriteriaChange CurationActionType = "CRITERIA_CHANGE"
	CurationActionStartResearch  CurationActionType = "START_RESEARCH"
	// CurationActionResponse writes the Thread's natural-language reply: the
	// comment after a research, or the answer to a question (ADR-0086). It is
	// the last Action of a Thread and never changes product state.
	CurationActionResponse              CurationActionType = "RESPONSE"
	CurationActionIntentNextStep        CurationActionType = "INTENT_NEXT_STEP"
	CurationActionPlanningAddTargets    CurationActionType = "PLANNING_ADD_TARGETS"
	CurationActionPlanningStartCurating CurationActionType = "PLANNING_START_CURATING"
	CurationActionCurationAddTargets    CurationActionType = "CURATION_ADD_TARGETS"
	CurationActionTargetResearchAgain   CurationActionType = "TARGET_RESEARCH_AGAIN"
	CurationActionTargetRemove          CurationActionType = "TARGET_REMOVE"
	CurationActionSelectionMutation     CurationActionType = "SELECTION_MUTATION"
)

type CurationActionEffectKind string

const (
	CurationActionEffectNone         CurationActionEffectKind = "NONE"
	CurationActionEffectIntelligence CurationActionEffectKind = "INTELLIGENCE"
)

type CurationActionTranscriptPolicy string

const (
	CurationActionTranscriptAppend    CurationActionTranscriptPolicy = "APPEND"
	CurationActionTranscriptPatchOnly CurationActionTranscriptPolicy = "PATCH_ONLY"
)

type CurationActionSubjectType string

const (
	CurationActionSubjectIntent     CurationActionSubjectType = "INTENT"
	CurationActionSubjectTargetList CurationActionSubjectType = "TARGET_LIST"
	CurationActionSubjectCuration   CurationActionSubjectType = "CURATION"
	CurationActionSubjectTarget     CurationActionSubjectType = "TARGET"
	CurationActionSubjectSelection  CurationActionSubjectType = "SELECTION"
)

// CurationActionBodySchema identifies which owning product must validate a
// non-empty body. This domain validates presence/absence; the owner validates
// the typed payload represented by TYPED_COMMAND.
type CurationActionBodySchema string

const (
	CurationActionBodyNone         CurationActionBodySchema = "NONE"
	CurationActionBodyTextRequired CurationActionBodySchema = "TEXT_REQUIRED"
	CurationActionBodyTextOptional CurationActionBodySchema = "TEXT_OPTIONAL"
	CurationActionBodyTypedCommand CurationActionBodySchema = "TYPED_COMMAND"
)

type CurationActionID = string
type CurationActionSourceRefType string
type CurationActionRequestHash [sha256.Size]byte

const (
	CurationActionSourceShoppingPlan         CurationActionSourceRefType = "SHOPPING_PLAN"
	CurationActionSourceCurationRunRequest   CurationActionSourceRefType = "CURATION_RUN_REQUEST"
	CurationActionSourceResearchStartRequest CurationActionSourceRefType = "RESEARCH_START_REQUEST"
	CurationActionSourceResearchAgainRequest CurationActionSourceRefType = "RESEARCH_AGAIN_REQUEST"
	CurationActionSourcePlanTarget           CurationActionSourceRefType = "PLAN_TARGET"
	CurationActionSourceSelectionCommand     CurationActionSourceRefType = "CURATION_SELECTION_COMMAND"
)

type CurationActionSubjectSchema struct {
	Type       CurationActionSubjectType `json:"type"`
	IDRequired bool                      `json:"idRequired"`
}

// CurationActionDescriptor is the phase-specific server catalog contract. A
// caller may further disable an entry for artifact-local preconditions, but it
// must never add an action that this catalog does not return for the phase.
type CurationActionDescriptor struct {
	ID                      CurationActionType             `json:"id"`
	Alias                   string                         `json:"alias"`
	Enabled                 bool                           `json:"enabled"`
	UnavailableReason       string                         `json:"unavailableReason,omitempty"`
	SubjectSchema           CurationActionSubjectSchema    `json:"subjectSchema"`
	BodySchema              CurationActionBodySchema       `json:"bodySchema"`
	EffectKind              CurationActionEffectKind       `json:"effectKind"`
	TranscriptPolicy        CurationActionTranscriptPolicy `json:"transcriptPolicy"`
	ExpectedResourceVersion int64                          `json:"expectedResourceVersion"`
	RequiresConfirmation    bool                           `json:"requiresConfirmation"`
	RequestedTransitionTo   *CurationPhase                 `json:"requestedTransitionTo,omitempty"`
}

type ParsedCurationActionCommand struct {
	Type        CurationActionType        `json:"type"`
	Alias       string                    `json:"alias"`
	SubjectType CurationActionSubjectType `json:"subjectType"`
	SubjectID   *string                   `json:"subjectId,omitempty"`
	Body        string                    `json:"body,omitempty"`
}

type CurationActionRequest struct {
	Type                    CurationActionType
	SubjectType             CurationActionSubjectType
	SubjectID               *string
	Body                    string
	ExpectedCurationVersion int64
}

type CurationActionValidationContext struct {
	CurationID     CurationID
	Phase          CurationActionPhase
	CurrentVersion int64
}

// CurationAction owns a frozen command, execution state, and committed receipts.
// Thread orders Actions; Intelligence owns their asynchronous Jobs.
type CurationAction struct {
	ThreadID                string               `json:"threadId,omitempty"`
	Sequence                int                  `json:"sequence"`
	GeneratedByActionID     string               `json:"generatedByActionId,omitempty"`
	DecisionIDs             []string             `json:"decisionIds"`
	RetryOfActionID         string               `json:"retryOfActionId,omitempty"`
	TargetID                string               `json:"targetId,omitempty"`
	TargetLabel             string               `json:"targetLabel,omitempty"`
	Instruction             string               `json:"instruction,omitempty"`
	Budget                  *BudgetCommand       `json:"budget,omitempty"`
	Criteria                *TargetCriteriaSetV1 `json:"criteria,omitempty"`
	Status                  string               `json:"status,omitempty"`
	Jobs                    []ActionJobResult    `json:"jobs"`
	Effects                 []ActionEffect       `json:"effects"`
	ReasonCode              string               `json:"reasonCode,omitempty"`
	Decisions               []ActionDecision     `json:"decisions"`
	Questions               []ThreadQuestion     `json:"questions,omitempty"`
	Question                *ThreadQuestion      `json:"question,omitempty"`
	Answers                 []ThreadAnswer       `json:"answers"`
	InputRevision           int64                `json:"inputRevision"`
	InterpretationStartedAt *time.Time           `json:"interpretationStartedAt,omitempty"`
	// Response is set only on a RESPONSE Action that produced a reply.
	Response *ActionResponse `json:"response,omitempty"`

	ID                      CurationActionID            `json:"id"`
	CurationID              CurationID                  `json:"curationId"`
	ActorUserID             UserID                      `json:"actorUserId"`
	Type                    CurationActionType          `json:"type"`
	PhaseAtRequest          CurationActionPhase         `json:"phaseAtRequest"`
	RequestedTransitionTo   *CurationPhase              `json:"requestedTransitionTo,omitempty"`
	SubjectType             CurationActionSubjectType   `json:"subjectType"`
	SubjectID               *string                     `json:"subjectId,omitempty"`
	EffectKind              CurationActionEffectKind    `json:"effectKind"`
	SourceRefType           CurationActionSourceRefType `json:"sourceRefType"`
	SourceRefID             string                      `json:"sourceRefId"`
	ExpectedCurationVersion int64                       `json:"expectedCurationVersion"`
	RequestHash             CurationActionRequestHash   `json:"-"`
	CreatedAt               time.Time                   `json:"createdAt"`
}

type NewCurationActionInput struct {
	ID                     CurationActionID
	CurationID             CurationID
	ActorUserID            UserID
	PhaseAtRequest         CurationActionPhase
	CurrentCurationVersion int64
	Request                CurationActionRequest
	SourceRefType          CurationActionSourceRefType
	SourceRefID            string
	RequestHash            []byte
	CreatedAt              time.Time
}

type curationActionDefinition struct {
	id                   CurationActionType
	alias                string
	phases               []CurationActionPhase
	subjectType          CurationActionSubjectType
	subjectIDRequired    bool
	bodySchema           CurationActionBodySchema
	effectKind           CurationActionEffectKind
	transcriptPolicy     CurationActionTranscriptPolicy
	requiresConfirmation bool
	transitionTo         CurationPhase
}

var internalActionDefinitions = []curationActionDefinition{
	{id: CurationActionAutoStart, alias: "@Auto-Start", phases: []CurationActionPhase{CurationActionPhasePlanning, CurationActionPhaseCurating}, subjectType: CurationActionSubjectCuration, subjectIDRequired: true, bodySchema: CurationActionBodyTextRequired, effectKind: CurationActionEffectIntelligence, transcriptPolicy: CurationActionTranscriptAppend},
	{id: CurationActionBudgetChange, alias: "@Budget-Change", phases: []CurationActionPhase{CurationActionPhasePlanning, CurationActionPhaseCurating}, subjectType: CurationActionSubjectCuration, subjectIDRequired: true, bodySchema: CurationActionBodyTypedCommand, effectKind: CurationActionEffectNone, transcriptPolicy: CurationActionTranscriptAppend},
	{id: CurationActionCriteriaChange, alias: "@Criteria-Change", phases: []CurationActionPhase{CurationActionPhasePlanning, CurationActionPhaseCurating}, subjectType: CurationActionSubjectTarget, subjectIDRequired: true, bodySchema: CurationActionBodyTypedCommand, effectKind: CurationActionEffectNone, transcriptPolicy: CurationActionTranscriptAppend},
	{id: CurationActionResponse, alias: "@Response-Write", phases: []CurationActionPhase{CurationActionPhasePlanning, CurationActionPhaseCurating}, subjectType: CurationActionSubjectCuration, subjectIDRequired: true, bodySchema: CurationActionBodyNone, effectKind: CurationActionEffectIntelligence, transcriptPolicy: CurationActionTranscriptAppend},
	{id: CurationActionStartResearch, alias: "@Research-Start", phases: []CurationActionPhase{CurationActionPhasePlanning, CurationActionPhaseCurating}, subjectType: CurationActionSubjectCuration, subjectIDRequired: true, bodySchema: CurationActionBodyNone, effectKind: CurationActionEffectIntelligence, transcriptPolicy: CurationActionTranscriptAppend},
}

var curationActionDefinitions = []curationActionDefinition{

	{
		id: CurationActionIntentNextStep, alias: "@Intent-NextStep",
		phases:           []CurationActionPhase{CurationActionPhaseHavingIntent},
		subjectType:      CurationActionSubjectIntent,
		bodySchema:       CurationActionBodyTextRequired,
		effectKind:       CurationActionEffectIntelligence,
		transcriptPolicy: CurationActionTranscriptAppend,
		transitionTo:     CurationPhasePlanning,
	},
	{
		id: CurationActionPlanningAddTargets, alias: "@TargetList-AddTarget",
		phases:      []CurationActionPhase{CurationActionPhasePlanning},
		subjectType: CurationActionSubjectTargetList, subjectIDRequired: true,
		bodySchema:       CurationActionBodyTextRequired,
		effectKind:       CurationActionEffectIntelligence,
		transcriptPolicy: CurationActionTranscriptAppend,
	},
	{
		id: CurationActionPlanningStartCurating, alias: "@Planning-NextStep",
		phases:      []CurationActionPhase{CurationActionPhasePlanning},
		subjectType: CurationActionSubjectCuration, subjectIDRequired: true,
		bodySchema:       CurationActionBodyNone,
		effectKind:       CurationActionEffectIntelligence,
		transcriptPolicy: CurationActionTranscriptAppend,
		transitionTo:     CurationPhaseCurating,
	},
	{
		id: CurationActionCurationAddTargets, alias: "@Curation-AddTarget",
		phases:      []CurationActionPhase{CurationActionPhaseCurating},
		subjectType: CurationActionSubjectCuration, subjectIDRequired: true,
		bodySchema:       CurationActionBodyTextRequired,
		effectKind:       CurationActionEffectIntelligence,
		transcriptPolicy: CurationActionTranscriptAppend,
	},
	{
		id: CurationActionTargetResearchAgain, alias: "@Target-ResearchAgain",
		phases:      []CurationActionPhase{CurationActionPhaseCurating},
		subjectType: CurationActionSubjectTarget, subjectIDRequired: true,
		bodySchema:       CurationActionBodyTextOptional,
		effectKind:       CurationActionEffectIntelligence,
		transcriptPolicy: CurationActionTranscriptAppend,
	},
	{
		id: CurationActionTargetRemove, alias: "@Target-Remove",
		phases: []CurationActionPhase{
			CurationActionPhasePlanning,
			CurationActionPhaseCurating,
		},
		subjectType: CurationActionSubjectTarget, subjectIDRequired: true,
		bodySchema:       CurationActionBodyNone,
		effectKind:       CurationActionEffectNone,
		transcriptPolicy: CurationActionTranscriptPatchOnly,
	},
	{
		id: CurationActionSelectionMutation, alias: "@Selection-Mutation",
		phases:      []CurationActionPhase{CurationActionPhaseCurating},
		subjectType: CurationActionSubjectSelection, subjectIDRequired: true,
		bodySchema:       CurationActionBodyTypedCommand,
		effectKind:       CurationActionEffectNone,
		transcriptPolicy: CurationActionTranscriptPatchOnly,
	},
}

// AvailableCurationActions returns the deterministic phase gate. Artifact-local
// rules such as "StartCurating needs an active Target" may disable an entry in
// the read model, while the command handler must still enforce that invariant.
func AvailableCurationActions(
	phase CurationActionPhase,
	currentVersion int64,
) ([]CurationActionDescriptor, error) {
	if err := validateActionVersionForPhase(phase, currentVersion); err != nil {
		return nil, err
	}
	result := make([]CurationActionDescriptor, 0, len(curationActionDefinitions))
	for i := range curationActionDefinitions {
		definition := &curationActionDefinitions[i]
		if definition.allows(phase) {
			result = append(result, definition.descriptor(currentVersion))
		}
	}
	return result, nil
}

// ParseCurationActionCommand recognizes only an exact, case-sensitive
// @Subject-Action prefix. The body may contain colons, but when present it must
// begin after exactly ": " and have no surrounding whitespace.
func ParseCurationActionCommand(command string) (ParsedCurationActionCommand, error) {
	if command == "" ||
		strings.TrimSpace(command) != command ||
		strings.ContainsRune(command, '\x00') ||
		!strings.HasPrefix(command, "@") {
		return ParsedCurationActionCommand{}, ErrCurationActionCommandInvalid
	}

	alias := command
	body := ""
	hasBody := false
	if colon := strings.Index(command, ": "); colon >= 0 {
		alias = command[:colon]
		body = command[colon+2:]
		if strings.TrimSpace(body) != body || body == "" {
			return ParsedCurationActionCommand{}, ErrCurationActionCommandInvalid
		}
		hasBody = true
	}
	var definition *curationActionDefinition
	var subjectID *string
	for index := range curationActionDefinitions {
		candidate := &curationActionDefinitions[index]
		parsedSubjectID, matches := parseCommandAlias(alias, candidate)
		if matches {
			definition = candidate
			subjectID = parsedSubjectID
			break
		}
	}
	if definition == nil {
		if malformedCurationCommandAlias(alias) {
			return ParsedCurationActionCommand{},
				ErrCurationActionCommandInvalid
		}
		return ParsedCurationActionCommand{}, fmt.Errorf(
			"%w: %s", ErrCurationActionAliasUnknown, alias,
		)
	}
	if err := validateCommandBody(definition.bodySchema, body, hasBody); err != nil {
		return ParsedCurationActionCommand{}, err
	}
	return ParsedCurationActionCommand{
		Type: definition.id, Alias: alias,
		SubjectType: definition.subjectType, SubjectID: subjectID, Body: body,
	}, nil
}

func malformedCurationCommandAlias(alias string) bool {
	if strings.Contains(alias, "--") {
		return true
	}
	for index := range curationActionDefinitions {
		definition := &curationActionDefinitions[index]
		if !definition.subjectIDRequired &&
			strings.HasPrefix(alias, definition.alias+":") {
			return true
		}
		separator := strings.IndexByte(definition.alias, '-')
		if definition.subjectIDRequired && separator > 1 {
			prefix := definition.alias[:separator] + ":"
			if strings.HasPrefix(alias, prefix) &&
				strings.Count(alias, ":") > 1 {
				return true
			}
		}
	}
	return false
}

// ValidateCurationActionRequest is the authoritative phase, optimistic
// version, subject, and body-presence gate shared by HTTP and other adapters.
func ValidateCurationActionRequest(
	context CurationActionValidationContext,
	request CurationActionRequest,
) (CurationActionDescriptor, error) {
	if !validActionIdentifier(string(context.CurationID)) {
		return CurationActionDescriptor{}, ErrCurationActionSubjectInvalid
	}
	if err := validateActionVersionForPhase(context.Phase, context.CurrentVersion); err != nil {
		return CurationActionDescriptor{}, err
	}
	if err := validateExpectedActionVersion(
		context.Phase,
		context.CurrentVersion,
		request.ExpectedCurationVersion,
	); err != nil {
		return CurationActionDescriptor{}, err
	}

	definition := curationActionDefinitionByType(request.Type)
	if definition == nil {
		return CurationActionDescriptor{}, fmt.Errorf(
			"%w: %q", ErrCurationActionTypeInvalid, request.Type,
		)
	}
	if !definition.allows(context.Phase) {
		return CurationActionDescriptor{}, fmt.Errorf(
			"%w: %s in %s",
			ErrCurationActionUnavailable,
			request.Type,
			context.Phase,
		)
	}
	if err := validateActionSubject(
		context.CurationID,
		definition,
		request.SubjectType,
		request.SubjectID,
	); err != nil {
		return CurationActionDescriptor{}, err
	}
	if err := validateActionBody(
		definition.bodySchema,
		request.Body,
		request.Body != "",
	); err != nil {
		return CurationActionDescriptor{}, err
	}
	return definition.descriptor(context.CurrentVersion), nil
}

func NewCurationAction(input NewCurationActionInput) (CurationAction, error) {
	return newCurationAction(input, true)
}

// NewOwnedPatchCurationAction is reserved for an owning product command that
// persists its state mutation in the same transaction. The generic record
// primitive remains APPEND-only, so a caller cannot create an orphan audit row
// through the public Curation action endpoint.
func NewOwnedPatchCurationAction(
	input NewCurationActionInput,
) (CurationAction, error) {
	switch input.Request.Type {
	case CurationActionSelectionMutation:
	default:
		return CurationAction{}, fmt.Errorf(
			"%w: %s",
			ErrCurationActionTypeInvalid,
			input.Request.Type,
		)
	}
	return newCurationAction(input, false)
}

// NewTargetRemoveCurationAction is reserved for Planning's owner command that
// atomically soft-removes the Target and its active Selections.
func NewTargetRemoveCurationAction(
	input NewCurationActionInput,
) (CurationAction, error) {
	if input.Request.Type != CurationActionTargetRemove {
		return CurationAction{}, fmt.Errorf(
			"%w: %s",
			ErrCurationActionTypeInvalid,
			input.Request.Type,
		)
	}
	return newCurationAction(input, false)
}

func newCurationAction(
	input NewCurationActionInput,
	appendOnly bool,
) (CurationAction, error) {
	if !validActionIdentifier(string(input.ID)) ||
		!validActionIdentifier(string(input.CurationID)) ||
		!validActionIdentifier(string(input.ActorUserID)) ||
		!validActionReferenceType(input.SourceRefType) ||
		!validActionIdentifier(input.SourceRefID) ||
		input.CreatedAt.IsZero() ||
		len(input.RequestHash) != sha256.Size {
		return CurationAction{}, ErrCurationActionInvalid
	}

	descriptor, err := ValidateCurationActionRequest(
		CurationActionValidationContext{
			CurationID: input.CurationID, Phase: input.PhaseAtRequest,
			CurrentVersion: input.CurrentCurationVersion,
		},
		input.Request,
	)
	if err != nil {
		return CurationAction{}, err
	}
	if appendOnly &&
		descriptor.TranscriptPolicy != CurationActionTranscriptAppend {
		return CurationAction{}, fmt.Errorf(
			"%w: %s", ErrCurationActionNotAppendable, input.Request.Type,
		)
	}

	var requestHash CurationActionRequestHash
	copy(requestHash[:], input.RequestHash)
	action := CurationAction{
		ID: input.ID, CurationID: input.CurationID, ActorUserID: input.ActorUserID,
		Type: input.Request.Type, PhaseAtRequest: input.PhaseAtRequest,
		RequestedTransitionTo: cloneCurationPhase(descriptor.RequestedTransitionTo),
		SubjectType:           input.Request.SubjectType,
		SubjectID:             cloneString(input.Request.SubjectID),
		EffectKind:            descriptor.EffectKind,
		SourceRefType:         input.SourceRefType, SourceRefID: input.SourceRefID,
		ExpectedCurationVersion: input.Request.ExpectedCurationVersion,
		RequestHash:             requestHash, CreatedAt: input.CreatedAt,
	}
	if err := action.Validate(); err != nil {
		return CurationAction{}, err
	}
	return action, nil
}

func (a CurationAction) Validate() error {
	if !validActionIdentifier(string(a.ID)) ||
		!validActionIdentifier(string(a.CurationID)) ||
		!validActionIdentifier(string(a.ActorUserID)) ||
		!validActionReferenceType(a.SourceRefType) ||
		!validActionIdentifier(a.SourceRefID) ||
		a.CreatedAt.IsZero() ||
		a.RequestHash == (CurationActionRequestHash{}) {
		return ErrCurationActionInvalid
	}
	if err := validateActionVersionForPhase(
		a.PhaseAtRequest,
		a.ExpectedCurationVersion,
	); err != nil {
		return err
	}
	definition := curationActionDefinitionByType(a.Type)
	if definition == nil {
		return fmt.Errorf("%w: %q", ErrCurationActionTypeInvalid, a.Type)
	}
	if !definition.allows(a.PhaseAtRequest) {
		return fmt.Errorf(
			"%w: %s in %s",
			ErrCurationActionUnavailable,
			a.Type,
			a.PhaseAtRequest,
		)
	}
	switch a.Type {
	case CurationActionTargetRemove:
		if a.SourceRefType != CurationActionSourcePlanTarget ||
			a.SubjectID == nil ||
			a.SourceRefID != *a.SubjectID {
			return ErrCurationActionInvalid
		}
	case CurationActionSelectionMutation:
		if a.SourceRefType != CurationActionSourceSelectionCommand ||
			a.SourceRefID != string(a.ID) {
			return ErrCurationActionInvalid
		}
	}
	if a.EffectKind != definition.effectKind {
		return ErrCurationActionInvalid
	}
	if err := validateActionSubject(
		a.CurationID,
		definition,
		a.SubjectType,
		a.SubjectID,
	); err != nil {
		return err
	}
	if !transitionMatches(a.RequestedTransitionTo, definition.transitionTo) {
		return ErrCurationActionInvalid
	}
	return nil
}

func (d *curationActionDefinition) descriptor(
	expectedVersion int64,
) CurationActionDescriptor {
	var transition *CurationPhase
	if d.transitionTo != "" {
		value := d.transitionTo
		transition = &value
	}
	return CurationActionDescriptor{
		ID: d.id, Alias: d.alias, Enabled: true,
		SubjectSchema: CurationActionSubjectSchema{
			Type: d.subjectType, IDRequired: d.subjectIDRequired,
		},
		BodySchema: d.bodySchema, EffectKind: d.effectKind,
		TranscriptPolicy:        d.transcriptPolicy,
		ExpectedResourceVersion: expectedVersion,
		RequiresConfirmation:    d.requiresConfirmation,
		RequestedTransitionTo:   transition,
	}
}

func (d *curationActionDefinition) allows(phase CurationActionPhase) bool {
	for _, candidate := range d.phases {
		if phase == candidate {
			return true
		}
	}
	return false
}

func curationActionDefinitionByType(
	actionType CurationActionType,
) *curationActionDefinition {
	for i := range internalActionDefinitions {
		if internalActionDefinitions[i].id == actionType {
			return &internalActionDefinitions[i]
		}
	}
	for i := range curationActionDefinitions {
		if curationActionDefinitions[i].id == actionType {
			return &curationActionDefinitions[i]
		}
	}
	return nil
}

// CurationActionAppendsTranscript centralizes the projection rule so every
// read model excludes owner PATCH_ONLY audit rows consistently.
func CurationActionAppendsTranscript(actionType CurationActionType) bool {
	definition := curationActionDefinitionByType(actionType)
	return definition != nil &&
		definition.transcriptPolicy == CurationActionTranscriptAppend
}

func curationActionDefinitionByAlias(alias string) *curationActionDefinition {
	for i := range curationActionDefinitions {
		if curationActionDefinitions[i].alias == alias {
			return &curationActionDefinitions[i]
		}
	}
	return nil
}

func validateActionSubject(
	curationID CurationID,
	definition *curationActionDefinition,
	subjectType CurationActionSubjectType,
	subjectID *string,
) error {
	if subjectType != definition.subjectType {
		return fmt.Errorf(
			"%w: %s requires %s",
			ErrCurationActionSubjectInvalid,
			definition.id,
			definition.subjectType,
		)
	}
	if definition.subjectIDRequired {
		if subjectID == nil || !validActionIdentifier(*subjectID) {
			return ErrCurationActionSubjectInvalid
		}
		if (subjectType == CurationActionSubjectCuration ||
			subjectType == CurationActionSubjectTargetList) &&
			*subjectID != string(curationID) {
			return ErrCurationActionSubjectInvalid
		}
		return nil
	}
	if subjectID != nil {
		return ErrCurationActionSubjectInvalid
	}
	return nil
}

func validateActionBody(
	schema CurationActionBodySchema,
	body string,
	hasBody bool,
) error {
	switch schema {
	case CurationActionBodyTextOptional:
		if body == "" && !hasBody {
			return nil
		}
		if !validPresentActionBody(body, hasBody) {
			return ErrCurationActionBodyInvalid
		}
	case CurationActionBodyNone:
		if hasBody || body != "" {
			return ErrCurationActionBodyInvalid
		}
	case CurationActionBodyTextRequired,
		CurationActionBodyTypedCommand:
		if !hasBody ||
			strings.TrimSpace(body) == "" ||
			strings.TrimSpace(body) != body ||
			strings.ContainsRune(body, '\x00') {
			return ErrCurationActionBodyInvalid
		}
	default:
		return ErrCurationActionBodyInvalid
	}
	return nil
}

// validateCommandBody validates the user-visible chat command.
func validateCommandBody(
	schema CurationActionBodySchema,
	body string,
	hasBody bool,
) error {
	switch schema {
	case CurationActionBodyTextOptional:
		if body == "" && !hasBody {
			return nil
		}
		if !validPresentActionBody(body, hasBody) {
			return ErrCurationActionBodyInvalid
		}
	case CurationActionBodyNone:
		if hasBody || body != "" {
			return ErrCurationActionBodyInvalid
		}
	case CurationActionBodyTextRequired,
		CurationActionBodyTypedCommand:
		if !validPresentActionBody(body, hasBody) {
			return ErrCurationActionBodyInvalid
		}
	default:
		return ErrCurationActionBodyInvalid
	}
	return nil
}

func validPresentActionBody(body string, hasBody bool) bool {
	return hasBody &&
		strings.TrimSpace(body) != "" &&
		strings.TrimSpace(body) == body &&
		!strings.ContainsRune(body, '\x00')
}

func validateActionVersionForPhase(
	phase CurationActionPhase,
	version int64,
) error {
	switch phase {
	case CurationActionPhaseHavingIntent:
		if version != 1 {
			return ErrCurationActionVersionInvalid
		}
	case CurationActionPhasePlanning, CurationActionPhaseCurating:
		if version < 1 {
			return ErrCurationActionVersionInvalid
		}
	default:
		return fmt.Errorf("%w: %q", ErrCurationActionPhaseInvalid, phase)
	}
	return nil
}

func validateExpectedActionVersion(
	phase CurationActionPhase,
	currentVersion, expectedVersion int64,
) error {
	if err := validateActionVersionForPhase(phase, expectedVersion); err != nil {
		return err
	}
	if currentVersion != expectedVersion {
		return fmt.Errorf(
			"%w: expected %d, current %d",
			ErrVersionConflict,
			expectedVersion,
			currentVersion,
		)
	}
	return nil
}

func transitionMatches(
	actual *CurationPhase,
	expected CurationPhase,
) bool {
	if expected == "" {
		return actual == nil
	}
	return actual != nil && *actual == expected
}

func cloneCurationPhase(value *CurationPhase) *CurationPhase {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func validActionIdentifier(value string) bool {
	return value != "" &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsRune(value, '\x00')
}

func validActionReferenceType(value CurationActionSourceRefType) bool {
	raw := string(value)
	if raw == "" {
		return false
	}
	for index, character := range raw {
		if (character >= 'A' && character <= 'Z') ||
			character == '_' ||
			(index > 0 && character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

func parseCommandAlias(
	alias string,
	definition *curationActionDefinition,
) (*string, bool) {
	if definition == nil || strings.ContainsAny(alias, " \t\r\n") {
		return nil, false
	}
	separator := strings.IndexByte(definition.alias, '-')
	if separator <= 1 || separator >= len(definition.alias)-1 {
		return nil, false
	}
	if !definition.subjectIDRequired {
		return nil, alias == definition.alias
	}
	prefix := definition.alias[:separator] + ":"
	suffix := definition.alias[separator:]
	if !strings.HasPrefix(alias, prefix) ||
		!strings.HasSuffix(alias, suffix) ||
		len(alias) <= len(prefix)+len(suffix) {
		return nil, false
	}
	value := alias[len(prefix) : len(alias)-len(suffix)]
	if !validActionIdentifier(value) ||
		strings.ContainsAny(value, ": \t\r\n") {
		return nil, false
	}
	return &value, true
}
