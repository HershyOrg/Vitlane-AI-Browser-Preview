-- Forward-only reducer cutover. Existing writers must be stopped after draining
-- their queues. Economic evidence is preserved; unresolved effects are never
-- converted into an idle process or guessed to have failed.
LOCK TABLE agency_order_processes,order_process_events,order_process_commands,
 order_workflow_operations,order_workflow_mo_controls,procurement_effect_locks,
 payment_mo_funding_positions,payment_mo_compensations,payment_external_operations
 IN ACCESS EXCLUSIVE MODE;

DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM order_workflow_operations WHERE status NOT IN ('SUCCEEDED','REJECTED'))
 OR EXISTS(SELECT 1 FROM order_process_commands WHERE state NOT IN ('SUCCEEDED','REJECTED','ABANDONED'))
 OR EXISTS(SELECT 1 FROM order_process_events WHERE applied_at IS NULL)
 OR EXISTS(SELECT 1 FROM merchant_orders mo WHERE NOT EXISTS(SELECT 1 FROM agency_order_processes p WHERE p.agency_order_id=mo.agency_order_id))
 OR EXISTS(SELECT 1 FROM procurement_effect_locks WHERE state NOT IN ('PLACED','FAILED'))
 OR EXISTS(SELECT 1 FROM merchant_orders WHERE state IN ('PLACEMENT_PENDING','PLACEMENT_UNKNOWN'))
 OR EXISTS(SELECT 1 FROM payment_mo_funding_positions WHERE state IN ('ACTIVATION_PENDING','ACTIVATION_UNKNOWN','RELEASE_PENDING','RELEASE_UNKNOWN'))
 OR EXISTS(SELECT 1 FROM payment_mo_funding_positions f JOIN merchant_orders mo ON mo.id=f.merchant_order_id
   WHERE f.state='ACTIVE' AND mo.state NOT IN ('PLACED','FAILED','CANCELLED'))
 OR EXISTS(SELECT 1 FROM payment_mo_compensations WHERE state<>'SUCCEEDED')
 OR EXISTS(SELECT 1 FROM payment_external_operations WHERE state NOT IN ('SUCCEEDED','FAILED','CANCELLED'))
 THEN RAISE EXCEPTION 'ORDER_REDUCER_CUTOVER_REQUIRES_RECONCILIATION' USING ERRCODE='55000'; END IF;
END $$;

CREATE TABLE order_process_execution_history (
 source text NOT NULL,
 identity text NOT NULL,
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 evidence jsonb NOT NULL,
 archived_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(agency_order_id,source,identity)
);
INSERT INTO order_process_execution_history(source,identity,agency_order_id,evidence)
 SELECT 'operation',id::text,agency_order_id,to_jsonb(o) FROM order_workflow_operations o;
INSERT INTO order_process_execution_history(source,identity,agency_order_id,evidence)
 SELECT 'request',request_id,agency_order_id,to_jsonb(r) FROM order_workflow_requests r;
INSERT INTO order_process_execution_history(source,identity,agency_order_id,evidence)
 SELECT 'effect',id::text,agency_order_id,to_jsonb(c) FROM order_process_commands c;

-- The old control/operation/command rows cease to be runtime authorities. Their
-- IDs remain in the append-only history and Owner evidence above and below.
DO $$ DECLARE c record; BEGIN
 FOR c IN SELECT conrelid::regclass AS relation,conname FROM pg_constraint
 WHERE contype='f' AND confrelid IN ('order_workflow_operations'::regclass,
 'order_workflow_mo_controls'::regclass,'order_process_commands'::regclass)
 LOOP EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',c.relation,c.conname); END LOOP;
END $$;
DROP TABLE order_workflow_requests;
DROP TABLE order_workflow_mo_controls;
DROP TABLE order_workflow_operations;
DROP TABLE order_process_commands;
ALTER TABLE agency_order_processes RENAME COLUMN workflow TO process_state;
ALTER TABLE order_process_decisions RENAME COLUMN workflow TO process_state;
ALTER TABLE order_process_decisions RENAME COLUMN commands TO effects;
ALTER TABLE agency_order_process_merchant_orders RENAME COLUMN attention_command_id TO attention_effect_id;

