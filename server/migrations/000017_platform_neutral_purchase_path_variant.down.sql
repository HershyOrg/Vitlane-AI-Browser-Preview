DROP TABLE IF EXISTS purchase_intent_snapshots;
DROP TABLE IF EXISTS candidate_configurations;

UPDATE shopping_sessions AS session
SET status = 'SELECTED',
    selected_candidate_id = reversal.candidate_id,
    version = version + 1
FROM candidate_decisions AS reversal
WHERE reversal.shopping_session_id = session.id
  AND reversal.command_hash LIKE
      'vitlane.migration.000017.variant-reconfiguration:%'
  AND session.status = 'REVIEWING'
  AND session.selected_candidate_id IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM candidate_decisions AS later
      WHERE later.shopping_session_id = session.id
        AND later.event_sequence > reversal.event_sequence
  );

DELETE FROM candidate_decisions
WHERE command_hash LIKE 'vitlane.migration.000017.variant-reconfiguration:%';

DELETE FROM candidate_decisions
WHERE command_hash LIKE 'vitlane.migration.000017.legacy-select:%';

DROP INDEX IF EXISTS idx_purchases_one_active_per_session;
DROP INDEX IF EXISTS idx_purchases_active_candidate_quantity_configuration;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM purchases
        WHERE status <> 'SUPERSEDED'
        GROUP BY user_id, shopping_session_id, candidate_id, quantity
        HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION
            'cannot roll back platform-neutral variants while multiple active configurations exist';
    END IF;
END
$$;

CREATE UNIQUE INDEX idx_purchases_active_candidate_quantity
    ON purchases(user_id, shopping_session_id, candidate_id, quantity)
    WHERE status <> 'SUPERSEDED';

ALTER TABLE purchases
    DROP COLUMN IF EXISTS configuration_hash;

ALTER TABLE candidates
    DROP COLUMN IF EXISTS purchase_path,
    DROP COLUMN IF EXISTS variant_discovery;
