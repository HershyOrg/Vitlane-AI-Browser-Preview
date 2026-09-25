package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	curationapp "github.com/vitlane/vitlane/server/internal/curation/app"
	curationdomain "github.com/vitlane/vitlane/server/internal/curation/domain"
	shareddomain "github.com/vitlane/vitlane/server/internal/shared/domain"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
)

var _ curationapp.TargetSelectionRemovalRepository = (*Repository)(nil)

func (r *Repository) GetCartView(
	ctx context.Context,
	userID, curationID string,
) (curationdomain.CartView, error) {
	var updatedAt time.Time
	var cartCurrency string
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT curation.updated_at, plan.budget_currency
		FROM curations AS curation
		JOIN shopping_plans AS plan
		  ON plan.id=curation.shopping_plan_id
		 AND plan.user_id=curation.user_id
		WHERE curation.id=$1
		  AND curation.user_id=$2
		  AND curation.phase='CURATING'
		  AND curation.archived_at IS NULL
	`, curationID, userID).Scan(&updatedAt, &cartCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.CartView{}, curationdomain.ErrCurationNotFound
	}
	if err != nil {
		return curationdomain.CartView{}, fmt.Errorf("get curation cart: %w", err)
	}

	rows, err := r.database.Queryer(ctx).QueryContext(ctx, `
		SELECT selection.id, selection.user_id, selection.curation_id,
		       selection.plan_target_id, selection.shopping_session_id,
		       selection.candidate_id, selection.candidate_configuration_id,
		       selection.candidate_configuration_hash, selection.quantity,
		       selection.version, selection.selected_at, selection.updated_at,
		       selection.removed_at,
		       candidate.name, candidate.product_url, candidate.image_url,
		       candidate.price_amount::text, candidate.price_currency
		FROM curation_selections AS selection
		JOIN plan_targets AS target
		  ON target.id=selection.plan_target_id
		 AND target.user_id=selection.user_id
		 AND target.curation_id=selection.curation_id
		JOIN candidates AS candidate
		  ON candidate.id=selection.candidate_id
		 AND candidate.shopping_session_id=selection.shopping_session_id
		WHERE selection.user_id=$1
		  AND selection.curation_id=$2
		  AND selection.removed_at IS NULL
		ORDER BY selection.selected_at, selection.id
	`, userID, curationID)
	if err != nil {
		return curationdomain.CartView{},
			fmt.Errorf("list curation selections: %w", err)
	}
	defer rows.Close()

	view := curationdomain.CartView{
		CurationID: curationID,
		Selections: []curationdomain.CartSelection{},
		Total: shareddomain.Money{
			Amount: "0", Currency: shareddomain.CurrencyCode(cartCurrency),
		},
		Warnings:  []curationdomain.CartWarning{},
		UpdatedAt: updatedAt,
	}
	totals := map[shareddomain.CurrencyCode]*big.Rat{}
	for rows.Next() {
		var item curationdomain.CartSelection
		var selection curationdomain.CurationSelection
		var removedAt sql.NullTime
		var amount, currency string
		if err := rows.Scan(
			&selection.ID, &selection.UserID, &selection.CurationID,
			&selection.PlanTargetID, &selection.ShoppingSessionID,
			&selection.CandidateID, &selection.CandidateConfigurationID,
			&selection.CandidateConfigurationHash, &selection.Quantity,
			&selection.Version, &selection.SelectedAt, &selection.UpdatedAt,
			&removedAt, &item.Candidate.Name, &item.Candidate.ProductURL,
			&item.Candidate.ImageURL, &amount, &currency,
		); err != nil {
			return curationdomain.CartView{},
				fmt.Errorf("scan curation selection: %w", err)
		}
		if removedAt.Valid {
			value := removedAt.Time
			selection.RemovedAt = &value
		}
		item.Selection = selection
		item.Candidate.ID = selection.CandidateID
		item.UnitPrice, err = shareddomain.NewMoney(amount, currency)
		if err != nil {
			return curationdomain.CartView{}, err
		}
		value, ok := new(big.Rat).SetString(item.UnitPrice.Amount)
		if !ok {
			return curationdomain.CartView{}, shareddomain.ErrInvalidMoney
		}
		value.Mul(value, new(big.Rat).SetInt64(selection.Quantity))
		if totals[item.UnitPrice.Currency] == nil {
			totals[item.UnitPrice.Currency] = new(big.Rat)
		}
		totals[item.UnitPrice.Currency].Add(
			totals[item.UnitPrice.Currency],
			value,
		)
		lineAmount := selectionDecimal(value)
		item.LineTotal, err = shareddomain.NewMoney(lineAmount, currency)
		if err != nil {
			return curationdomain.CartView{}, err
		}
		if selection.UpdatedAt.After(view.UpdatedAt) {
			view.UpdatedAt = selection.UpdatedAt
		}
		view.Selections = append(view.Selections, item)
	}
	if err := rows.Err(); err != nil {
		return curationdomain.CartView{}, err
	}

	currencies := make([]string, 0, len(totals))
	for currency := range totals {
		currencies = append(currencies, string(currency))
	}
	sort.Strings(currencies)
	for index, currency := range currencies {
		amount := selectionDecimal(
			totals[shareddomain.CurrencyCode(currency)],
		)
		if index == 0 {
			view.Total, err = shareddomain.NewMoney(amount, currency)
			if err != nil {
				return curationdomain.CartView{}, err
			}
			continue
		}
		view.Warnings = append(
			view.Warnings,
			curationdomain.CartWarning{
				Code:    "MULTIPLE_CURRENCIES",
				Message: "합계에는 첫 번째 통화만 표시됩니다.",
			},
		)
	}
	return view, nil
}

func (r *Repository) SoftRemoveActiveSelectionsForTarget(
	ctx context.Context,
	userID, curationID, targetID string,
	removedAt time.Time,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_selections
		SET version=version+1,
		    updated_at=$4,
		    removed_at=$4,
		    removed_by_user_id=$1
		WHERE user_id=$1
		  AND curation_id=$2
		  AND plan_target_id=$3
		  AND removed_at IS NULL
	`, userID, curationID, targetID, removedAt)
	if err != nil {
		return fmt.Errorf(
			"soft-remove target curation selections: %w",
			err,
		)
	}
	return nil
}

