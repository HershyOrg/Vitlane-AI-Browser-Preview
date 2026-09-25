package postgres

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

const paypalMOFundingMigration = "000091_paypal_mo_funding_clean_cut.up.sql"

const (
	paypalSandboxProfileHash = "0x6b5f02663c9702ec58d6c7f0547ae0fdf150445ae500fcab91206e67de9c6665"
	giwaTestnetProfileHash   = "0x1aca907eaa5dae72e8a25e215c854c7b913ae9ef4ce2b47b29a2edb9e0c91732"
)

func TestPayPalMOFundingMigrationContract(t *testing.T) {
	body, err := os.ReadFile("../../../../migrations/" + paypalMOFundingMigration)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(body)
	required := []string{
		"cannot apply MO funding clean cut while REAL_MONEY/LIVE facts exist",
		"state IN ('SENT','UNKNOWN')",
		"payment_paypal_dispute_cases WHERE state='OPEN'",
		"nonterminal money or merchant effects",
		"requires an archived and empty legacy commerce graph",
		"DROP TRIGGER IF EXISTS trg_procurement_authorization_legacy_insert",
		"DROP FUNCTION IF EXISTS reject_legacy_procurement_authorization_insert()",
		"DO $authorization_clean_cut$",
		"ALTER COLUMN locale SET NOT NULL",
		"agency_order_procurement_authorizations_current_kind_check",
		"CHECK (authorization_kind = 'MANUAL_OPERATOR_PURCHASE')",
		"agency_order_procurement_authorizations_current_payload_check",
		"CREATE TABLE agency_order_mo_allocations",
		"UNIQUE (agency_order_id, checkout_ordinal)",
		"trg_agency_order_mo_allocation_immutable",
		"ADD COLUMN placement_evidence_kind TEXT",
		"'SANDBOX_TEST_EVIDENCE','LIVE_MERCHANT_EFFECT_EVIDENCE'",
		"merchant_orders_placement_evidence_shape_check",
		"trg_merchant_order_placement_evidence_immutable",
		"ADD COLUMN placement_receipt_safe_ref TEXT",
		"ADD COLUMN placement_actual_amount_minor BIGINT",
		"CREATE TABLE payment_paypal_authorizations",
		"honor_refreshed_at TIMESTAMPTZ NOT NULL",
		"CREATE TABLE payment_paypal_reauthorizations",
		"customer_payment_id UUID NOT NULL UNIQUE",
		"agency_order_id UUID NOT NULL UNIQUE",
		"'AUTHORIZE_SUBMITTED','AUTHORIZE_PENDING','AUTHORIZE_OUTCOME_UNKNOWN'",
		"'AUTHORIZE_COMPLETED','AUTHORIZE_DECLINED','AUTHORIZE_FAILED'",
		"CREATE TABLE payment_mo_funding_positions",
		"'AVAILABLE','ACTIVATION_PENDING','ACTIVATION_UNKNOWN','ACTIVE'",
		"CREATE TABLE payment_mo_cash_receipts",
		"economics_reconciled BOOLEAN NOT NULL",
		"gross_minor - processor_fee_minor = net_receivable_minor",
		"NOT economics_reconciled",
		"processor_fee_minor IS NULL",
		"net_receivable_minor IS NULL",
		"CREATE TABLE payment_mo_compensations",
		"action IN ('VOID','REFUND','TVIT_REFUND')",
		"payment_external_operations_owner_identity_unique",
		"CREATE TABLE payment_paypal_refund_adoptions",
		"trg_payment_paypal_refund_adoption_immutable",
		"'PAYPAL_AUTH_VOID','PAYPAL_REAUTHORIZE','PAYPAL_MO_REFUND'",
		"procurement_manifests_one_planning_source",
		"ADD COLUMN allocation_id UUID NOT NULL",
		"ADD COLUMN funding_position_id UUID NOT NULL UNIQUE",
		"'MERCHANT_EFFECT_CUSTOMER_STOP_AFTER_FUNDING'",
		"idx_agency_order_refund_requests_open_allocation",
		"ADD COLUMN mo_compensation_id UUID",
		"DROP TABLE payment_customer_refund_attempts",
		"DROP TABLE payment_customer_refunds",
		"ADD COLUMN mo_cash_receipt_id UUID NOT NULL",
		"DROP COLUMN reason",
		"DROP COLUMN review_contract_version",
		"ALTER COLUMN public_rationale SET NOT NULL",
		"DROP TABLE payment_refund_slice_claims",
		"DROP TABLE agency_order_refund_request_items",
		"ALTER TABLE merchant_order_units DROP COLUMN slice_id",
		"ALTER TABLE logistics_expected_units DROP COLUMN slice_id",
		"DROP TABLE agency_order_refund_slices",
	}
	for _, fragment := range required {
		if !strings.Contains(payload, fragment) {
			t.Errorf("MO funding migration is missing %q", fragment)
		}
	}

	guardOffset := strings.Index(payload, "DO $cutover_guard$")
	guardEnd := strings.Index(payload, "$cutover_guard$;")
	authorizationCut := strings.Index(payload, "DO $authorization_clean_cut$")
	firstDDL := strings.Index(payload, "CREATE TABLE agency_order_mo_allocations")
	if guardOffset < 0 || guardEnd <= guardOffset || authorizationCut <= guardEnd ||
		firstDDL <= authorizationCut {
		t.Fatal("destructive/schema DDL must follow the complete cutover guard")
	}

	authorizationStart := strings.Index(payload, "CREATE TABLE payment_paypal_authorizations")
	authorizationEnd := strings.Index(payload, "CREATE TABLE payment_mo_funding_positions")
	if authorizationStart < 0 || authorizationEnd <= authorizationStart {
		t.Fatal("cannot isolate PayPal authorization schema")
	}
	authorizationDDL := payload[authorizationStart:authorizationEnd]
	if strings.Contains(authorizationDDL, "allocation_id") ||
		strings.Contains(authorizationDDL, "purchase_unit_reference") {
		t.Fatal("the full-order PayPal authorization must not be allocated per MO")
	}

	dropCascade := regexp.MustCompile(`(?is)DROP\s+(TABLE|COLUMN|CONSTRAINT|INDEX)[^;]*\bCASCADE\b`)
	if dropCascade.MatchString(payload) {
		t.Fatal("clean cut must remove dependencies explicitly, without DROP CASCADE")
	}
	if strings.Contains(payload, "LEGACY_NO_REAL_VALUE_V0") {
		t.Fatal("current clean-cut migration must not preserve the legacy authorization kind")
	}
	if strings.Contains(payload, "DROP INDEX idx_payment_funds_receipts_accepted") {
		t.Fatal("PayPal MO receipts must not weaken the GIWA whole-order receipt invariant")
	}
}

