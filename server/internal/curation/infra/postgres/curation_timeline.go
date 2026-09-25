package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
)

var _ curationapp.CurationTimelineRepository = (*Repository)(nil)

// curationTimelineProjectionQuery is deliberately a read-only, allowlisted
// composition. It selects only APPEND actions and their originalIntent,
// instruction, feedback, names, counts, and owner timestamps. PATCH_ONLY
// Target/Candidate/Selection mutations are deliberately absent from the chat
// transcript. It never selects Selection references, configuration
// identifiers/hashes, Agent credentials, or arbitrary JSON payloads.
const curationTimelineProjectionQuery = `
	WITH transcript_actions AS (
		SELECT
			id, curation_id, actor_user_id, action_type,
			phase_at_request, requested_transition_to,
			subject_type, subject_id, effect_kind,
			source_ref_type, source_ref_id,
			expected_curation_version, created_at
		FROM curation_actions
		WHERE actor_user_id=$1
		  AND curation_id=$2
		  AND action_type IN (
			'INTENT_NEXT_STEP',
			'PLANNING_ADD_TARGETS',
			'PLANNING_START_CURATING',
			'CURATION_ADD_TARGETS',
			'TARGET_RESEARCH_AGAIN'
		  )
		ORDER BY created_at, id
		LIMIT $3
	)
	SELECT
		action.id,
		action.curation_id,
		action.action_type,
		action.phase_at_request,
		action.requested_transition_to,
		action.subject_type,
		action.subject_id,
		action.effect_kind,
		action.expected_curation_version,
		action.created_at,
		CASE action.source_ref_type
			WHEN 'SHOPPING_PLAN' THEN intent.original_intent
			WHEN 'CURATION_RUN_REQUEST' THEN expansion.instruction
			WHEN 'RESEARCH_AGAIN_REQUEST' THEN research_again.feedback
			ELSE NULL
		END AS display_body,
		CASE
			WHEN intent.original_intent IS NOT NULL
				THEN 'INTENT_ACCEPTED'
			WHEN expansion.instruction IS NOT NULL
				THEN 'TARGET_EXPANSION'
			WHEN research_start.found
				THEN 'RESEARCH_STARTED'
			WHEN research_again.feedback IS NOT NULL
				THEN 'TARGET_RESEARCHED'
			ELSE NULL
		END AS result_kind,
		CASE
			WHEN intent.original_intent IS NOT NULL
				THEN 'Curation 계획을 시작했습니다.'
			WHEN expansion.instruction IS NOT NULL
				THEN CASE
					WHEN expansion.target_count > 0
						THEN format(
							'Target %s개를 추가했습니다.',
							expansion.target_count
						)
					WHEN expansion.status IN ('REQUESTED', 'MATERIALIZING')
						THEN 'Target 제안 작업을 시작했습니다.'
					WHEN expansion.status IN ('FAILED', 'CANCELLED')
						THEN 'Target 제안 작업이 결과 없이 종료됐습니다.'
					ELSE '추가된 Target이 없습니다.'
				END
			WHEN research_start.found
				THEN format(
					'Target %s개에 ResearchRound %s개를 시작했습니다.',
					research_start.target_count,
					research_start.round_count
				)
			WHEN research_again.feedback IS NOT NULL
				THEN CASE
					WHEN research_again.round_status='REQUESTED'
						THEN format(
							'%s Target 재조사를 시작했습니다.',
							research_again.target_title
						)
					ELSE format(
						'%s Target 재조사에서 후보 %s개를 반영했습니다.',
						research_again.target_title,
						research_again.candidate_count
					)
				END
			ELSE NULL
		END AS result_summary,
		CASE
			WHEN intent.original_intent IS NOT NULL
				THEN intent.created_at
			WHEN expansion.instruction IS NOT NULL
				THEN expansion.occurred_at
			WHEN research_start.found
				THEN research_start.occurred_at
			WHEN research_again.feedback IS NOT NULL
				THEN research_again.occurred_at
			ELSE NULL
		END AS result_occurred_at,
		CASE
			WHEN expansion.instruction IS NOT NULL
				THEN expansion.target_titles
			WHEN research_again.feedback IS NOT NULL
				THEN research_again.candidate_titles
			ELSE '[]'::jsonb
		END::text AS result_added,
		CASE
			WHEN research_start.found
				THEN research_start.target_titles
			WHEN research_again.feedback IS NOT NULL
				THEN jsonb_build_array(research_again.target_title)
			ELSE '[]'::jsonb
		END::text AS result_changed,
		'[]'::jsonb::text AS result_removed
	FROM transcript_actions AS action
	LEFT JOIN LATERAL (
		SELECT plan.original_intent, plan.created_at
		FROM shopping_plans AS plan
		WHERE action.source_ref_type='SHOPPING_PLAN'
		  AND plan.user_id=action.actor_user_id
		  AND plan.id::text=action.source_ref_id
	) AS intent ON TRUE
	LEFT JOIN LATERAL (
		SELECT
			run.instruction,
			run.status,
			COUNT(target.id)::bigint AS target_count,
			COALESCE(
				jsonb_agg(
					target.title
					ORDER BY target.order_index, target.id
				) FILTER (WHERE target.id IS NOT NULL),
				'[]'::jsonb
			) AS target_titles,
			COALESCE(
				run.completed_at,
				run.updated_at,
				run.created_at
			) AS occurred_at
		FROM curation_runs AS run
		LEFT JOIN plan_targets AS target
		  ON target.created_by_curation_run_id=run.id
		 AND target.user_id=run.user_id
		 AND target.curation_id=run.curation_id
		WHERE action.source_ref_type='CURATION_RUN_REQUEST'
		  AND run.user_id=action.actor_user_id
		  AND run.curation_id=action.curation_id
		  AND run.idempotency_key::text=action.source_ref_id
		GROUP BY
			run.id,
			run.instruction,
			run.status,
			run.completed_at,
			run.updated_at,
			run.created_at
	) AS expansion ON TRUE
	LEFT JOIN LATERAL (
		SELECT
			COUNT(DISTINCT round.id) > 0 AS found,
			COUNT(DISTINCT target.id)::bigint AS target_count,
			COUNT(DISTINCT round.id)::bigint AS round_count,
			COALESCE(
				jsonb_agg(
					DISTINCT target.title
					ORDER BY target.title
				) FILTER (WHERE target.id IS NOT NULL),
				'[]'::jsonb
			) AS target_titles,
			MAX(
				COALESCE(round.completed_at, round.created_at)
			) AS occurred_at
		FROM intelligence_jobs AS job
		JOIN research_rounds AS round
		  ON round.id=job.research_round_id
		 AND round.user_id=job.user_id
		JOIN shopping_sessions AS session
		  ON session.id=round.shopping_session_id
		 AND session.user_id=round.user_id
		JOIN plan_targets AS target
		  ON target.id=session.plan_target_id
		 AND target.user_id=session.user_id
		 AND target.curation_id=action.curation_id
		WHERE action.source_ref_type='RESEARCH_START_REQUEST'
		  AND job.user_id=action.actor_user_id
		  AND job.curation_action_id=action.id
	) AS research_start ON TRUE
	LEFT JOIN LATERAL (
		SELECT
			feedback.feedback,
			target.title AS target_title,
			round.status AS round_status,
			(
				SELECT COUNT(*)::bigint
				FROM candidates AS candidate
				WHERE candidate.research_submission_id=
					round.result_submission_id
			) AS candidate_count,
			(
				SELECT COALESCE(
					jsonb_agg(
						visible_candidate.name
						ORDER BY visible_candidate.order_index
					),
					'[]'::jsonb
				)
				FROM (
					SELECT candidate.name, candidate.order_index
					FROM candidates AS candidate
					WHERE candidate.research_submission_id=
						round.result_submission_id
					ORDER BY candidate.order_index
					LIMIT 5
				) AS visible_candidate
			) AS candidate_titles,
			COALESCE(
				round.completed_at,
				feedback.created_at
			) AS occurred_at
		FROM research_feedback AS feedback
		JOIN research_rounds AS round
		  ON round.id=feedback.next_round_id
		 AND round.user_id=feedback.user_id
		JOIN shopping_sessions AS session
		  ON session.id=feedback.shopping_session_id
		 AND session.user_id=feedback.user_id
		JOIN plan_targets AS target
		  ON target.id=session.plan_target_id
		 AND target.user_id=session.user_id
		 AND target.curation_id=action.curation_id
		WHERE action.source_ref_type='RESEARCH_AGAIN_REQUEST'
		  AND feedback.user_id=action.actor_user_id
		  AND feedback.client_request_id::text=action.source_ref_id
	) AS research_again ON TRUE
	ORDER BY action.created_at, action.id
`