func selectionDecimal(value *big.Rat) string {
	amount := strings.TrimRight(
		strings.TrimRight(value.FloatString(18), "0"),
		".",
	)
	if amount == "" || amount == "-0" {
		return "0"
	}
	return amount
}

func (r *Repository) GetSelection(
	ctx context.Context,
	userID, curationID, selectionID string,
	forUpdate bool,
) (curationdomain.CurationSelection, error) {
	query := `
		SELECT selection.id, selection.user_id, selection.curation_id,
		       selection.plan_target_id, selection.shopping_session_id,
		       selection.candidate_id, selection.candidate_configuration_id,
		       selection.candidate_configuration_hash, selection.quantity,
		       selection.version, selection.selected_at, selection.updated_at,
		       selection.removed_at
		FROM curation_selections AS selection
		JOIN curations AS curation
		  ON curation.id=selection.curation_id
		 AND curation.user_id=selection.user_id
		 AND curation.phase='CURATING'
		 AND curation.archived_at IS NULL
		WHERE selection.id=$1
		  AND selection.user_id=$2
		  AND selection.curation_id=$3`
	if forUpdate {
		query += ` FOR UPDATE OF selection`
	}
	return scanSelection(
		r.database.Queryer(ctx).QueryRowContext(
			ctx, query, selectionID, userID, curationID,
		),
	)
}