func TestPayPalMOFundingMigrationGuardAndFreshSchema(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	adminDatabase, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := isolatedCutoverDatabaseName(t)
	if _, err := adminDatabase.DB.ExecContext(
		ctx, "CREATE DATABASE "+quotePostgresIdentifier(databaseName)+" TEMPLATE template0",
	); err != nil {
		_ = adminDatabase.Close()
		t.Fatalf("create isolated MO funding database: %v", err)
	}

	var isolatedDatabase *Database
	t.Cleanup(func() {
		if isolatedDatabase != nil {
			if err := isolatedDatabase.Close(); err != nil {
				t.Errorf("close isolated MO funding database: %v", err)
			}
		}
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := adminDatabase.DB.ExecContext(
			cleanupContext,
			"DROP DATABASE "+quotePostgresIdentifier(databaseName)+" WITH (FORCE)",
		); err != nil {
			t.Errorf("drop isolated MO funding database: %v", err)
		}
		if err := adminDatabase.Close(); err != nil {
			t.Errorf("close MO funding admin database: %v", err)
		}
	})

	isolatedDatabase, err = Open(ctx, databaseURLWithName(t, databaseURL, databaseName))
	if err != nil {
		t.Fatalf("open isolated MO funding database: %v", err)
	}
	assertPostgres16(t, ctx, isolatedDatabase.DB)
	migrationDirectory := migrationTestDirectory(t)
	preCutoverDirectory := migrationDirectoryThrough(
		t, migrationDirectory, "000090_operator_order_lookup.up.sql",
	)
	if err := isolatedDatabase.Migrate(ctx, preCutoverDirectory); err != nil {
		t.Fatalf("migrate through pre-MO-funding schema: %v", err)
	}

	now := time.Date(2026, 8, 27, 6, 7, 8, 123456000, time.UTC)
	if _, err := isolatedDatabase.DB.ExecContext(ctx, `
		INSERT INTO payment_external_operations(
			id,purpose,owner_kind,owner_id,idempotency_key,request_hash,state,
			first_sent_at,created_at,updated_at
		) VALUES(
			'91000000-0000-4000-8000-000000000001','PAYPAL_CAPTURE',
			'PAYPAL_ATTEMPT','91000000-0000-4000-8000-000000000002',
			'guard-operation','guard-request','SENT',$1,$1,$1
		)
	`, now); err != nil {
		t.Fatalf("seed unresolved operation guard: %v", err)
	}
	if err := isolatedDatabase.Migrate(ctx, migrationDirectory); err == nil ||
		!strings.Contains(err.Error(), "SENT/UNKNOWN external operations") {
		t.Fatalf("unresolved operation migration error=%v", err)
	}
	var allocationTableExists bool
	if err := isolatedDatabase.DB.QueryRowContext(ctx, `
		SELECT to_regclass('agency_order_mo_allocations') IS NOT NULL
	`).Scan(&allocationTableExists); err != nil {
		t.Fatal(err)
	}
	if allocationTableExists {
		t.Fatal("guard failure left partially applied MO funding DDL")
	}
	if _, err := isolatedDatabase.DB.ExecContext(
		ctx, `DELETE FROM payment_external_operations WHERE idempotency_key='guard-operation'`,
	); err != nil {
		t.Fatal(err)
	}
	if err := isolatedDatabase.Migrate(ctx, migrationDirectory); err != nil {
		t.Fatalf("apply clean MO funding migration: %v", err)
	}

	assertMOFundingSchemaShape(t, ctx, isolatedDatabase)
	seedAndAssertMOFundingRelations(t, ctx, isolatedDatabase, now)
	seedAndAssertGIWAPrepaidEquivalence(t, ctx, isolatedDatabase, now)
}

