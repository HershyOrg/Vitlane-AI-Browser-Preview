-- Forward-only authority cutover. Stop the old application/workers before this
-- transaction; do not run old and new execution paths concurrently afterwards.
-- Preserve existing identities and facts. No synthetic historical operations.
LOCK TABLE merchant_orders, procurement_effect_locks,
 payment_mo_funding_positions, payment_mo_compensations,
 payment_external_operations, order_process_commands IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM merchant_orders WHERE state IN ('PLACEMENT_PENDING','PLACEMENT_UNKNOWN'))
 OR EXISTS (SELECT 1 FROM procurement_effect_locks WHERE state NOT IN ('PLACED','FAILED'))
 OR EXISTS (SELECT 1 FROM payment_mo_funding_positions WHERE state IN
  ('ACTIVATION_PENDING','ACTIVATION_UNKNOWN','RELEASE_PENDING','RELEASE_UNKNOWN'))
 OR EXISTS (SELECT 1 FROM payment_mo_funding_positions f JOIN merchant_orders mo ON mo.allocation_id=f.allocation_id
  WHERE f.state='ACTIVE' AND mo.state NOT IN ('PLACED','FAILED','CANCELLED'))
 OR EXISTS (SELECT 1 FROM payment_mo_compensations WHERE state<>'SUCCEEDED')
 OR EXISTS (SELECT 1 FROM payment_external_operations WHERE state NOT IN ('SUCCEEDED','FAILED','CANCELLED'))
 OR EXISTS (SELECT 1 FROM order_process_commands WHERE type IN
  ('procurement.prepare_purchase.v1','payment.ensure_mo_authorization.v1',
   'payment.activate_mo_funding.v1','procurement.authorize_merchant_effect.v1',
   'procurement.cancel_pre_effect.v1','procurement.cancel_delay_rule.v1','payment.execute_mo_compensation.v1')
  AND state NOT IN ('SUCCEEDED','REJECTED','ABANDONED')) THEN
  RAISE EXCEPTION 'ORDER_WORKFLOW_CUTOVER_REQUIRES_RECONCILIATION' USING ERRCODE='55000';
 END IF;
END $$;

CREATE UNIQUE INDEX merchant_orders_order_identity ON merchant_orders(id, agency_order_id);
CREATE TABLE order_workflow_mo_controls (
 merchant_order_id uuid PRIMARY KEY,
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 active_operation_id uuid,
 generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
 purchase_closed boolean NOT NULL DEFAULT false,
 cancelled boolean NOT NULL DEFAULT false,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 updated_at timestamptz NOT NULL,
 UNIQUE (merchant_order_id, agency_order_id),
 FOREIGN KEY (merchant_order_id, agency_order_id)
  REFERENCES merchant_orders(id, agency_order_id) ON DELETE CASCADE
);
CREATE TABLE order_workflow_operations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 merchant_order_id uuid NOT NULL,
 kind text NOT NULL CHECK (kind IN ('PURCHASE','CANCEL','COMPENSATION')),
 status text NOT NULL CHECK (status IN ('RUNNING','WAITING','EFFECT_UNKNOWN','SUCCEEDED','REJECTED','ATTENTION_REQUIRED')),
 stage text NOT NULL CHECK (stage <> ''),
 reason_code text NOT NULL DEFAULT '',
 generation bigint NOT NULL CHECK (generation > 0),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 request_id text NOT NULL,
 request_hash text NOT NULL,
 input jsonb NOT NULL DEFAULT '{}',
 actor_id text NOT NULL DEFAULT '',
 task_id text NOT NULL DEFAULT '',
 due_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE (agency_order_id, request_id),
 UNIQUE (id, merchant_order_id, agency_order_id),
 FOREIGN KEY (merchant_order_id, agency_order_id)
  REFERENCES order_workflow_mo_controls(merchant_order_id, agency_order_id) ON DELETE CASCADE
);
ALTER TABLE order_workflow_mo_controls ADD CONSTRAINT workflow_active_operation_scope
 FOREIGN KEY (active_operation_id, merchant_order_id, agency_order_id)
 REFERENCES order_workflow_operations(id, merchant_order_id, agency_order_id)
 DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX workflow_operation_open ON order_workflow_operations(agency_order_id, merchant_order_id)
 WHERE status NOT IN ('SUCCEEDED','REJECTED');
CREATE INDEX workflow_operation_due ON order_workflow_operations(due_at) WHERE due_at IS NOT NULL;