func (r *Repository) ListCurationTimeline(
	ctx context.Context,
	userID, curationID string,
	limit int,
) ([]curationapp.CurationTimelineItem, error) {
	rows, err := r.database.Queryer(ctx).QueryContext(
		ctx,
		curationTimelineProjectionQuery,
		userID,
		curationID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list curation timeline: %w", err)
	}
	defer rows.Close()

	items := make([]curationapp.CurationTimelineItem, 0)
	for rows.Next() {
		item, scanErr := scanCurationTimelineItem(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate curation timeline: %w", err)
	}
	return items, nil
}

func scanCurationTimelineItem(
	row curationActionRowScanner,
) (curationapp.CurationTimelineItem, error) {
	var item curationapp.CurationTimelineItem
	var transition sql.NullString
	var subjectID sql.NullString
	var displayBody sql.NullString
	var resultKind sql.NullString
	var resultSummary sql.NullString
	var resultOccurredAt sql.NullTime
	var addedJSON string
	var changedJSON string
	var removedJSON string
	if err := row.Scan(
		&item.Action.ID,
		&item.Action.CurationID,
		&item.Action.Type,
		&item.Action.PhaseAtRequest,
		&transition,
		&item.Action.SubjectType,
		&subjectID,
		&item.Action.EffectKind,
		&item.Action.ExpectedCurationVersion,
		&item.Action.CreatedAt,
		&displayBody,
		&resultKind,
		&resultSummary,
		&resultOccurredAt,
		&addedJSON,
		&changedJSON,
		&removedJSON,
	); err != nil {
		return curationapp.CurationTimelineItem{},
			fmt.Errorf("scan curation timeline: %w", err)
	}
	if transition.Valid {
		value := curationdomain.CurationPhase(transition.String)
		item.Action.RequestedTransitionTo = &value
	}
	if subjectID.Valid {
		value := subjectID.String
		item.Action.SubjectID = &value
	}
	if displayBody.Valid {
		item.DisplayBody = displayBody.String
	}
	if !resultKind.Valid {
		return item, nil
	}
	if !resultSummary.Valid || !resultOccurredAt.Valid {
		return curationapp.CurationTimelineItem{},
			fmt.Errorf("scan curation timeline: incomplete result")
	}
	diff, err := decodeCurationTimelineDiff(
		addedJSON,
		changedJSON,
		removedJSON,
	)
	if err != nil {
		return curationapp.CurationTimelineItem{}, err
	}
	item.Result = &curationapp.CurationTimelineResult{
		Kind: curationapp.CurationTimelineResultKind(
			resultKind.String,
		),
		Summary:    resultSummary.String,
		OccurredAt: resultOccurredAt.Time,
		Diff:       diff,
	}
	return item, nil
}

func decodeCurationTimelineDiff(
	addedJSON, changedJSON, removedJSON string,
) (*curationapp.CurationTimelineDiff, error) {
	diff := curationapp.CurationTimelineDiff{}
	for _, value := range []struct {
		raw         string
		destination *[]string
	}{
		{raw: addedJSON, destination: &diff.Added},
		{raw: changedJSON, destination: &diff.Changed},
		{raw: removedJSON, destination: &diff.Removed},
	} {
		if err := json.Unmarshal(
			[]byte(value.raw),
			value.destination,
		); err != nil {
			return nil, fmt.Errorf(
				"decode curation timeline diff: %w",
				err,
			)
		}
	}
	if len(diff.Added) == 0 &&
		len(diff.Changed) == 0 &&
		len(diff.Removed) == 0 {
		return nil, nil
	}
	return &diff, nil
}