func assertMOFundingSchemaShape(
	t *testing.T,
	ctx context.Context,
	database *Database,
) {
	t.Helper()
	var acceptedFundsReceiptIndexIsUnique bool
	if err := database.DB.QueryRowContext(ctx, `
		SELECT index_definition.indexdef LIKE 'CREATE UNIQUE INDEX%'
		FROM pg_indexes index_definition
		WHERE index_definition.schemaname='public'
		  AND index_definition.indexname='idx_payment_funds_receipts_accepted'
	`).Scan(&acceptedFundsReceiptIndexIsUnique); err != nil {
		t.Fatalf("read accepted whole-order receipt index: %v", err)
	}
	if !acceptedFundsReceiptIndexIsUnique {
		t.Fatal("accepted whole-order GIWA receipt must remain unique per payment")
	}
	for _, table := range []string{
		"agency_order_mo_allocations",
		"payment_paypal_authorizations",
		"payment_paypal_reauthorizations",
		"payment_mo_funding_positions",
		"payment_mo_cash_receipts",
		"payment_mo_compensations",
		"payment_paypal_refund_adoptions",
	} {
		var exists bool
		if err := database.DB.QueryRowContext(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, table,
		).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("required MO funding table %s does not exist", table)
		}
	}
	var legacyAuthorizationConstraintCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_constraint constraints
		WHERE constraints.conrelid =
		          'agency_order_procurement_authorizations'::regclass
		  AND constraints.contype='c'
		  AND pg_get_constraintdef(constraints.oid) LIKE '%LEGACY_NO_REAL_VALUE_V0%'
	`).Scan(&legacyAuthorizationConstraintCount); err != nil {
		t.Fatal(err)
	}
	if legacyAuthorizationConstraintCount != 0 {
		t.Fatalf("legacy authorization constraints=%d want=0",
			legacyAuthorizationConstraintCount)
	}
	var currentAuthorizationKindConstraintCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*)
		FROM pg_constraint constraints
		WHERE constraints.conrelid =
		          'agency_order_procurement_authorizations'::regclass
		  AND constraints.contype='c'
		  AND constraints.conname =
		          'agency_order_procurement_authorizations_current_kind_check'
		  AND pg_get_constraintdef(constraints.oid) =
		          'CHECK ((authorization_kind = ''MANUAL_OPERATOR_PURCHASE''::text))'
	`).Scan(&currentAuthorizationKindConstraintCount); err != nil {
		t.Fatal(err)
	}
	if currentAuthorizationKindConstraintCount != 1 {
		t.Fatalf("current authorization kind constraints=%d want=1",
			currentAuthorizationKindConstraintCount)
	}
	var legacyAuthorizationTriggerCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_trigger
		WHERE tgrelid='agency_order_procurement_authorizations'::regclass
		  AND tgname='trg_procurement_authorization_legacy_insert'
		  AND NOT tgisinternal
	`).Scan(&legacyAuthorizationTriggerCount); err != nil {
		t.Fatal(err)
	}
	if legacyAuthorizationTriggerCount != 0 {
		t.Fatalf("legacy authorization trigger count=%d want=0",
			legacyAuthorizationTriggerCount)
	}
	var authorizationLocaleNullable string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema='public'
		  AND table_name='agency_order_procurement_authorizations'
		  AND column_name='locale'
	`).Scan(&authorizationLocaleNullable); err != nil {
		t.Fatal(err)
	}
	if authorizationLocaleNullable != "NO" {
		t.Fatalf("authorization locale nullable=%s want NO",
			authorizationLocaleNullable)
	}
	for _, retired := range []string{
		"payment_refund_slice_claims",
		"payment_customer_refund_attempts",
		"payment_customer_refunds",
		"agency_order_refund_request_items",
		"agency_order_refund_slices",
	} {
		var exists bool
		if err := database.DB.QueryRowContext(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, retired,
		).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Errorf("retired monetary-slice table %s still exists", retired)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"merchant_orders", "allocation_id"},
		{"procurement_effect_locks", "funding_position_id"},
		{"procurement_effect_locks", "funding_state"},
		{"agency_order_cancellations", "allocation_id"},
		{"agency_order_cancellations", "merchant_order_id"},
		{"agency_order_refund_requests", "allocation_id"},
		{"agency_order_refund_requests", "merchant_order_id"},
		{"agency_order_refund_requests", "public_rationale"},
		{"payment_paypal_dispute_cases", "mo_cash_receipt_id"},
		{"payment_paypal_authorizations", "honor_refreshed_at"},
	} {
		var nullable string
		if err := database.DB.QueryRowContext(ctx, `
			SELECT is_nullable FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2
		`, column.table, column.name).Scan(&nullable); err != nil {
			t.Fatalf("read %s.%s: %v", column.table, column.name, err)
		}
		if nullable != "NO" {
			t.Errorf("%s.%s nullable=%s, want NO", column.table, column.name, nullable)
		}
	}
	for _, removed := range []struct{ table, name string }{
		{"settlement_command_outbox", "customer_refund_id"},
		{"payment_paypal_dispute_cases", "funds_receipt_id"},
	} {
		var count int
		if err := database.DB.QueryRowContext(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name=$2
		`, removed.table, removed.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("retired column %s.%s survived", removed.table, removed.name)
		}
	}
	var manifestSourceNullable string
	if err := database.DB.QueryRowContext(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema='public' AND table_name='procurement_manifests'
		  AND column_name='customer_payment_id'
	`).Scan(&manifestSourceNullable); err != nil {
		t.Fatal(err)
	}
	if manifestSourceNullable != "YES" {
		t.Errorf("procurement_manifests.customer_payment_id nullable=%s, want YES for source XOR",
			manifestSourceNullable)
	}
	for _, table := range []string{"merchant_order_units", "logistics_expected_units"} {
		var count int
		if err := database.DB.QueryRowContext(ctx, `
			SELECT count(*) FROM information_schema.columns
			WHERE table_schema='public' AND table_name=$1 AND column_name='slice_id'
		`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s.slice_id survived the clean cut", table)
		}
	}
}

