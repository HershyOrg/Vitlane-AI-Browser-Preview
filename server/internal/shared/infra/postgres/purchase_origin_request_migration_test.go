package postgres

import (
	"strings"
	"testing"
)

func TestPurchaseOriginRequestExpandMigrationContract(t *testing.T) {
	t.Parallel()

	directory := migrationTestDirectory(t)
	up := readMigrationContract(
		t, directory, "000035_purchase_origin_request_expand.up.sql",
	)
	down := readMigrationContract(
		t, directory, "000035_purchase_origin_request_expand.down.sql",
	)

	for _, fragment := range []string{
		"DROP INDEX IF EXISTS idx_purchases_active_candidate_quantity_configuration",
		"DROP INDEX IF EXISTS idx_purchases_one_active_per_session",
		"UNIQUE (id, actor_user_id, curation_id)",
		"CREATE TABLE purchase_origin_requests",
		"curation_action_id UUID NOT NULL",
		"UNIQUE (curation_action_id)",
		"FOREIGN KEY (curation_action_id, user_id, curation_id)",
		"REFERENCES curation_actions(id, actor_user_id, curation_id)",
		"curation_action_id=id",
		"request_hash ~ '^sha256:[0-9a-f]{64}$'",
		"origin IN ('DIRECT', 'CART')",
		"REFERENCES curations(user_id, id) ON DELETE RESTRICT",
		"REFERENCES purchase_origin_requests(\n" +
			"            user_id, id, root_origin_request_id, curation_id, origin\n" +
			"        )",
		"CREATE TABLE purchase_origin_request_items",
		"UNIQUE (purchase_id)",
		"REFERENCES plan_targets(user_id, id, curation_id)",
		"REFERENCES shopping_sessions(user_id, id, plan_target_id)",
		"REFERENCES candidates(id, shopping_session_id)",
		") REFERENCES candidate_configurations(",
		") REFERENCES curation_selections(",
		"source_selection_version=expected_selection_version",
		"CONSTRAINT purchase_origin_request_items_outcome_terminal_shape_check CHECK",
		"NEW.curation_action_id IS DISTINCT FROM OLD.curation_action_id",
		"CREATE TRIGGER purchase_origin_request_immutable",
		"CREATE TRIGGER purchase_origin_request_item_immutable",
	} {
		if !strings.Contains(up, fragment) {
			t.Errorf("up migration is missing contract fragment %q", fragment)
		}
	}

	for _, forbidden := range []string{
		"GENERIC_WEB_USD",
		"currency='USD'",
		"currency = 'USD'",
		"ON CONFLICT",
		"DROP TABLE IF EXISTS shopping_carts",
		"DROP TABLE IF EXISTS shopping_cart_items",
		"CONSTRAINT purchase_origin_request_items_outcome_check CHECK",
	} {
		if strings.Contains(up, forbidden) {
			t.Errorf("up migration contains forbidden coupling %q", forbidden)
		}
	}

	for _, fragment := range []string{
		"rollback requires explicit preservation or removal of Purchase origin provenance",
		"000034 rollback requires manual resolution",
		"DROP TABLE IF EXISTS purchase_origin_request_items",
		"DROP TABLE IF EXISTS purchase_origin_requests",
		"DROP CONSTRAINT IF EXISTS curation_actions_id_actor_curation_unique",
		"CREATE UNIQUE INDEX idx_purchases_active_candidate_quantity_configuration",
		"CREATE UNIQUE INDEX idx_purchases_one_active_per_session",
	} {
		if !strings.Contains(down, fragment) {
			t.Errorf("down migration is missing rollback fragment %q", fragment)
		}
	}
}