func scanSelection(row sharedpostgres.Row) (curationdomain.CurationSelection, error) {
	var selection curationdomain.CurationSelection
	var removedAt sql.NullTime
	err := row.Scan(
		&selection.ID, &selection.UserID, &selection.CurationID,
		&selection.PlanTargetID, &selection.ShoppingSessionID,
		&selection.CandidateID, &selection.CandidateConfigurationID,
		&selection.CandidateConfigurationHash, &selection.Quantity,
		&selection.Version, &selection.SelectedAt, &selection.UpdatedAt,
		&removedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationdomain.CurationSelection{},
			curationdomain.ErrSelectionNotFound
	}
	if err != nil {
		return curationdomain.CurationSelection{},
			fmt.Errorf("scan curation selection: %w", err)
	}
	if removedAt.Valid {
		value := removedAt.Time
		selection.RemovedAt = &value
	}
	return selection, nil
}

func (r *Repository) FindSelectionCommand(
	ctx context.Context,
	userID, clientCommandID string,
) (curationapp.SelectionCommand, bool, error) {
	var command curationapp.SelectionCommand
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT id, user_id, curation_id, COALESCE(selection_id::text, ''),
		       client_command_id, command_kind, request_hash,
		       response_snapshot, created_at
		FROM curation_selection_commands
		WHERE user_id=$1 AND client_command_id=$2
	`, userID, clientCommandID).Scan(
		&command.ID, &command.UserID, &command.CurationID,
		&command.SelectionID, &command.ClientCommandID, &command.CommandKind,
		&command.RequestHash, &command.ResponseSnapshot, &command.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationapp.SelectionCommand{}, false, nil
	}
	if err != nil {
		return curationapp.SelectionCommand{}, false,
			fmt.Errorf("find selection command: %w", err)
	}
	return command, true, nil
}

func (r *Repository) ResolveSelectionConfiguration(
	ctx context.Context,
	userID, curationID, configurationID string,
) (curationapp.ResolvedSelectionConfiguration, error) {
	var resolved curationapp.ResolvedSelectionConfiguration
	err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT curation.id, target.id, session.id, candidate.id,
		       configuration.id, configuration.configuration_hash
		FROM candidate_configurations AS configuration
		JOIN candidates AS candidate
		  ON candidate.id=configuration.candidate_id
		 AND candidate.shopping_session_id=configuration.shopping_session_id
		JOIN shopping_sessions AS session
		  ON session.id=configuration.shopping_session_id
		 AND session.user_id=configuration.user_id
		JOIN plan_targets AS target
		  ON target.id=session.plan_target_id
		 AND target.user_id=configuration.user_id
		 AND target.curation_id=$2
		 AND target.removed_at IS NULL
		JOIN curations AS curation
		  ON curation.id=target.curation_id
		 AND curation.user_id=configuration.user_id
		 AND curation.phase='CURATING'
		 AND curation.archived_at IS NULL
		WHERE configuration.id=$3
		  AND configuration.user_id=$1
		FOR UPDATE OF curation, target, session
	`, userID, curationID, configurationID).Scan(
		&resolved.CurationID,
		&resolved.PlanTargetID,
		&resolved.SessionID,
		&resolved.CandidateID,
		&resolved.ConfigurationID,
		&resolved.ConfigurationHash,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return curationapp.ResolvedSelectionConfiguration{},
			curationdomain.ErrSelectionInvalid
	}
	if err != nil {
		return curationapp.ResolvedSelectionConfiguration{},
			fmt.Errorf("resolve selection configuration: %w", err)
	}
	return resolved, nil
}