func seedAndAssertMOFundingRelations(
	t *testing.T,
	ctx context.Context,
	database *Database,
	now time.Time,
) {
	t.Helper()
	const (
		userID       = "92000000-0000-4000-8000-000000000001"
		shippingID   = "92000000-0000-4000-8000-000000000002"
		sessionID    = "92000000-0000-4000-8000-000000000003"
		orderID      = "92000000-0000-4000-8000-000000000004"
		paymentID    = "92000000-0000-4000-8000-000000000005"
		attemptID    = "92000000-0000-4000-8000-000000000006"
		authorizeID  = "92000000-0000-4000-8000-000000000007"
		allocation1  = "92000000-0000-4000-8000-000000000008"
		allocation2  = "92000000-0000-4000-8000-000000000009"
		funding1     = "92000000-0000-4000-8000-000000000010"
		funding2     = "92000000-0000-4000-8000-000000000011"
		cash1        = "92000000-0000-4000-8000-000000000012"
		cash2        = "92000000-0000-4000-8000-000000000013"
		manifestID   = "92000000-0000-4000-8000-000000000014"
		merchant1    = "92000000-0000-4000-8000-000000000015"
		merchant2    = "92000000-0000-4000-8000-000000000016"
		task2        = "92000000-0000-4000-8000-000000000017"
		decision2    = "92000000-0000-4000-8000-000000000018"
		compensation = "92000000-0000-4000-8000-000000000019"
	)
	profileHash := paypalSandboxProfileHash
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed MO funding relation: %v", err)
		}
	}

	exec(`INSERT INTO users(id,status,created_at,updated_at)
		VALUES($1,'ACTIVE',$2,$2)`, userID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'US','M*** O**, US',decode('00','hex'),decode('00','hex'),
		1,'mo-hmac',$3,'ORDER_SHEET_INPUT')`, shippingID, userID, now)
	exec(`INSERT INTO agency_order_sheet_sessions(
		id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
		state,version,creation_key_hash,creation_request_hash,snapshot,
		created_at,expires_at,updated_at
	) VALUES($1,$2,gen_random_uuid(),1,'mo-cart','CONSUMED',1,'mo-key',
		'mo-request','{}',$3,$3::timestamptz + interval '20 minutes',$3)`,
		sessionID, userID, now)
	exec(`INSERT INTO agency_orders(
		id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
		source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
		idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
		payment_rail,provider_environment,asset,economic_effect,
		merchant_execution_mode,execution_profile_hash,issued_at,expires_at
	) VALUES($1,$2,$3,gen_random_uuid(),1,'mo-cart',$4,'mo-order-hash',
		'mo-order-key','ISSUED',1000,'USD','{}','PAYPAL','SANDBOX','USD',
		'NO_REAL_VALUE','SIMULATED_NO_EFFECT',$5,$6,$6::timestamptz + interval '20 minutes')`,
		orderID, userID, sessionID, shippingID, profileHash, now)

	exec(`INSERT INTO agency_order_mo_allocations(
		id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
		pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
		customer_gross_minor,currency,fee_policy_version,allocation_hash,
		execution_profile_hash,created_at
	) VALUES
		($1,$3,1,'merchant-1','one.example',360,20,20,40,400,'USD','mo-fee.v1',
		 '0x'||repeat('11',32),$4,$5),
		($2,$3,2,'merchant-2','two.example',550,30,20,50,600,'USD','mo-fee.v1',
		 '0x'||repeat('22',32),$4,$5)`, allocation1, allocation2, orderID, profileHash, now)
	exec(`INSERT INTO payment_customer_payments(
		id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
		amount_minor,currency,state,merchant_execution_mode,execution_profile_hash,
		version,created_at,updated_at
	) VALUES($1,$2,$3,'PAYPAL','SANDBOX','USD','NO_REAL_VALUE',1000,'USD',
		'AUTHORIZED','SIMULATED_NO_EFFECT',$4,1,$5,$5)`,
		paymentID, orderID, userID, profileHash, now)
	exec(`INSERT INTO payment_paypal_attempts(
		id,customer_payment_id,sequence,state,paypal_order_id,approval_url,
		return_nonce,version,created_at,updated_at
	) VALUES($1,$2,1,'AUTHORIZE_COMPLETED','PAYPAL-ORDER-MO',NULL,
		'mo-return-nonce',1,$3,$3)`, attemptID, paymentID, now)
	exec(`INSERT INTO payment_paypal_authorizations(
		id,customer_payment_id,agency_order_id,paypal_attempt_id,rail,
		provider_environment,paypal_order_id,payee_merchant_id,paypal_authorization_id,
		amount_minor,currency,execution_profile_hash,state,version,
		authorized_at,honor_refreshed_at,created_at,updated_at
	) VALUES($1,$2,$3,$4,'PAYPAL','SANDBOX','PAYPAL-ORDER-MO','MERCHANT-1','AUTH-MO-1',
		1000,'USD',$5,'PARTIALLY_CAPTURED',1,$6,$6,$6,$6)`,
		authorizeID, paymentID, orderID, attemptID, profileHash, now)
	exec(`INSERT INTO payment_mo_funding_positions(
		id,allocation_id,agency_order_id,customer_payment_id,paypal_authorization_id,
		rail,source,provider_environment,amount_minor,currency,execution_profile_hash,
		state,version,available_at,activated_at,created_at,updated_at
	) VALUES
		($1,$3,$5,$6,$7,'PAYPAL','PAYPAL_AUTHORIZATION','SANDBOX',400,'USD',$8,
		 'ACTIVE',1,$9,$9,$9,$9),
		($2,$4,$5,$6,$7,'PAYPAL','PAYPAL_AUTHORIZATION','SANDBOX',600,'USD',$8,
		 'ACTIVE',1,$9,$9,$9,$9)`,
		funding1, funding2, allocation1, allocation2, orderID, paymentID,
		authorizeID, profileHash, now)
	exec(`INSERT INTO payment_mo_cash_receipts(
		id,funding_position_id,allocation_id,agency_order_id,customer_payment_id,
		paypal_authorization_id,provider_environment,kind,provider_capture_id,
		gross_minor,economics_reconciled,processor_fee_minor,net_receivable_minor,currency,
		execution_profile_hash,occurred_at,created_at
	) VALUES
		($1,$3,$5,$7,$8,$9,'SANDBOX','PAYPAL_CAPTURE','CAPTURE-MO-1',400,true,20,380,
			 'USD',$10,$11,$11),
		($2,$4,$6,$7,$8,$9,'SANDBOX','PAYPAL_CAPTURE','CAPTURE-MO-2',600,false,NULL,NULL,
			 'USD',$10,$11,$11)`,
		cash1, cash2, funding1, funding2, allocation1, allocation2, orderID,
		paymentID, authorizeID, profileHash, now)

	var sharedPositionCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_mo_funding_positions
		WHERE paypal_authorization_id=$1
	`, authorizeID).Scan(&sharedPositionCount); err != nil {
		t.Fatal(err)
	}
	if sharedPositionCount != 2 {
		t.Fatalf("shared full-order authorization position count=%d want=2", sharedPositionCount)
	}
	var unreconciledCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_mo_cash_receipts
		WHERE id=$1 AND NOT economics_reconciled
		  AND processor_fee_minor IS NULL AND net_receivable_minor IS NULL
	`, cash2).Scan(&unreconciledCount); err != nil {
		t.Fatal(err)
	}
	if unreconciledCount != 1 {
		t.Fatalf("unreconciled PayPal capture count=%d want=1", unreconciledCount)
	}

	exec(`INSERT INTO procurement_manifests(
		id,agency_order_id,funds_receipt_id,customer_payment_id,snapshot_hash,
		execution_profile_hash,created_at
	) VALUES($1,$2,NULL,$3,'mo-manifest-hash',$4,$5)`,
		manifestID, orderID, paymentID, profileHash, now)
	exec(`INSERT INTO merchant_orders(
		id,agency_order_id,manifest_id,merchant_id,shop_domain,checkout_ordinal,
		checkout_snapshot,execution_mode,state,version,created_at,updated_at,allocation_id
	) VALUES
		($1,$3,$4,'merchant-1','one.example',1,'{}','SIMULATED_NO_EFFECT','PLANNED',1,$5,$5,$6),
		($2,$3,$4,'merchant-2','two.example',2,'{}','SIMULATED_NO_EFFECT','PLANNED',1,$5,$5,$7)`,
		merchant1, merchant2, orderID, manifestID, now, allocation1, allocation2)
	exec(`INSERT INTO merchant_order_execution_tasks(
		id,merchant_order_id,agency_order_id,state,assigned_operator_user_id,
		assigned_at,lease_until,version,created_at,updated_at
	) VALUES($1,$2,$3,'CLAIMED',$4,$5,$5::timestamptz + interval '30 minutes',1,$5,$5)`,
		task2, merchant2, orderID, userID, now)
	exec(`INSERT INTO procurement_decision_records(
		id,merchant_order_id,agency_order_id,task_id,decision,public_rationale,
		observed_condition,evidence_source,evidence_hash,observed_at,
		decided_by_user_id,task_version,authorization_hash,execution_profile_hash,
		idempotency_key,created_at
	) VALUES($1,$2,$3,$4,'WITHIN_AUTHORIZATION','Approved conditions match.',
		'Exact option and amount observed.','OPERATOR_OBSERVATION','evidence-hash-0001',$5,
		$6,1,'authorization-hash',$7,'decision-key',$5)`,
		decision2, merchant2, orderID, task2, now, userID, profileHash)
	exec(`INSERT INTO procurement_effect_locks(
		merchant_order_id,task_id,decision_record_id,authorization_hash,
		execution_profile_hash,operator_user_id,state,idempotency_key,started_at,
		funding_position_id,funding_state,funding_requested_at,funding_resolved_at
	) VALUES($1,$2,$3,'authorization-hash',$4,$5,'STARTED','effect-key',$6,
		$7,'FUNDED',$6,$6)`, merchant2, task2, decision2, profileHash, userID, now, funding2)

	if _, err := database.DB.ExecContext(ctx, `
		UPDATE procurement_effect_locks SET funding_position_id=$1
		WHERE merchant_order_id=$2
	`, funding1, merchant2); err == nil ||
		!strings.Contains(err.Error(), "belongs to another MO") {
		t.Fatalf("mismatched MO funding update error=%v", err)
	}

	exec(`INSERT INTO payment_mo_compensations(
		id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
		rail,provider_environment,action,cause,state,amount_minor,currency,
		execution_profile_hash,idempotency_key,version,approved_at,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,'PAYPAL','SANDBOX','REFUND',
		'CUSTOMER_REFUND_POST_EFFECT','APPROVED',400,'USD',$6,'compensation-key',1,$7,$7,$7)`,
		compensation, allocation1, funding1, orderID, paymentID, profileHash, now)
	if _, err := database.DB.ExecContext(ctx, `
		INSERT INTO payment_mo_compensations(
			id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
			rail,provider_environment,action,cause,state,amount_minor,currency,
			execution_profile_hash,idempotency_key,version,approved_at,created_at,updated_at
		) VALUES(gen_random_uuid(),$1,$2,$3,$4,'PAYPAL','SANDBOX','VOID',
			'CUSTOMER_CANCEL_PRE_EFFECT','APPROVED',600,'USD',$5,'invalid-void',1,$6,$6,$6)
	`, allocation2, funding2, orderID, paymentID, profileHash, now); err == nil ||
		!strings.Contains(err.Error(), "captured MO funding cannot be voided") {
		t.Fatalf("captured MO void error=%v", err)
	}

	if _, err := database.DB.ExecContext(ctx, `
		UPDATE agency_order_mo_allocations SET pass_through_minor=359 WHERE id=$1
	`, allocation1); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("allocation immutability error=%v", err)
	}

	// Sandbox keeps the complete production-shaped evidence record, while its
	// kind remains an explicit TEST fact. The DB must reject a LIVE evidence
	// claim for the same immutable SIMULATED_NO_EFFECT execution mode.
	exec(`UPDATE merchant_orders
		SET state='PLACED', external_order_ref='TEST-ORDER-ONE',
		    placement_evidence_kind='SANDBOX_TEST_EVIDENCE',
		    placement_receipt_safe_ref='receipt:test:one',
		    placement_actual_amount_minor=360,
		    placement_evidence_source='RECEIPT',
		    placement_evidence_hash='sha256:test-placement-one',
		    placement_observed_at=$2, placement_recorded_by_user_id=$3,
		    placement_recorded_at=$2
		WHERE id=$1`, merchant1, now, userID)
	var evidenceKind, externalOrderRef, receiptSafeRef string
	var actualAmountMinor int64
	if err := database.DB.QueryRowContext(ctx, `
		SELECT placement_evidence_kind, external_order_ref,
		       placement_receipt_safe_ref, placement_actual_amount_minor
		FROM merchant_orders WHERE id=$1
	`, merchant1).Scan(
		&evidenceKind, &externalOrderRef, &receiptSafeRef, &actualAmountMinor,
	); err != nil {
		t.Fatal(err)
	}
	if evidenceKind != "SANDBOX_TEST_EVIDENCE" || externalOrderRef != "TEST-ORDER-ONE" ||
		receiptSafeRef != "receipt:test:one" || actualAmountMinor != 360 {
		t.Fatalf("Sandbox placement evidence was not preserved: kind=%s order=%s receipt=%s amount=%d",
			evidenceKind, externalOrderRef, receiptSafeRef, actualAmountMinor)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE merchant_orders SET placement_receipt_safe_ref='receipt:test:changed'
		WHERE id=$1
	`, merchant1); err == nil || !strings.Contains(err.Error(), "placement evidence is immutable") {
		t.Fatalf("recorded placement evidence was mutable: %v", err)
	}
	if _, err := database.DB.ExecContext(ctx, `
		UPDATE merchant_orders
		SET state='PLACED', external_order_ref='LIVE-ORDER-TWO',
		    placement_evidence_kind='LIVE_MERCHANT_EFFECT_EVIDENCE',
		    placement_receipt_safe_ref='receipt:live:two',
		    placement_actual_amount_minor=640,
		    placement_evidence_source='RECEIPT',
		    placement_evidence_hash='sha256:live-placement-two',
		    placement_observed_at=$2, placement_recorded_by_user_id=$3,
		    placement_recorded_at=$2
		WHERE id=$1
	`, merchant2, now, userID); err == nil ||
		!strings.Contains(err.Error(), "merchant_orders_placement_evidence_shape_check") {
		t.Fatalf("Sandbox accepted a LIVE merchant-effect claim: %v", err)
	}
}