-- Each HTTP retry key remains bound even when a fresh click joins an active operation.
CREATE UNIQUE INDEX workflow_operation_order_identity ON order_workflow_operations(id,agency_order_id);
CREATE TABLE order_workflow_requests (
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 request_id text NOT NULL,
 request_hash text NOT NULL,
 operation_id uuid NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(agency_order_id,request_id),
 FOREIGN KEY(operation_id,agency_order_id) REFERENCES order_workflow_operations(id,agency_order_id) ON DELETE CASCADE
);

ALTER TABLE order_process_commands ALTER COLUMN caused_by_event_id DROP NOT NULL;
ALTER TABLE order_process_commands
 ADD COLUMN operation_id uuid REFERENCES order_workflow_operations(id) ON DELETE CASCADE,
 ADD COLUMN step_key text NOT NULL DEFAULT '',
 ADD COLUMN generation bigint NOT NULL DEFAULT 0,
 ADD COLUMN claim_version bigint NOT NULL DEFAULT 0,
 ADD COLUMN lease_until timestamptz,
 ADD COLUMN prepared jsonb;
ALTER TABLE order_process_commands DROP CONSTRAINT order_process_commands_state_check;
ALTER TABLE order_process_commands ADD CONSTRAINT order_process_commands_state_check
 CHECK (state IN ('PENDING','WAITING','EFFECT_UNKNOWN','SUCCEEDED','REJECTED','EXHAUSTED','ABANDONED'));
CREATE UNIQUE INDEX workflow_operation_step ON order_process_commands(operation_id,step_key)
 WHERE operation_id IS NOT NULL;
ALTER TABLE order_process_events
 ADD COLUMN operation_id uuid REFERENCES order_workflow_operations(id) ON DELETE CASCADE,
 ADD COLUMN causation_command_id uuid REFERENCES order_process_commands(id) ON DELETE SET NULL,
 ADD COLUMN source_entity_version bigint;
ALTER TABLE procurement_effect_locks
 ADD COLUMN operation_id uuid REFERENCES order_workflow_operations(id);

-- Payment owns this immutable correlation after receiving the Procurement fact.
ALTER TABLE payment_mo_funding_positions ADD COLUMN merchant_order_id uuid UNIQUE;
ALTER TABLE payment_mo_funding_positions ADD CONSTRAINT funding_merchant_order_scope
 FOREIGN KEY (merchant_order_id,agency_order_id)
 REFERENCES merchant_orders(id,agency_order_id);

ALTER TABLE order_process_commands ADD CONSTRAINT workflow_command_order_scope
 FOREIGN KEY(operation_id,agency_order_id) REFERENCES order_workflow_operations(id,agency_order_id) ON DELETE CASCADE;
ALTER TABLE order_process_commands ADD COLUMN causation_command_id uuid REFERENCES order_process_commands(id);
CREATE FUNCTION keep_workflow_funding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.merchant_order_id IS NOT NULL AND NEW.merchant_order_id IS DISTINCT FROM OLD.merchant_order_id THEN
  RAISE EXCEPTION 'WORKFLOW_FUNDING_IDENTITY_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER workflow_funding_identity BEFORE UPDATE OF merchant_order_id ON payment_mo_funding_positions
 FOR EACH ROW EXECUTE FUNCTION keep_workflow_funding_identity();

-- Owner execution attribution survives callback/webhook delivery without a worker context.
ALTER TABLE payment_mo_funding_positions ADD COLUMN workflow_operation_id uuid, ADD COLUMN workflow_command_id uuid;
ALTER TABLE payment_mo_compensations ADD COLUMN workflow_operation_id uuid, ADD COLUMN workflow_command_id uuid;
ALTER TABLE procurement_effect_locks ADD COLUMN workflow_command_id uuid;

-- Existing confirmed Owner state becomes the initial control boundary. Keep
-- history in its original Owner/event rows; do not invent commands or ACKs.
INSERT INTO order_workflow_mo_controls(merchant_order_id,agency_order_id,purchase_closed,cancelled,updated_at)
 SELECT id,agency_order_id,state IN ('PLACED','FAILED','CANCELLED'),state='CANCELLED',clock_timestamp()
 FROM merchant_orders;
UPDATE payment_mo_funding_positions f SET merchant_order_id=mo.id
 FROM merchant_orders mo WHERE mo.allocation_id=f.allocation_id AND mo.agency_order_id=f.agency_order_id;