func (r *Repository) CreateSelection(
	ctx context.Context,
	mutation curationapp.SelectionMutation,
) error {
	selection := mutation.Selection
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO curation_selections(
			id, user_id, curation_id, plan_target_id, shopping_session_id,
			candidate_id, candidate_configuration_id,
			candidate_configuration_hash, quantity, version,
			selected_at, updated_at
		)
		SELECT $1, $2, curation.id, target.id, session.id, candidate.id,
		       configuration.id, configuration.configuration_hash,
		       $9, $10, $11, $12
		FROM curations AS curation
		JOIN plan_targets AS target
		  ON target.id=$4
		 AND target.user_id=$2
		 AND target.curation_id=curation.id
		 AND target.removed_at IS NULL
		JOIN shopping_sessions AS session
		  ON session.id=$5
		 AND session.user_id=$2
		 AND session.plan_target_id=target.id
		JOIN candidates AS candidate
		  ON candidate.id=$6
		 AND candidate.shopping_session_id=session.id
		JOIN candidate_configurations AS configuration
		  ON configuration.id=$7
		 AND configuration.user_id=$2
		 AND configuration.shopping_session_id=session.id
		 AND configuration.candidate_id=candidate.id
		 AND configuration.configuration_hash=$8
		WHERE curation.id=$3
		  AND curation.user_id=$2
		  AND curation.phase='CURATING'
		  AND curation.archived_at IS NULL
	`, selection.ID, selection.UserID, selection.CurationID,
		selection.PlanTargetID, selection.ShoppingSessionID,
		selection.CandidateID, selection.CandidateConfigurationID,
		selection.CandidateConfigurationHash, selection.Quantity,
		selection.Version, selection.SelectedAt, selection.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert curation selection: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrSelectionInvalid
	}
	return r.insertSelectionCommand(ctx, mutation.Command)
}

func (r *Repository) UpdateSelection(
	ctx context.Context,
	mutation curationapp.SelectionMutation,
) error {
	selection := mutation.Selection
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_selections AS selection
		SET candidate_configuration_id=$1,
		    candidate_configuration_hash=$2,
		    quantity=$3,
		    version=$4,
		    updated_at=$5
		WHERE selection.id=$6
		  AND selection.user_id=$7
		  AND selection.curation_id=$8
		  AND selection.plan_target_id=$9
		  AND selection.shopping_session_id=$10
		  AND selection.candidate_id=$11
		  AND selection.version=$12
		  AND selection.removed_at IS NULL
		  AND EXISTS (
		      SELECT 1
		      FROM candidate_configurations AS configuration
		      WHERE configuration.id=$1
		        AND configuration.user_id=$7
		        AND configuration.shopping_session_id=$10
		        AND configuration.candidate_id=$11
		        AND configuration.configuration_hash=$2
		  )
	`, selection.CandidateConfigurationID,
		selection.CandidateConfigurationHash, selection.Quantity,
		selection.Version, selection.UpdatedAt, selection.ID, selection.UserID,
		selection.CurationID, selection.PlanTargetID,
		selection.ShoppingSessionID, selection.CandidateID,
		mutation.ExpectedVersion,
	)
	if err != nil {
		return fmt.Errorf("update curation selection: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrSelectionVersionConflict
	}
	return r.insertSelectionCommand(ctx, mutation.Command)
}