CREATE TABLE order_process_effects (
 id uuid PRIMARY KEY,
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 merchant_order_id uuid,
 flow_id uuid NOT NULL,
 request_id text NOT NULL DEFAULT '',
 target text NOT NULL CHECK(target IN ('AGENCYORDER','PROCUREMENT','PAYMENT','LOGISTICS','SUPPORT')),
 type text NOT NULL CHECK(type<>''),
 idempotency_key text NOT NULL,
 input_hash text NOT NULL CHECK(length(input_hash)=64),
 payload jsonb NOT NULL,
 caused_by_event_id bigint REFERENCES order_process_events(id),
 delivery_state text NOT NULL DEFAULT 'PENDING' CHECK(delivery_state IN ('PENDING','CONSUMED')),
 claim_version bigint NOT NULL DEFAULT 0 CHECK(claim_version>=0),
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
 lease_until timestamptz,
 next_attempt_at timestamptz NOT NULL,
 consumed_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 UNIQUE(agency_order_id,idempotency_key),
 FOREIGN KEY(merchant_order_id,agency_order_id) REFERENCES merchant_orders(id,agency_order_id),
 CHECK((delivery_state='CONSUMED')=(consumed_at IS NOT NULL))
);
CREATE INDEX order_effect_delivery_due ON order_process_effects(target,next_attempt_at)
 WHERE delivery_state='PENDING';