func seedAndAssertGIWAPrepaidEquivalence(
	t *testing.T,
	ctx context.Context,
	database *Database,
	now time.Time,
) {
	t.Helper()
	const (
		userID       = "93000000-0000-4000-8000-000000000001"
		shippingID   = "93000000-0000-4000-8000-000000000002"
		sessionID    = "93000000-0000-4000-8000-000000000003"
		orderID      = "93000000-0000-4000-8000-000000000004"
		paymentID    = "93000000-0000-4000-8000-000000000005"
		receiptID    = "93000000-0000-4000-8000-000000000006"
		allocationID = "93000000-0000-4000-8000-000000000007"
		fundingID    = "93000000-0000-4000-8000-000000000008"
		manifestID   = "93000000-0000-4000-8000-000000000009"
		merchantID   = "93000000-0000-4000-8000-000000000010"
		compensation = "93000000-0000-4000-8000-000000000011"
	)
	profileHash := giwaTestnetProfileHash
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("seed GIWA prepaid equivalence: %v", err)
		}
	}

	exec(`INSERT INTO users(id,status,created_at,updated_at)
		VALUES($1,'ACTIVE',$2,$2)`, userID, now)
	exec(`INSERT INTO shipping_snapshots(
		id,user_id,profile_version,country,masked_summary,encrypted_payload,
		payload_nonce,key_version,snapshot_hmac,created_at,source_kind
	) VALUES($1,$2,1,'US','G*** U**, US',decode('00','hex'),decode('00','hex'),
		1,'giwa-mo-hmac',$3,'ORDER_SHEET_INPUT')`, shippingID, userID, now)
	exec(`INSERT INTO agency_order_sheet_sessions(
		id,user_id,source_cart_id,source_cart_version,source_cart_snapshot_hash,
		state,version,creation_key_hash,creation_request_hash,snapshot,
		created_at,expires_at,updated_at
	) VALUES($1,$2,gen_random_uuid(),1,'giwa-mo-cart','CONSUMED',1,'giwa-mo-key',
		'giwa-mo-request','{}',$3,$3::timestamptz + interval '20 minutes',$3)`,
		sessionID, userID, now)
	exec(`INSERT INTO agency_orders(
		id,user_id,order_sheet_session_id,source_cart_id,source_cart_version,
		source_cart_snapshot_hash,shipping_snapshot_id,snapshot_hash,
		idempotency_key_hash,status,customer_payable_minor,currency,snapshot,
		payment_rail,provider_environment,asset,economic_effect,
		merchant_execution_mode,execution_profile_hash,issued_at,expires_at
	) VALUES($1,$2,$3,gen_random_uuid(),1,'giwa-mo-cart',$4,'giwa-order-hash',
		'giwa-order-key','ISSUED',500,'USD','{}','GIWA','TESTNET','TVITUSD',
		'NO_REAL_VALUE','SIMULATED_NO_EFFECT',$5,$6,$6::timestamptz + interval '20 minutes')`,
		orderID, userID, sessionID, shippingID, profileHash, now)
	exec(`INSERT INTO agency_order_mo_allocations(
		id,agency_order_id,checkout_ordinal,merchant_id,shop_domain,
		pass_through_minor,fee_variable_minor,fee_fixed_minor,fee_total_minor,
		customer_gross_minor,currency,fee_policy_version,allocation_hash,
		execution_profile_hash,created_at
	) VALUES($1,$2,1,'giwa-merchant','giwa.example',495,5,0,5,500,'USD',
		'tvit-mo-fee.v1','0x'||repeat('33',32),$3,$4)`,
		allocationID, orderID, profileHash, now)
	exec(`INSERT INTO payment_customer_payments(
		id,agency_order_id,user_id,rail,provider_environment,asset,economic_effect,
		amount_minor,currency,state,merchant_execution_mode,execution_profile_hash,
		version,created_at,updated_at
	) VALUES($1,$2,$3,'GIWA','TESTNET','TVITUSD','NO_REAL_VALUE',500,'USD',
		'CAPTURED','SIMULATED_NO_EFFECT',$4,1,$5,$5)`,
		paymentID, orderID, userID, profileHash, now)
	exec(`INSERT INTO payment_funds_receipts(
		id,customer_payment_id,agency_order_id,kind,provider_environment,
		execution_profile_hash,order_hash,pay_tx_hash,amount_minor,currency,
		accepted,occurred_at,created_at
	) VALUES($1,$2,$3,'GIWA_FINALIZED_PAY','TESTNET',$4,
		'0x'||repeat('44',32),'0x'||repeat('55',32),500,'USD',true,$5,$5)`,
		receiptID, paymentID, orderID, profileHash, now)
	exec(`INSERT INTO payment_mo_funding_positions(
		id,allocation_id,agency_order_id,customer_payment_id,paypal_authorization_id,
		rail,source,provider_environment,amount_minor,currency,execution_profile_hash,
		state,version,available_at,created_at,updated_at
	) VALUES($1,$2,$3,$4,NULL,'GIWA','GIWA_PREPAID','TESTNET',500,'USD',$5,
		'AVAILABLE',1,$6,$6,$6)`, fundingID, allocationID, orderID, paymentID,
		profileHash, now)
	exec(`INSERT INTO procurement_manifests(
		id,agency_order_id,funds_receipt_id,customer_payment_id,snapshot_hash,
		execution_profile_hash,created_at
	) VALUES($1,$2,$3,NULL,'giwa-manifest-hash',$4,$5)`,
		manifestID, orderID, receiptID, profileHash, now)
	exec(`INSERT INTO merchant_orders(
		id,agency_order_id,manifest_id,merchant_id,shop_domain,checkout_ordinal,
		checkout_snapshot,execution_mode,state,version,created_at,updated_at,allocation_id
	) VALUES($1,$2,$3,'giwa-merchant','giwa.example',1,'{}',
		'SIMULATED_NO_EFFECT','PLANNED',1,$4,$4,$5)`,
		merchantID, orderID, manifestID, now, allocationID)
	exec(`INSERT INTO payment_mo_compensations(
		id,allocation_id,funding_position_id,agency_order_id,customer_payment_id,
		rail,provider_environment,action,cause,state,amount_minor,currency,
		execution_profile_hash,idempotency_key,version,approved_at,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,'GIWA','TESTNET','TVIT_REFUND',
		'CUSTOMER_CANCEL_PRE_EFFECT','APPROVED',500,'USD',$6,'giwa-compensation-key',
		1,$7,$7,$7)`, compensation, allocationID, fundingID, orderID, paymentID,
		profileHash, now)

	var paypalAuthorizationCount, syntheticCashReceiptCount int
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_paypal_authorizations WHERE customer_payment_id=$1
	`, paymentID).Scan(&paypalAuthorizationCount); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM payment_mo_cash_receipts WHERE customer_payment_id=$1
	`, paymentID).Scan(&syntheticCashReceiptCount); err != nil {
		t.Fatal(err)
	}
	if paypalAuthorizationCount != 0 || syntheticCashReceiptCount != 0 {
		t.Fatalf("GIWA truthfulness: paypal authorizations=%d synthetic MO cash receipts=%d",
			paypalAuthorizationCount, syntheticCashReceiptCount)
	}
}