func (r *Repository) RemoveSelection(
	ctx context.Context,
	mutation curationapp.SelectionMutation,
) error {
	selection := mutation.Selection
	result, err := r.database.Queryer(ctx).ExecContext(ctx, `
		UPDATE curation_selections
		SET version=$1,
		    updated_at=$2,
		    removed_at=$2,
		    removed_by_user_id=$3
		WHERE id=$4
		  AND user_id=$3
		  AND curation_id=$5
		  AND version=$6
		  AND removed_at IS NULL
	`, selection.Version, selection.UpdatedAt, selection.UserID,
		selection.ID, selection.CurationID, mutation.ExpectedVersion,
	)
	if err != nil {
		return fmt.Errorf("remove curation selection: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return curationdomain.ErrSelectionVersionConflict
	}
	return r.insertSelectionCommand(ctx, mutation.Command)
}

func (r *Repository) insertSelectionCommand(
	ctx context.Context,
	command curationapp.SelectionCommand,
) error {
	_, err := r.database.Queryer(ctx).ExecContext(ctx, `
		INSERT INTO curation_selection_commands(
			id, user_id, curation_id, selection_id, client_command_id,
			command_kind, request_hash, response_snapshot, created_at
		) VALUES ($1,$2,$3,NULLIF($4, '')::uuid,$5,$6,$7,$8,$9)
	`, command.ID, command.UserID, command.CurationID, command.SelectionID,
		command.ClientCommandID, command.CommandKind, command.RequestHash,
		command.ResponseSnapshot, command.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert selection command: %w", err)
	}
	return nil
}

func (r *Repository) LockSelectionSnapshots(
	ctx context.Context,
	userID, curationID string,
	references []curationapp.SelectionReference,
) ([]curationapp.SelectionSnapshotResult, error) {
	var owned bool
	if err := r.database.Queryer(ctx).QueryRowContext(ctx, `
		SELECT TRUE
		FROM curations
		WHERE id=$1
		  AND user_id=$2
		  AND phase='CURATING'
		  AND archived_at IS NULL
		FOR UPDATE
	`, curationID, userID).Scan(&owned); errors.Is(err, sql.ErrNoRows) {
		return nil, curationdomain.ErrCurationNotFound
	} else if err != nil {
		return nil, fmt.Errorf("lock curation selections: %w", err)
	}

	ids := make([]string, 0, len(references))
	seen := map[string]struct{}{}
	for _, reference := range references {
		if _, found := seen[reference.SelectionID]; found {
			continue
		}
		seen[reference.SelectionID] = struct{}{}
		ids = append(ids, reference.SelectionID)
	}
	sort.Strings(ids)
	locked := map[string]curationdomain.CurationSelection{}
	for _, id := range ids {
		selection, err := r.GetSelection(
			ctx, userID, curationID, id, true,
		)
		if errors.Is(err, curationdomain.ErrSelectionNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		locked[id] = selection
	}

	results := make([]curationapp.SelectionSnapshotResult, len(references))
	for index, reference := range references {
		results[index].Reference = reference
		selection, found := locked[reference.SelectionID]
		switch {
		case !found:
			results[index].ReasonCode = "SELECTION_NOT_FOUND"
		case selection.RemovedAt != nil:
			results[index].ReasonCode = "SELECTION_REMOVED"
		case selection.Version != reference.ExpectedVersion:
			results[index].ReasonCode = "SELECTION_VERSION_MISMATCH"
			results[index].Retryable = true
		default:
			hash, err := selectionSnapshotHash(selection)
			if err != nil {
				return nil, err
			}
			results[index].Snapshot = &curationapp.SelectionSnapshot{
				Selection:    selection,
				SnapshotHash: hash,
			}
		}
	}
	return results, nil
}

func selectionSnapshotHash(
	selection curationdomain.CurationSelection,
) (string, error) {
	snapshot := struct {
		CandidateConfigurationHash string `json:"candidateConfigurationHash"`
		CandidateConfigurationID   string `json:"candidateConfigurationId"`
		CandidateID                string `json:"candidateId"`
		CurationID                 string `json:"curationId"`
		Quantity                   int64  `json:"quantity"`
		SelectionID                string `json:"selectionId"`
		ShoppingSessionID          string `json:"shoppingSessionId"`
		TargetID                   string `json:"targetId"`
		Version                    int64  `json:"version"`
	}{
		CandidateConfigurationHash: selection.CandidateConfigurationHash,
		CandidateConfigurationID:   selection.CandidateConfigurationID,
		CandidateID:                selection.CandidateID,
		CurationID:                 selection.CurationID,
		Quantity:                   selection.Quantity,
		SelectionID:                selection.ID,
		ShoppingSessionID:          selection.ShoppingSessionID,
		TargetID:                   selection.PlanTargetID,
		Version:                    selection.Version,
	}
	payload, err := json.Marshal(struct {
		Schema   string `json:"schema"`
		Snapshot any    `json:"snapshot"`
	}{
		Schema:   "CurationSelectionSnapshotHash.v1",
		Snapshot: snapshot,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