CREATE TABLE payment_instruction_claims (
 id uuid PRIMARY KEY,
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 claim jsonb NOT NULL,
 state text NOT NULL CHECK(state IN ('PENDING','CONFIRMED','REJECTED')),
 reason text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 confirmed_at timestamptz
);
CREATE TABLE agency_order_instruction_claims (
 agency_order_id uuid PRIMARY KEY REFERENCES agency_orders(id) ON DELETE CASCADE,
 claim_id uuid NOT NULL UNIQUE,
 claim jsonb NOT NULL,
 consumed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Logistics decides cancellation eligibility under its own unit locks. A
-- reservation fixes the race with delivery without pretending units vanished.
CREATE TABLE logistics_cancellation_reservations (
 id uuid PRIMARY KEY,
 agency_order_id uuid NOT NULL,
 merchant_order_id uuid NOT NULL,
 flow_id uuid NOT NULL,
 undelivered_units integer NOT NULL CHECK(undelivered_units>=0),
 state text NOT NULL CHECK(state IN ('RESERVED','CONFIRMED','RELEASED')),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 FOREIGN KEY(merchant_order_id,agency_order_id) REFERENCES merchant_orders(id,agency_order_id)
);
CREATE UNIQUE INDEX logistics_one_cancellation_reservation ON logistics_cancellation_reservations(merchant_order_id)
 WHERE state IN ('RESERVED','CONFIRMED');

CREATE FUNCTION preserve_process_effect_input() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF ROW(NEW.id,NEW.agency_order_id,NEW.merchant_order_id,NEW.flow_id,NEW.request_id,NEW.target,
 NEW.type,NEW.idempotency_key,NEW.input_hash,NEW.payload,NEW.caused_by_event_id,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.agency_order_id,OLD.merchant_order_id,OLD.flow_id,OLD.request_id,OLD.target,
 OLD.type,OLD.idempotency_key,OLD.input_hash,OLD.payload,OLD.caused_by_event_id,OLD.created_at)
 THEN RAISE EXCEPTION 'ORDER_PROCESS_EFFECT_IMMUTABLE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER process_effect_input_immutable BEFORE UPDATE ON order_process_effects
 FOR EACH ROW EXECUTE FUNCTION preserve_process_effect_input();

CREATE TABLE order_process_requests (
 agency_order_id uuid NOT NULL REFERENCES agency_orders(id) ON DELETE CASCADE,
 request_id text NOT NULL,
 request_hash text NOT NULL CHECK(length(request_hash)=64),
 merchant_order_id uuid,
 actor_id text NOT NULL,
 receipt jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(agency_order_id,request_id),
 FOREIGN KEY(merchant_order_id,agency_order_id) REFERENCES merchant_orders(id,agency_order_id)
);

DO $$ DECLARE owner_table text; BEGIN
 FOREACH owner_table IN ARRAY ARRAY['procurement_process_inputs','agency_order_process_inputs','logistics_process_inputs','payment_process_inputs'] LOOP
 EXECUTE format('CREATE TABLE %I (
 id uuid PRIMARY KEY,agency_order_id uuid NOT NULL REFERENCES agency_orders(id),merchant_order_id uuid,
 actor_id text NOT NULL,request_hash text NOT NULL CHECK(length(request_hash)=64),input_hash text NOT NULL CHECK(length(input_hash)=64),
 payload jsonb NOT NULL,request jsonb NOT NULL,created_at timestamptz NOT NULL,
 FOREIGN KEY(merchant_order_id,agency_order_id) REFERENCES merchant_orders(id,agency_order_id))',owner_table);
 END LOOP;
END $$;

ALTER TABLE order_process_events RENAME COLUMN operation_id TO flow_id;
ALTER TABLE order_process_events RENAME COLUMN causation_command_id TO causation_effect_id;
ALTER TABLE order_process_events DROP CONSTRAINT order_process_events_source_check;
ALTER TABLE order_process_events ADD CONSTRAINT order_process_events_source_check CHECK(source IN
 ('AGENCYORDER','PAYMENT','SETTLEMENT','PROCUREMENT','LOGISTICS','SUPPORT','CUSTOMER','OPERATOR','TIMER','WATCHDOG','SYSTEM'));
ALTER TABLE procurement_effect_locks RENAME COLUMN operation_id TO process_flow_id;
ALTER TABLE procurement_effect_locks RENAME COLUMN workflow_command_id TO process_effect_id;
ALTER TABLE payment_mo_funding_positions RENAME COLUMN workflow_operation_id TO process_flow_id;
ALTER TABLE payment_mo_funding_positions RENAME COLUMN workflow_command_id TO process_effect_id;
ALTER TABLE payment_mo_compensations RENAME COLUMN workflow_operation_id TO process_flow_id;
ALTER TABLE payment_mo_compensations RENAME COLUMN workflow_command_id TO process_effect_id;

-- Bootstrap only from confirmed, owned rows. No historical requests, customer
-- approvals, provider outcomes or open effect permissions are manufactured.
UPDATE agency_order_processes p SET process_state=(COALESCE(p.process_state,'{}'::jsonb)-'foldVersion')||jsonb_build_object(
 'userId',a.user_id::text,'authorization',jsonb_build_object(
 'kind',approved.authorization_kind,'hash',approved.authorization_hash,
 'executionProfileHash',a.execution_profile_hash,'executionMode',a.merchant_execution_mode),
 'effects','{}'::jsonb,'modelVersion',3)
 FROM agency_orders a JOIN agency_order_procurement_authorizations approved ON approved.agency_order_id=a.id
 WHERE p.agency_order_id=a.id;
UPDATE agency_order_processes p SET process_state=jsonb_set(process_state,'{merchantOrders}',
 COALESCE((SELECT jsonb_object_agg(mo.id::text,COALESCE(p.process_state->'merchantOrders'->mo.id::text,'{}'::jsonb)||jsonb_build_object(
 'allocationId',mo.allocation_id::text,'ownerState',mo.state,'taskId',task.id::text,
 'fundingPositionId',funding.id::text,'fundingState',funding.state,'effects','{}'::jsonb,
 'unitIds',COALESCE((SELECT jsonb_agg(u.id::text ORDER BY u.id) FROM merchant_order_units u WHERE u.merchant_order_id=mo.id),'[]'::jsonb),
 'unitManifest',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',u.id::text,'lineId',u.line_id,'unitIndex',u.unit_index) ORDER BY u.id) FROM merchant_order_units u WHERE u.merchant_order_id=mo.id),'[]'::jsonb),
 'unitCount',(SELECT count(*) FROM merchant_order_units u WHERE u.merchant_order_id=mo.id),'shopDomain',mo.shop_domain))
 FROM merchant_orders mo LEFT JOIN merchant_order_execution_tasks task ON task.merchant_order_id=mo.id
 LEFT JOIN payment_mo_funding_positions funding ON funding.merchant_order_id=mo.id
 WHERE mo.agency_order_id=p.agency_order_id),'{}'::jsonb));

UPDATE agency_order_processes p SET process_state=jsonb_set(process_state,'{fundingByAllocation}',
 COALESCE((SELECT jsonb_object_agg(f.allocation_id::text,jsonb_build_object('positionId',f.id::text,'state',f.state)) FROM payment_mo_funding_positions f WHERE f.agency_order_id=p.agency_order_id),'{}'::jsonb));

CREATE FUNCTION reject_order_action_input_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'Owner process input is immutable'; END IF;
 RETURN NEW;
END $$;
DO $$ DECLARE owner_table text;
BEGIN
 FOREACH owner_table IN ARRAY ARRAY['procurement_process_inputs','agency_order_process_inputs','logistics_process_inputs','payment_process_inputs'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_action_input BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION reject_order_action_input_change()',owner_table);
 END LOOP;
END $$;

-- The confirmed identity may not be changed while a result is being recorded.
CREATE FUNCTION preserve_payment_instruction_claim() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.agency_order_id,NEW.claim,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.agency_order_id,OLD.claim,OLD.created_at)
 THEN RAISE EXCEPTION 'PAYMENT_INSTRUCTION_CLAIM_IMMUTABLE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_instruction_claim BEFORE UPDATE ON payment_instruction_claims
 FOR EACH ROW EXECUTE FUNCTION preserve_payment_instruction_claim();
CREATE TRIGGER immutable_instruction_consumption BEFORE UPDATE ON agency_order_instruction_claims
 FOR EACH ROW EXECUTE FUNCTION reject_order_action_input_change();
