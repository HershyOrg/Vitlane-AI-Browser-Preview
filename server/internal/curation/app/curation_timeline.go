package app

import (
	"context"
	"errors"
	"strings"
	"time"

	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

var ErrCurationTimelineUnavailable = errors.New(
	"CURATION_TIMELINE_UNAVAILABLE",
)

const maxCurationTimelineListLimit = 100

// CurationTimelineRepository is an isolated cross-product read projection.
// The owning product rows remain authoritative; this port must return only
// user-display-safe text and compact summaries, never source payloads.
type CurationTimelineRepository interface {
	ListCurationTimeline(
		context.Context,
		string,
		string,
		int,
	) ([]CurationTimelineItem, error)
}

// CurationTimelineAction intentionally omits actor identity, source reference,
// request hash, and every owner-product payload. Those values are useful for
// audit APIs but are not part of the user-facing transcript.
type CurationTimelineAction struct {
	ID                      curationdomain.CurationActionID          `json:"id"`
	CurationID              curationdomain.CurationID                `json:"curationId"`
	Type                    curationdomain.CurationActionType        `json:"type"`
	PhaseAtRequest          curationdomain.CurationActionPhase       `json:"phaseAtRequest"`
	RequestedTransitionTo   *curationdomain.CurationPhase            `json:"requestedTransitionTo,omitempty"`
	SubjectType             curationdomain.CurationActionSubjectType `json:"subjectType"`
	SubjectID               *string                                  `json:"subjectId,omitempty"`
	EffectKind              curationdomain.CurationActionEffectKind  `json:"effectKind"`
	ExpectedCurationVersion int64                                    `json:"expectedCurationVersion"`
	CreatedAt               time.Time                                `json:"createdAt"`
}

type CurationTimelineResultKind string

const (
	CurationTimelineResultIntentAccepted   CurationTimelineResultKind = "INTENT_ACCEPTED"
	CurationTimelineResultTargetExpansion  CurationTimelineResultKind = "TARGET_EXPANSION"
	CurationTimelineResultResearchStarted  CurationTimelineResultKind = "RESEARCH_STARTED"
	CurationTimelineResultTargetResearched CurationTimelineResultKind = "TARGET_RESEARCHED"
)

type CurationTimelineDiff struct {
	Added   []string `json:"added,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

type CurationTimelineResult struct {
	Kind       CurationTimelineResultKind `json:"kind"`
	Summary    string                     `json:"summary"`
	OccurredAt time.Time                  `json:"occurredAt"`
	Diff       *CurationTimelineDiff      `json:"diff,omitempty"`
}

type CurationTimelineItem struct {
	Action      CurationTimelineAction  `json:"action"`
	DisplayBody string                  `json:"displayBody,omitempty"`
	Result      *CurationTimelineResult `json:"result,omitempty"`
}

func (s *Service) ListCurationTimeline(
	ctx context.Context,
	userID, curationID string,
	limit int,
) ([]CurationTimelineItem, error) {
	repository, ok := s.repository.(CurationTimelineRepository)
	if !ok {
		return nil, ErrCurationTimelineUnavailable
	}
	userID = strings.TrimSpace(userID)
	curationID = strings.TrimSpace(curationID)
	if userID == "" || curationID == "" {
		return nil, curationdomain.ErrCurationActionInvalid
	}
	if limit <= 0 || limit > maxCurationTimelineListLimit {
		limit = maxCurationTimelineListLimit
	}
	items, err := repository.ListCurationTimeline(
		ctx,
		userID,
		curationID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if err := validateCurationTimelineItem(
			items[index],
			curationID,
		); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func validateCurationTimelineItem(
	item CurationTimelineItem,
	curationID string,
) error {
	if string(item.Action.ID) == "" ||
		string(item.Action.CurationID) != curationID ||
		item.Action.CreatedAt.IsZero() ||
		item.Action.ExpectedCurationVersion < 1 {
		return curationdomain.ErrCurationActionInvalid
	}
	if !curationdomain.CurationActionAppendsTranscript(item.Action.Type) {
		return curationdomain.ErrCurationActionInvalid
	}
	switch item.Action.Type {
	case curationdomain.CurationActionIntentNextStep,
		curationdomain.CurationActionPlanningAddTargets,
		curationdomain.CurationActionCurationAddTargets,
		curationdomain.CurationActionTargetResearchAgain:
		// These owner rows contain the only user-authored text that the
		// transcript projection is allowed to expose.
	case curationdomain.CurationActionPlanningStartCurating:
		if item.DisplayBody != "" {
			return curationdomain.ErrCurationActionInvalid
		}
	default:
		return curationdomain.ErrCurationActionInvalid
	}
	if item.Result == nil {
		return nil
	}
	if item.Result.Kind == "" ||
		strings.TrimSpace(item.Result.Summary) == "" ||
		item.Result.OccurredAt.IsZero() {
		return curationdomain.ErrCurationActionInvalid
	}
	expectedKind := CurationTimelineResultKind("")
	switch item.Action.Type {
	case curationdomain.CurationActionIntentNextStep:
		expectedKind = CurationTimelineResultIntentAccepted
	case curationdomain.CurationActionPlanningAddTargets,
		curationdomain.CurationActionCurationAddTargets:
		expectedKind = CurationTimelineResultTargetExpansion
	case curationdomain.CurationActionPlanningStartCurating:
		expectedKind = CurationTimelineResultResearchStarted
	case curationdomain.CurationActionTargetResearchAgain:
		expectedKind = CurationTimelineResultTargetResearched
	}
	if item.Result.Kind != expectedKind {
		return curationdomain.ErrCurationActionInvalid
	}
	return nil
}
